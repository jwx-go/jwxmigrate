package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
)

// Edit represents a byte-range replacement in source code.
type Edit struct {
	Start int
	End   int
	New   string
}

// FixResult summarizes what was fixed in a single file and what remains.
type FixResult struct {
	File      string
	Applied   []string  // rule IDs that were applied
	Remaining []Finding // issues that could not be auto-fixed
}

// FixFile applies mechanical fixes to a single Go file in-place.
// It also collects non-mechanical findings from the same parse so the
// caller can report remaining issues without re-scanning.
func FixFile(filePath string, rules []CompiledRule) (*FixResult, error) {
	// Try type-checked loading first for type-aware fixes.
	pf := parseGoFileTyped(filePath)
	if pf == nil {
		var err error
		pf, err = parseGoFile(filePath, filePath)
		if err != nil {
			return nil, err
		}
	}
	if pf == nil {
		// No v3 imports — nothing to fix, caller treats nil result as skip.
		return nil, nil //nolint:nilnil
	}

	edits := collectEdits(pf, rules)

	// Correlate edits back to findings by (ruleID, line) so the remaining
	// list reports every finding that did NOT receive an edit — mechanical
	// or not. This catches rules that matched in Check but whose fixer
	// couldn't emit an edit (e.g. kindRemoved calls nested inside a
	// composite literal with no statement parent). Previously those rules
	// were silently dropped from the remaining list because the old logic
	// blanket-skipped everything marked Mechanical.
	type fixKey struct {
		ruleID string
		line   int
	}
	fixedAt := make(map[fixKey]struct{}, len(edits))
	for _, e := range edits {
		if e.line == 0 {
			continue // side-effect edit (import injection); no finding
		}
		fixedAt[fixKey{ruleID: e.ruleID, line: e.line}] = struct{}{}
	}

	allFindings := scanGoFileAST(pf, rules, CheckOptions{})
	var unfixed []Finding
	for _, f := range allFindings {
		if _, fixed := fixedAt[fixKey{ruleID: f.RuleID, line: f.Line}]; fixed {
			continue
		}
		unfixed = append(unfixed, f)
	}
	if len(edits) == 0 {
		if len(unfixed) == 0 {
			// Nothing to fix and nothing remaining — caller treats nil as skip.
			return nil, nil //nolint:nilnil
		}
		return &FixResult{File: filePath, Remaining: unfixed}, nil
	}

	applied := make(map[string]struct{})
	for _, e := range edits {
		applied[e.ruleID] = struct{}{}
	}
	var ids []string
	for id := range applied {
		ids = append(ids, id)
	}
	sort.Strings(ids)

	result := applyEdits(pf.Src, edits)
	if err := writeFormatted(filePath, result, ids); err != nil {
		return nil, err
	}

	return &FixResult{File: filePath, Applied: ids, Remaining: unfixed}, nil
}

// writeFormatted gofmts result and writes it to filePath. If format.Source
// fails, the file is left untouched and an error naming the contributing
// rule IDs is returned — silently writing unformatted (likely broken) Go
// would hand users a code base that their next `go build` chokes on, with
// no signal about which rule to blame.
func writeFormatted(filePath string, result []byte, ruleIDs []string) error {
	formatted, err := format.Source(result)
	if err != nil {
		return fmt.Errorf("refusing to write %s: post-edit source failed to format (rules: %s): %w", filePath, strings.Join(ruleIDs, ","), err)
	}
	if err := os.WriteFile(filePath, formatted, 0o644); err != nil {
		return fmt.Errorf("writing %s: %w", filePath, err)
	}
	return nil
}

// taggedEdit is an Edit with its originating rule ID and the source line
// of the matched node. The line is used to correlate edits back to Check
// findings so FixFile can report which findings remain unfixed at the
// per-location level (not per-rule).
//
// Side-effect edits that are not tied to a specific finding — e.g. the
// import-injection edits emitted by ensureOsImport / ensureExtensionImports
// — use line=0. Zero is never a real Check-finding line, so it can't
// falsely mark any finding as fixed.
type taggedEdit struct {
	Edit

	ruleID string
	line   int
}

// collectEdits walks the AST and collects all applicable mechanical edits.
func collectEdits(pf *ParsedGoFile, rules []CompiledRule) []taggedEdit {
	// Build helper maps.
	localToBasePkg := make(map[string]string)
	for localName, importPath := range pf.V3Imports {
		localToBasePkg[localName] = goPkgName(importPath)
	}
	matchesPkg := func(localName, expectedPkg string) bool {
		basePkg, ok := localToBasePkg[localName]
		return ok && basePkg == expectedPkg
	}

	file := pf.FileSet.File(pf.ASTFile.Pos())
	if file == nil {
		return nil
	}
	base := file.Base()

	byteOffset := func(pos token.Pos) int {
		return int(pos) - base
	}
	lineOf := func(n ast.Node) int {
		return pf.FileSet.Position(n.Pos()).Line
	}

	// Pre-index: map ast.Node → parent statement for statement deletion.
	stmtOf := buildStmtMap(pf.ASTFile)

	var edits []taggedEdit

	// Pre-collect CallExpr fun nodes.
	callFuns := make(map[ast.Node]struct{})
	ast.Inspect(pf.ASTFile, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if call, ok := n.(*ast.CallExpr); ok {
			callFuns[call.Fun] = struct{}{}
		}
		return true
	})

	for i := range rules {
		r := &rules[i]
		if !r.Mechanical && !canFixWithTypes(r, pf) {
			continue
		}

		for j := range r.ASTMatchers {
			m := &r.ASTMatchers[j]

			ast.Inspect(pf.ASTFile, func(n ast.Node) bool {
				if n == nil {
					return false
				}

				switch node := n.(type) {
				case *ast.ImportSpec:
					if m.Kind != MatchImportSpec {
						return true
					}
					importPath := strings.Trim(node.Path.Value, `"`)
					if !strings.Contains(importPath, m.ImportPath) {
						return true
					}
					e := fixImportChange(pf, node, r, byteOffset)
					if e != nil {
						edits = append(edits, taggedEdit{Edit: *e, ruleID: r.ID, line: lineOf(node)})
					}

				case *ast.CallExpr:
					sel, ok := node.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}

					// Rules that need a single target name for rewriting (rename,
					// signature_change) cannot use NamePattern — skip them here so
					// we don't try to fix a call we can't safely rewrite.
					if m.NamePattern != nil && (r.Kind == kindRename || r.Kind == kindSignatureChange) {
						return true
					}

					switch m.Kind {
					case MatchCallExpr:
						ident, ok := sel.X.(*ast.Ident)
						if !ok || !m.MatchesName(sel.Sel.Name) || !matchesPkg(ident.Name, m.PkgName) {
							return true
						}
					case MatchMethodCall:
						if !m.MatchesName(sel.Sel.Name) {
							return true
						}
						if pf.TypesInfo != nil && !isV3Type(pf.TypesInfo, sel.X) {
							return true
						}
					default:
						return true
					}

					ee := fixCall(pf, node, r, byteOffset, stmtOf)
					for _, e := range ee {
						edits = append(edits, taggedEdit{Edit: e, ruleID: r.ID, line: lineOf(node)})
					}

				case *ast.SelectorExpr:
					if _, isCallFun := callFuns[node]; isCallFun {
						return true
					}
					if m.Kind != MatchSelectorExpr {
						return true
					}
					if m.NamePattern != nil && (r.Kind == kindRename || r.Kind == kindSignatureChange) {
						return true
					}
					ident, ok := node.X.(*ast.Ident)
					if !ok || !m.MatchesName(node.Sel.Name) || !matchesPkg(ident.Name, m.PkgName) {
						return true
					}
					if r.Kind == kindMovedToExtension {
						for _, e := range fixMoveToExtensionSelectorExpr(node, r, byteOffset) {
							edits = append(edits, taggedEdit{Edit: e, ruleID: r.ID, line: lineOf(node)})
						}
						return true
					}
					e := fixSelectorExpr(pf, node, r, byteOffset)
					if e != nil {
						edits = append(edits, taggedEdit{Edit: *e, ruleID: r.ID, line: lineOf(node)})
					}

				case *ast.Ident:
					if m.Kind != MatchIdent {
						return true
					}
					if m.NamePattern != nil && (r.Kind == kindRename || r.Kind == kindSignatureChange) {
						return true
					}
					if !m.MatchesName(node.Name) {
						return true
					}
					if m.PkgName != "" {
						if _, hasDot := pf.V3Imports["."]; !hasDot {
							return true
						}
					}
					e := fixIdent(pf, node, r, byteOffset)
					if e != nil {
						edits = append(edits, taggedEdit{Edit: *e, ruleID: r.ID, line: lineOf(node)})
					}
				}
				return true
			})
		}
	}

	edits = ensureOsImport(pf, edits, byteOffset)
	edits = ensureExtensionImports(pf, edits, rules, byteOffset)
	return deduplicateEdits(edits)
}

// fixImportChange rewrites an import path from v3 to v4.
func fixImportChange(_ *ParsedGoFile, node *ast.ImportSpec, r *CompiledRule, byteOffset func(token.Pos) int) *Edit {
	if r.ToVersion() == "" {
		return nil
	}
	oldPath := strings.Trim(node.Path.Value, `"`)
	newPath := strings.Replace(oldPath, r.FromVersion(), r.ToVersion(), 1)
	if newPath == oldPath {
		return nil
	}
	start := byteOffset(node.Path.Pos())
	end := byteOffset(node.Path.End())
	return &Edit{Start: start, End: end, New: `"` + newPath + `"`}
}

// fixCall handles call expressions — either rename the function, delete
// the enclosing statement for removed calls, or perform type-aware rewrites.
func fixCall(pf *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int, stmtOf map[ast.Node]ast.Stmt) []Edit {
	switch r.Kind {
	case kindRemoved:
		return fixDeleteStatement(node, byteOffset, stmtOf)
	case kindRename:
		sel, ok := node.Fun.(*ast.SelectorExpr)
		if !ok || r.ToVersion() == "" {
			return nil
		}
		start := byteOffset(sel.Sel.Pos())
		end := byteOffset(sel.Sel.End())
		return []Edit{{Start: start, End: end, New: r.ToVersion()}}
	case kindSignatureChange:
		if ee := fixWithVerifyFalse(pf, node, r, byteOffset); ee != nil {
			return ee
		}
		if ee := fixGetToField(pf, node, r, byteOffset, stmtOf); ee != nil {
			return ee
		}
		if ee := fixReadFileToParseFS(pf, node, r, byteOffset); ee != nil {
			return ee
		}
		return fixSignatureChange(node, r, byteOffset)
	case kindMovedToExtension:
		return fixMoveToExtension(pf, node, r, byteOffset)
	}
	return nil
}

// fixMoveToExtension rewrites package-qualified references where a symbol
// moved from the core library to an extension module. The rule's v4 field
// holds the new cross-package target in "pkg.Name" form (e.g. the jwa
// extension rules carry v4 "es256k.ES256K"). The rewrite replaces both
// sides of the selector at once and relies on ensureExtensionImport to
// inject the extension module's import path in the collectEdits post-pass.
//
// Applies only to call-site selectors. Bare type/value references (no
// CallExpr) are handled separately by fixMoveToExtensionSelectorExpr so
// the fix runs whether the old symbol was called or referenced.
func fixMoveToExtension(_ *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int) []Edit {
	newPkg, newName := parseExtensionTarget(r.ToVersion())
	if newPkg == "" {
		return nil
	}
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	xIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	xStart := byteOffset(xIdent.Pos())
	xEnd := byteOffset(xIdent.End())
	nameStart := byteOffset(sel.Sel.Pos())
	nameEnd := byteOffset(sel.Sel.End())
	edits := []Edit{{Start: xStart, End: xEnd, New: newPkg}}
	if newName != sel.Sel.Name {
		edits = append(edits, Edit{Start: nameStart, End: nameEnd, New: newName})
	}
	return edits
}

// fixMoveToExtensionSelectorExpr handles the bare selector case — a
// reference that is not the Fun of a CallExpr, e.g. `var _ = jwa.ES256K`
// (value) or `var _ jwa.SomeType` (type). Called from the SelectorExpr
// branch of collectEdits.
func fixMoveToExtensionSelectorExpr(node *ast.SelectorExpr, r *CompiledRule, byteOffset func(token.Pos) int) []Edit {
	if r.Kind != kindMovedToExtension {
		return nil
	}
	newPkg, newName := parseExtensionTarget(r.ToVersion())
	if newPkg == "" {
		return nil
	}
	xIdent, ok := node.X.(*ast.Ident)
	if !ok {
		return nil
	}
	xStart := byteOffset(xIdent.Pos())
	xEnd := byteOffset(xIdent.End())
	nameStart := byteOffset(node.Sel.Pos())
	nameEnd := byteOffset(node.Sel.End())
	edits := []Edit{{Start: xStart, End: xEnd, New: newPkg}}
	if newName != node.Sel.Name {
		edits = append(edits, Edit{Start: nameStart, End: nameEnd, New: newName})
	}
	return edits
}

// parseExtensionTarget splits a v4 field of the form "pkg.Name" into
// its two parts, returning "", "" if the input is not in that shape or
// either half is not a valid Go identifier. Guards against bogus yaml
// values like "Token.Claims() iter.Seq2[string, any]".
func parseExtensionTarget(v4 string) (pkg, name string) {
	dot := strings.Index(v4, ".")
	if dot <= 0 || dot == len(v4)-1 {
		return "", ""
	}
	pkg = v4[:dot]
	name = v4[dot+1:]
	if !isGoIdent(pkg) || !isGoIdent(name) {
		return "", ""
	}
	return pkg, name
}

// fixReadFileToParseFS rewrites jwt.ReadFile("dir/file") →
// jwt.ParseFS(os.DirFS("dir"), "file"), preserving any trailing options.
// Non-literal path arguments return []Edit{} (match claimed but skipped)
// so the caller does not fall through to a name-only rename that would
// leave the call uncallable in v4.
//
// An "os" import is injected in collectEdits's post-pass if any of these
// rewrites were emitted and the file doesn't already import "os".
func fixReadFileToParseFS(pf *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int) []Edit {
	if r.ID != "readfile-to-parsefs" && r.ID != "readfile-to-parsefs-v2" {
		return nil
	}
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	if len(node.Args) == 0 {
		return []Edit{}
	}
	lit, ok := node.Args[0].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return []Edit{}
	}
	raw, err := strconv.Unquote(lit.Value)
	if err != nil {
		return []Edit{}
	}

	dir, file := path.Split(raw)
	switch dir {
	case "":
		dir = "."
	case "/":
		// leave as "/"
	default:
		dir = strings.TrimSuffix(dir, "/")
	}

	newHead := fmt.Sprintf("ParseFS(os.DirFS(%q), %q", dir, file)
	start := byteOffset(sel.Sel.Pos())
	end := byteOffset(node.Args[0].End())
	return []Edit{{Start: start, End: end, New: newHead}}
}

// ensureOsImport appends a single import edit inserting "os" into the
// file's first import group, if any readfile-to-parsefs rewrite was
// emitted and "os" is not already imported. No-op otherwise.
func ensureOsImport(pf *ParsedGoFile, edits []taggedEdit, byteOffset func(token.Pos) int) []taggedEdit {
	var hasReadFileFix bool
	var triggerRuleID string
	for _, e := range edits {
		if e.ruleID == "readfile-to-parsefs" || e.ruleID == "readfile-to-parsefs-v2" {
			hasReadFileFix = true
			triggerRuleID = e.ruleID
			break
		}
	}
	if !hasReadFileFix {
		return edits
	}
	return appendImportEdit(pf, edits, byteOffset, "", "os", triggerRuleID)
}

// ensureExtensionImports injects one `pkg "path"` import for each distinct
// moved_to_extension rule whose edits were emitted and whose target
// package is not already imported. Called from the collectEdits post-pass.
func ensureExtensionImports(pf *ParsedGoFile, edits []taggedEdit, rules []CompiledRule, byteOffset func(token.Pos) int) []taggedEdit {
	if len(edits) == 0 {
		return edits
	}
	ruleByID := make(map[string]*CompiledRule, len(rules))
	for i := range rules {
		ruleByID[rules[i].ID] = &rules[i]
	}
	seen := make(map[string]struct{})
	for _, e := range edits {
		r, ok := ruleByID[e.ruleID]
		if !ok || r.Kind != kindMovedToExtension || r.ExtensionModule == "" {
			continue
		}
		newPkg, _ := parseExtensionTarget(r.ToVersion())
		if newPkg == "" {
			continue
		}
		key := r.ExtensionModule + "\x00" + newPkg
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		if _, already := importedAs(pf, r.ExtensionModule); already {
			continue
		}
		edits = appendImportEdit(pf, edits, byteOffset, newPkg, r.ExtensionModule, e.ruleID)
	}
	return edits
}

// appendImportEdit injects one import spec `alias "path"` (alias may be
// empty) into the file's first import group. No-op if the file has no
// import block at all. Multiple calls with the same target produce
// multiple edits; deduplicateEdits collapses identical ones.
func appendImportEdit(pf *ParsedGoFile, edits []taggedEdit, byteOffset func(token.Pos) int, alias, importPath, ruleID string) []taggedEdit {
	if len(pf.ASTFile.Imports) == 0 {
		return edits
	}
	first := pf.ASTFile.Imports[0]
	pos := byteOffset(first.Pos())
	lineStart := pos
	for lineStart > 0 && pf.Src[lineStart-1] != '\n' {
		lineStart--
	}
	var spec string
	if alias != "" && alias != path.Base(importPath) {
		spec = fmt.Sprintf("\t%s %q\n", alias, importPath)
	} else {
		spec = fmt.Sprintf("\t%q\n", importPath)
	}
	// line=0: import-injection edits are not tied to a single Check finding.
	return append(edits, taggedEdit{
		Edit:   Edit{Start: lineStart, End: lineStart, New: spec},
		ruleID: ruleID,
	})
}

// importedAs reports whether importPath is present in the file's imports
// and, if so, the local name used to refer to it.
func importedAs(pf *ParsedGoFile, importPath string) (string, bool) {
	for _, imp := range pf.ASTFile.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if p != importPath {
			continue
		}
		if imp.Name != nil {
			return imp.Name.Name, true
		}
		return goPkgName(importPath), true
	}
	return "", false
}

// canFixWithTypes returns true if a non-mechanical rule can be fixed when
// type information is available.
func canFixWithTypes(r *CompiledRule, pf *ParsedGoFile) bool {
	return r.ID == ruleGetToField && pf.TypesInfo != nil
}

// fixSignatureChange handles function renames where v3 != v4.
// fixGetToField rewrites obj.Get(name, &dst) → dst, err = pkg.Get[T](obj, name)
// when type info is available to infer T and the receiver package.
// Returns nil if this rule/call doesn't match the pattern.
func fixGetToField(pf *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int, stmtOf map[ast.Node]ast.Stmt) []Edit {
	if r.ID != ruleGetToField || pf.TypesInfo == nil {
		return nil
	}
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok || len(node.Args) != 2 {
		return nil
	}

	// Second arg must be &dst.
	unary, ok := node.Args[1].(*ast.UnaryExpr)
	if !ok || unary.Op.String() != "&" {
		return nil
	}

	// Infer T from the type of dst.
	tv, ok := pf.TypesInfo.Types[unary.X]
	if !ok {
		return nil
	}
	typeName := types.TypeString(tv.Type, func(pkg *types.Package) string {
		// Use the local import name for the type's package.
		for localName, importPath := range pf.V3Imports {
			if pkg.Path() == importPath {
				return localName
			}
		}
		return pkg.Name()
	})

	// Determine the receiver's package local name for pkg.Get[T].
	recvPkgLocal := ""
	if recvTV, ok := pf.TypesInfo.Types[sel.X]; ok {
		recvType := recvTV.Type
		if ptr, ok := recvType.(*types.Pointer); ok {
			recvType = ptr.Elem()
		}
		if named, ok := recvType.(*types.Named); ok {
			if obj := named.Obj(); obj != nil && obj.Pkg() != nil {
				pkgPath := obj.Pkg().Path()
				for localName, importPath := range pf.V3Imports {
					if pkgPath == importPath {
						recvPkgLocal = localName
						break
					}
				}
			}
		}
	}
	if recvPkgLocal == "" {
		return nil
	}

	// Build the source text for the first argument (name).
	nameText := extractSourceText(pf, node.Args[0])
	// Build the source text for the receiver (obj).
	objText := extractSourceText(pf, sel.X)
	// Build the source text for dst.
	dstText := extractSourceText(pf, unary.X)

	if nameText == "" || objText == "" || dstText == "" {
		return nil
	}

	// New call: pkg.Get[T](obj, name)
	newCall := fmt.Sprintf("%s.Get[%s](%s, %s)", recvPkgLocal, typeName, objText, nameText)

	// Determine the enclosing statement to know what assignment to generate.
	stmt, hasStmt := stmtOf[node]
	if !hasStmt {
		return nil
	}

	var newStmt string
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		// Bare call: obj.Get(name, &dst) → dst, _ = pkg.Get[T](obj, name)
		newStmt = fmt.Sprintf("%s, _ = %s", dstText, newCall)
	case *ast.AssignStmt:
		if len(s.Lhs) == 1 {
			lhs := extractSourceText(pf, s.Lhs[0])
			tok := s.Tok.String() // "=" or ":="
			if lhs == "_" {
				// _ = obj.Get(...) → dst, _ = pkg.Get[T](...)
				newStmt = fmt.Sprintf("%s, _ %s %s", dstText, tok, newCall)
			} else {
				// err = obj.Get(...) → dst, err = pkg.Get[T](...)
				newStmt = fmt.Sprintf("%s, %s %s %s", dstText, lhs, tok, newCall)
			}
		}
	}
	if newStmt == "" {
		return nil
	}

	start := byteOffset(stmt.Pos())
	end := byteOffset(stmt.End())
	return []Edit{{Start: start, End: end, New: newStmt}}
}

// extractSourceText extracts the source text for an AST expression.
func extractSourceText(pf *ParsedGoFile, expr ast.Expr) string {
	file := pf.FileSet.File(pf.ASTFile.Pos())
	if file == nil {
		return ""
	}
	base := file.Base()
	start := int(expr.Pos()) - base
	end := int(expr.End()) - base
	if start < 0 || end > len(pf.Src) || start >= end {
		return ""
	}
	return string(pf.Src[start:end])
}

// fixWithVerifyFalse rewrites jwt.Parse*/ParseString calls that contain
// jwt.WithVerify(false) → jwt.ParseInsecure, preserving all other options.
// For ParseString, the first arg is wrapped in []byte().
func fixWithVerifyFalse(pf *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int) []Edit {
	if r.ID != "jwt-withverify-false-to-parseinsecure-v2" {
		return nil
	}
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	funcName := sel.Sel.Name
	if funcName != "Parse" && funcName != "ParseString" {
		return nil
	}

	// Find the WithVerify(false) argument.
	withVerifyIdx := -1
	for i, arg := range node.Args {
		if isWithVerifyFalse(arg) {
			withVerifyIdx = i
			break
		}
	}
	if withVerifyIdx < 0 {
		// Matched the rule's function but no WithVerify(false) — skip, don't
		// fall through to generic signature rename.
		return []Edit{}
	}

	var edits []Edit

	// 1. Rename the function to ParseInsecure.
	edits = append(edits, Edit{
		Start: byteOffset(sel.Sel.Pos()),
		End:   byteOffset(sel.Sel.End()),
		New:   "ParseInsecure",
	})

	// 2. For ParseString, wrap the first arg in []byte().
	if funcName == "ParseString" && len(node.Args) > 0 {
		firstArg := node.Args[0]
		argText := extractSourceText(pf, firstArg)
		edits = append(edits, Edit{
			Start: byteOffset(firstArg.Pos()),
			End:   byteOffset(firstArg.End()),
			New:   "[]byte(" + argText + ")",
		})
	}

	// 3. Remove the WithVerify(false) argument.
	wvArg := node.Args[withVerifyIdx]
	removeStart := byteOffset(wvArg.Pos())
	removeEnd := byteOffset(wvArg.End())

	// Also remove the surrounding comma+whitespace.
	if withVerifyIdx > 0 {
		// Remove leading ", "
		prevEnd := byteOffset(node.Args[withVerifyIdx-1].End())
		removeStart = prevEnd
	} else if withVerifyIdx < len(node.Args)-1 {
		// Remove trailing ", "
		nextStart := byteOffset(node.Args[withVerifyIdx+1].Pos())
		removeEnd = nextStart
	}

	edits = append(edits, Edit{
		Start: removeStart,
		End:   removeEnd,
		New:   "",
	})

	return edits
}

// isWithVerifyFalse checks if an expression is jwt.WithVerify(false).
func isWithVerifyFalse(expr ast.Expr) bool {
	call, ok := expr.(*ast.CallExpr)
	if !ok || len(call.Args) != 1 {
		return false
	}
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "WithVerify" {
		return false
	}
	// Check arg is the identifier "false".
	ident, ok := call.Args[0].(*ast.Ident)
	return ok && ident.Name == "false"
}

// fixSignatureChange handles function renames where v3 != v4.
func fixSignatureChange(node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int) []Edit {
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	// Only rename if the function name actually changed.
	if r.ToVersion() == "" || r.FromVersion() == r.ToVersion() {
		return nil
	}
	start := byteOffset(sel.Sel.Pos())
	end := byteOffset(sel.Sel.End())
	return []Edit{{Start: start, End: end, New: r.ToVersion()}}
}

// fixDeleteStatement deletes the entire statement containing a call expression.
func fixDeleteStatement(node ast.Node, byteOffset func(token.Pos) int, stmtOf map[ast.Node]ast.Stmt) []Edit {
	stmt, ok := stmtOf[node]
	if !ok {
		return nil
	}
	start := byteOffset(stmt.Pos())
	end := byteOffset(stmt.End())
	return []Edit{{Start: start, End: end, New: ""}}
}

// isGoIdent reports whether s is a valid Go identifier — starts with a
// letter or underscore, followed by letters, digits, or underscores. Used
// to reject rename targets that are actually prose (e.g. a rule whose v4
// field is "func(T) (Key, error)" — a description, not a symbol).
func isGoIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if r == '_' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			continue
		}
		if i > 0 && r >= '0' && r <= '9' {
			continue
		}
		return false
	}
	return true
}

// fixSelectorExpr renames a selector expression (e.g. jws.Signer2 → jws.Signer).
func fixSelectorExpr(_ *ParsedGoFile, node *ast.SelectorExpr, r *CompiledRule, byteOffset func(token.Pos) int) *Edit {
	newName := r.ToVersion()
	if newName == "" {
		newName = r.Replacement
	}
	if !isGoIdent(newName) {
		return nil
	}
	start := byteOffset(node.Sel.Pos())
	end := byteOffset(node.Sel.End())
	return &Edit{Start: start, End: end, New: newName}
}

// fixIdent renames a bare identifier.
func fixIdent(_ *ParsedGoFile, node *ast.Ident, r *CompiledRule, byteOffset func(token.Pos) int) *Edit {
	newName := r.ToVersion()
	if newName == "" {
		newName = r.Replacement
	}
	if !isGoIdent(newName) {
		return nil
	}
	start := byteOffset(node.Pos())
	end := byteOffset(node.End())
	return &Edit{Start: start, End: end, New: newName}
}

// buildStmtMap maps child nodes to their enclosing statement.
// This is needed to delete entire statements when removing calls.
func buildStmtMap(f *ast.File) map[ast.Node]ast.Stmt {
	m := make(map[ast.Node]ast.Stmt)
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		switch stmt := n.(type) {
		case *ast.ExprStmt:
			m[stmt.X] = stmt
		case *ast.AssignStmt:
			for _, rhs := range stmt.Rhs {
				m[rhs] = stmt
			}
		}
		return true
	})
	return m
}

// applyEdits applies edits to source bytes in reverse position order.
func applyEdits(src []byte, edits []taggedEdit) []byte {
	// Sort by start position descending so edits don't shift offsets.
	sort.Slice(edits, func(i, j int) bool {
		return edits[i].Start > edits[j].Start
	})

	result := make([]byte, len(src))
	copy(result, src)

	for _, e := range edits {
		if e.Start < 0 || e.End > len(result) || e.Start > e.End {
			continue
		}

		var buf []byte
		buf = append(buf, result[:e.Start]...)
		buf = append(buf, []byte(e.New)...)
		buf = append(buf, result[e.End:]...)
		result = buf
	}

	return result
}

// deduplicateEdits removes edits that overlap the same byte range.
func deduplicateEdits(edits []taggedEdit) []taggedEdit {
	if len(edits) == 0 {
		return edits
	}

	sort.Slice(edits, func(i, j int) bool {
		if edits[i].Start != edits[j].Start {
			return edits[i].Start < edits[j].Start
		}
		return edits[i].End < edits[j].End
	})

	result := []taggedEdit{edits[0]}
	for _, e := range edits[1:] {
		last := result[len(result)-1]
		if e.Start < last.End {
			continue // overlapping — skip
		}
		result = append(result, e)
	}
	return result
}
