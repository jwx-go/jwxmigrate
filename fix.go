package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
)

// readfileToParseFSRuleID is the rule identifier for the
// jwt.ReadFile/jwk.ReadFile → jwt.ParseFS/jwk.ParseFS rewrite in the
// v3→v4 ruleset. Kept as a constant to satisfy goconst and to provide
// a single canonical spelling.
const readfileToParseFSRuleID = "readfile-to-parsefs"

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

// FixOptions controls optional behavior of FixFile / FixFileWithOptions.
type FixOptions struct {
	// Backup, when true, causes writeFormatted to save the original file
	// alongside the rewritten one as `<path>.bak` before overwriting.
	Backup bool

	// overlay maps absolute file path → original (pre-batch) content. When
	// non-nil, parseGoFileTyped passes it to packages.Config.Overlay so
	// the type checker sees a consistent snapshot even after sibling
	// files in the same package have already been rewritten on disk.
	// Set by fixFiles for batch runs; nil for single-file callers (their
	// type loading is consistent by construction).
	overlay map[string][]byte
}

// FixFile applies mechanical fixes to a single Go file in-place.
// It also collects non-mechanical findings from the same parse so the
// caller can report remaining issues without re-scanning.
func FixFile(filePath string, rules []CompiledRule) (*FixResult, error) {
	return FixFileWithOptions(filePath, rules, FixOptions{})
}

// FixFileWithOptions is FixFile with caller-supplied FixOptions.
func FixFileWithOptions(filePath string, rules []CompiledRule, opts FixOptions) (*FixResult, error) {
	// Try type-checked loading first for type-aware fixes.
	pf := parseGoFileTyped(filePath, opts.overlay)
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
	if err := writeFormatted(filePath, result, ids, opts.Backup); err != nil {
		return nil, err
	}

	return &FixResult{File: filePath, Applied: ids, Remaining: unfixed}, nil
}

// writeFormatted gofmts result and writes it to filePath. If format.Source
// fails, the file is left untouched and an error naming the contributing
// rule IDs is returned — silently writing unformatted (likely broken) Go
// would hand users a code base that their next `go build` chokes on, with
// no signal about which rule to blame.
//
// Writes are atomic: we stage into a sibling temp file (same directory,
// so os.Rename stays on one filesystem), fsync, then rename over the
// target. A SIGINT/OOM/power event mid-write leaves either the old file
// intact or the fully-written new file, never a half-flushed source.
// When backup is true, the original file is copied to `<path>.bak`
// before the rename.
func writeFormatted(filePath string, result []byte, ruleIDs []string, backup bool) error {
	formatted, err := format.Source(result)
	if err != nil {
		return fmt.Errorf("refusing to write %s: post-edit source failed to format (rules: %s): %w", filePath, strings.Join(ruleIDs, ","), err)
	}

	if backup {
		orig, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("reading %s for backup: %w", filePath, err)
		}
		if err := os.WriteFile(filePath+".bak", orig, 0o644); err != nil {
			return fmt.Errorf("writing backup %s.bak: %w", filePath, err)
		}
	}

	dir := filepath.Dir(filePath)
	tmp, err := os.CreateTemp(dir, filepath.Base(filePath)+".jwxmigrate.tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp for %s: %w", filePath, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(formatted); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp for %s: %w", filePath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fsync temp for %s: %w", filePath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp for %s: %w", filePath, err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		cleanup()
		return fmt.Errorf("renaming temp to %s: %w", filePath, err)
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

	// Pre-collect AssignStmts that live in if-init position, mapped back
	// to the enclosing IfStmt. Fixers that produce a new LHS binding
	// (jwk-export-generic) need both pieces of context: the if-init
	// AssignStmt to detect the case at all, and the IfStmt itself to
	// emit an else block that writes the temp back to the user's dst
	// — adding a second LHS to the AssignStmt alone would shadow the
	// outer dst and silently break every read after the if.
	ifInitAssigns := make(map[*ast.AssignStmt]*ast.IfStmt)
	ast.Inspect(pf.ASTFile, func(n ast.Node) bool {
		ifs, ok := n.(*ast.IfStmt)
		if !ok || ifs.Init == nil {
			return true
		}
		if as, ok := ifs.Init.(*ast.AssignStmt); ok {
			ifInitAssigns[as] = ifs
		}
		return true
	})

	var edits []taggedEdit

	// pendingJWXImports tracks v4 jwx import paths that rewrites need
	// injected because the file references the type only transitively
	// (e.g. OPA's sign_test.go uses jwt.Token via a helper's return
	// type without importing jwt directly). Keyed by v4 path so the
	// same package is injected at most once; value is the desired
	// local name. See ensureJWXImports for the post-pass.
	pendingJWXImports := make(map[string]string)

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

					ee := fixCall(pf, node, r, byteOffset, stmtOf, ifInitAssigns, pendingJWXImports)
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
	edits = ensureJWXImports(pf, edits, pendingJWXImports, byteOffset)
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
func fixCall(pf *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int, stmtOf map[ast.Node]ast.Stmt, ifInitAssigns map[*ast.AssignStmt]*ast.IfStmt, pendingJWXImports map[string]string) []Edit {
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
		if ee := fixGetToField(pf, node, r, byteOffset, stmtOf, ifInitAssigns, pendingJWXImports); ee != nil {
			return ee
		}
		if ee := fixReadFileToParseFS(pf, node, r, byteOffset); ee != nil {
			return ee
		}
		if ee := fixJWKImportGeneric(node, r, byteOffset); ee != nil {
			return ee
		}
		if ee := fixJWKFromRawToImport(node, r, byteOffset); ee != nil {
			return ee
		}
		if ee := fixJWKExportGeneric(pf, node, r, byteOffset, stmtOf, ifInitAssigns); ee != nil {
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
func fixReadFileToParseFS(_ *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int) []Edit {
	if r.ID != readfileToParseFSRuleID && r.ID != "readfile-to-parsefs-v2" {
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
		if e.ruleID == readfileToParseFSRuleID || e.ruleID == "readfile-to-parsefs-v2" {
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

// ensureJWXImports injects a `pkg "path"` import for each v4 jwx
// subpackage that a rewrite (currently only fixGetToField) referenced
// via a transitively-reachable type the file didn't import directly.
// Skipped when the path is already imported — importedAs matches on
// the full v4 path so a pre-existing alias still counts.
func ensureJWXImports(pf *ParsedGoFile, edits []taggedEdit, pending map[string]string, byteOffset func(token.Pos) int) []taggedEdit {
	if len(pending) == 0 {
		return edits
	}
	for v4Path, localName := range pending {
		if importedAs(pf, v4Path) {
			continue
		}
		alias := ""
		if localName != path.Base(v4Path) {
			alias = localName
		}
		edits = appendImportEdit(pf, edits, byteOffset, alias, v4Path, ruleGetToField)
	}
	return edits
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
		if importedAs(pf, r.ExtensionModule) {
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

// importedAs reports whether importPath is present in the file's imports.
func importedAs(pf *ParsedGoFile, importPath string) bool {
	for _, imp := range pf.ASTFile.Imports {
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		if p == importPath {
			return true
		}
	}
	return false
}

// canFixWithTypes returns true if a non-mechanical rule has a fixer in this
// file. Some fixers require type information (get-to-field uses it to infer
// the type parameter); others are pure syntactic transforms safe without
// types (the jwk.Import / jwk.FromRaw → typed-Import rewrites just inject
// `[<jwk-local>.Key]`, preserving the original Key return semantics).
func canFixWithTypes(r *CompiledRule, pf *ParsedGoFile) bool {
	switch r.ID {
	case ruleGetToField, ruleJWKExportGeneric:
		return pf.TypesInfo != nil
	case ruleJWKImportGeneric, ruleJWKImportGenericV, ruleJWKFromRawV2:
		return true
	}
	return false
}

// fixGetToField rewrites obj.Get(name, &dst) → dst, err = pkg.Get[T](obj, name)
// when type info is available to infer T and the receiver package.
//
// Returns nil when the call doesn't have the .Get(name, &ptr) shape so that
// other fixers in the kindSignatureChange dispatch get a chance. Once the
// shape is confirmed but a reshape can't be synthesized (unknown dst type,
// unreachable receiver package, unsupported enclosing statement), returns
// []Edit{} to claim the match and block collectEdits' fallthrough to
// fixSignatureChange — without that block, the get-to-field rule would
// naively rename .Get → .Field, producing code that doesn't compile
// because v4's Field signature is `Field(name) (any, bool)`, not
// `Get(name, &dst) error`.
func fixGetToField(pf *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int, stmtOf map[ast.Node]ast.Stmt, ifInitAssigns map[*ast.AssignStmt]*ast.IfStmt, pendingJWXImports map[string]string) []Edit {
	if r.ID != ruleGetToField || pf.TypesInfo == nil {
		return nil
	}
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok || len(node.Args) != 2 {
		return nil
	}

	// Second arg must be &dst. After this point the call is confirmed
	// to be get-to-field-shaped, so bail-outs switch to []Edit{} to
	// block the naive-rename fallthrough.
	unary, ok := node.Args[1].(*ast.UnaryExpr)
	if !ok || unary.Op.String() != "&" {
		return nil
	}

	// Infer T from the type of dst.
	tv, ok := pf.TypesInfo.Types[unary.X]
	if !ok {
		return []Edit{}
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
	// First try V3Imports (the common case: file directly imports the
	// package). If the package is reachable only transitively (e.g. via
	// a helper's return type — OPA's sign_test.go uses jwt.Token this
	// way without importing jwt), fall back to the v3 path from
	// TypesInfo and record a pending import for the post-pass so the
	// rewritten call has something to bind against.
	recvPkgLocal := ""
	var recvPkgPath string
	if recvTV, ok := pf.TypesInfo.Types[sel.X]; ok {
		recvType := recvTV.Type
		if ptr, ok := recvType.(*types.Pointer); ok {
			recvType = ptr.Elem()
		}
		if named, ok := recvType.(*types.Named); ok {
			if obj := named.Obj(); obj != nil && obj.Pkg() != nil {
				recvPkgPath = obj.Pkg().Path()
				for localName, importPath := range pf.V3Imports {
					if recvPkgPath == importPath {
						recvPkgLocal = localName
						break
					}
				}
			}
		}
	}
	if recvPkgLocal == "" {
		if recvPkgPath == "" || !strings.HasPrefix(recvPkgPath, sourceImportPrefix) {
			return []Edit{}
		}
		// Not directly imported — derive local name and queue an
		// import for the v4-rewritten path. The import-v3-to-v4 rule
		// only rewrites existing v3 ImportSpec nodes, so injecting a
		// v4 path keeps the file consistent after the full pass.
		recvPkgLocal = goPkgName(recvPkgPath)
		v4Path := strings.Replace(recvPkgPath, sourceImportPrefix, targetImportPrefix, 1)
		pendingJWXImports[v4Path] = recvPkgLocal
	}

	// Build the source text for the first argument (name).
	nameText := extractSourceText(pf, node.Args[0])
	// Build the source text for the receiver (obj).
	objText := extractSourceText(pf, sel.X)
	// Build the source text for dst.
	dstText := extractSourceText(pf, unary.X)

	if nameText == "" || objText == "" || dstText == "" {
		return []Edit{}
	}

	// New call: pkg.Get[T](obj, name)
	newCall := fmt.Sprintf("%s.Get[%s](%s, %s)", recvPkgLocal, typeName, objText, nameText)

	// Determine the enclosing statement to know what assignment to generate.
	stmt, hasStmt := stmtOf[node]
	if !hasStmt {
		return []Edit{}
	}

	var newStmt string
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		// Bare call: obj.Get(name, &dst) → dst, _ = pkg.Get[T](obj, name)
		newStmt = fmt.Sprintf("%s, _ = %s", dstText, newCall)
	case *ast.AssignStmt:
		if ifs, ok := ifInitAssigns[s]; ok {
			// if-init shape: turn
			//   if err := tok.Get(name, &dst); err != nil { BODY }
			// into
			//   if dstV4, err := jwt.Get[T](tok, name); err != nil { BODY } else { dst = dstV4 }
			// The temp dstV4 dodges the shadowing trap (dst may already
			// be declared outside the if), and the else preserves v3's
			// "leave dst alone on error" semantics for callers whose
			// BODY doesn't unconditionally exit.
			return fixGetToFieldIfInit(pf, ifs, s, byteOffset, newCall, dstText)
		}
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
		return []Edit{}
	}

	start := byteOffset(stmt.Pos())
	end := byteOffset(stmt.End())
	return []Edit{{Start: start, End: end, New: newStmt}}
}

// fixGetToFieldIfInit emits the if-init rewrite described on fixGetToField.
// Mirrors fixJWKExportIfInit: adds a temp LHS, swaps the call, and appends
// an else block that copies the temp back to dst. Skips when the if has
// an existing else clause (merging into else-if chains is more reshape
// than this fixer owns; the call gets reported as remaining).
func fixGetToFieldIfInit(pf *ParsedGoFile, ifs *ast.IfStmt, init *ast.AssignStmt, byteOffset func(token.Pos) int, newCall, dstText string) []Edit {
	if ifs.Else != nil {
		return []Edit{}
	}
	if len(init.Lhs) != 1 {
		return []Edit{}
	}
	errText := extractSourceText(pf, init.Lhs[0])
	if errText == "" {
		return []Edit{}
	}
	tmpName := getFieldTempName(pf, dstText, ifs.Pos())

	lhsStart := byteOffset(init.Lhs[0].Pos())
	lhsEnd := byteOffset(init.Lhs[0].End())
	rhsStart := byteOffset(init.Rhs[0].Pos())
	rhsEnd := byteOffset(init.Rhs[0].End())
	bodyEnd := byteOffset(ifs.Body.End())

	elseBlock := fmt.Sprintf(" else {\n\t%s = %s\n}", dstText, tmpName)

	return []Edit{
		{Start: lhsStart, End: lhsEnd, New: tmpName + ", " + errText},
		{Start: rhsStart, End: rhsEnd, New: newCall},
		{Start: bodyEnd, End: bodyEnd, New: elseBlock},
	}
}

// getFieldTempName mirrors exportTempName but uses a V4 suffix (shorter
// than Export's V4Exported — this rewrite reads as "the value from Get",
// so the suffix just disambiguates the temp from the outer dst).
func getFieldTempName(pf *ParsedGoFile, dst string, pos token.Pos) string {
	base := dst + "V4"
	if !nameInScope(pf, base, pos) {
		return base
	}
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		if !nameInScope(pf, candidate, pos) {
			return candidate
		}
	}
	return base
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

// fixJWKImportGeneric rewrites `<jwk>.Import(raw)` →
// `<jwk>.Import[<jwk>.Key](raw)` for both the v3→v4 and v2→v4 rules. The
// inserted `[<jwk>.Key]` preserves the v3/v2 return type (the bare Key
// interface), so downstream code that took a Key keeps working without
// edits. Callers that previously did `key.(jwk.RSAPrivateKey)` after the
// call still need to migrate to `Import[jwk.RSAPrivateKey](raw)` by hand
// — that's why the rules stay mechanical:false in YAML and remain on the
// remaining-issues list even after this fix runs.
//
// The local name for jwk is taken from the call's selector receiver
// (`sel.X`), so aliased imports like `myjwk "github.com/.../jwk"` produce
// `myjwk.Import[myjwk.Key](...)`. The fixer skips already-typed calls —
// when the user wrote `jwk.Import[T](raw)`, the parser exposes `node.Fun`
// as an *ast.IndexExpr (or IndexListExpr), so the SelectorExpr cast
// fails and we no-op.
func fixJWKImportGeneric(node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int) []Edit {
	if r.ID != ruleJWKImportGeneric && r.ID != ruleJWKImportGenericV {
		return nil
	}
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	insertAt := byteOffset(sel.End())
	return []Edit{{Start: insertAt, End: insertAt, New: "[" + pkg.Name + ".Key]"}}
}

// fixJWKExportGeneric rewrites `<jwk>.Export(k, &dst)` →
// `dst, <err> = <jwk>.Export[T](k)` when type info lets us pick a T that
// is round-trip-safe. Mirrors fixGetToField in shape (assignment-form
// rewrite using type info to pick T) but applies a stricter T policy
// because v4 Export's exporter returns whatever the dispatcher emits and
// then asserts it to T — picking the wrong T turns a working v3 call
// into a runtime "exported X, requested Y" error.
//
// Safe T cases (we rewrite):
//
//   - dst's pointed-to type is an interface (`any`, `jwk.Key`, etc.):
//     T = pointed-to type. Dispatcher emits the concrete `*rsa.PrivateKey`
//     style value, and an interface assertion always succeeds for any
//     value, so the caller still gets back the same dynamic type they
//     used to read out via `dst.(...)`.
//
//   - dst's pointed-to type is itself a pointer (`*rsa.PrivateKey`,
//     `*ecdsa.PrivateKey`, …): T = pointed-to type. The dispatcher's
//     return type matches T directly, so `result, ok := v.(T)` succeeds
//     and dst preserves its original type.
//
// Skipped (left on the remaining-issues list):
//
//   - dst's pointed-to type is a non-interface value type (e.g.
//     `var raw rsa.PrivateKey; jwk.Export(k, &raw)`). The dispatcher
//     returns `*rsa.PrivateKey`, and Export[rsa.PrivateKey] would fail
//     `v.(T)` at runtime. The only correct mechanical equivalent
//     introduces a temp pointer + an explicit deref, which crosses
//     statement boundaries — an invasive multi-line rewrite that's
//     better left to the developer.
//
//   - if-init form (`if err := jwk.Export(k, &dst); err != nil { … }`)
//     when the if already has an else branch (merging into existing else
//     is more reshape than this fixer is willing to do). The bare
//     no-else form IS handled: a temp var is introduced inside the init
//     and an else block writes it back to dst, preserving v3's "leave
//     dst untouched on error" semantics for callers whose body doesn't
//     unconditionally exit.
func fixJWKExportGeneric(pf *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int, stmtOf map[ast.Node]ast.Stmt, ifInitAssigns map[*ast.AssignStmt]*ast.IfStmt) []Edit {
	if r.ID != ruleJWKExportGeneric || pf.TypesInfo == nil {
		return nil
	}
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok || sel.Sel.Name != "Export" || len(node.Args) != 2 {
		return nil
	}
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	unary, ok := node.Args[1].(*ast.UnaryExpr)
	if !ok || unary.Op != token.AND {
		return nil
	}
	tv, ok := pf.TypesInfo.Types[unary.X]
	if !ok {
		return nil
	}
	dstType := tv.Type
	switch dstType.Underlying().(type) {
	case *types.Interface, *types.Pointer:
		// Round-trip-safe. Fall through.
	default:
		return nil
	}

	tStr := types.TypeString(dstType, func(pkg *types.Package) string {
		for localName, importPath := range pf.V3Imports {
			if pkg.Path() == importPath {
				return localName
			}
		}
		return pkg.Name()
	})

	keyText := extractSourceText(pf, node.Args[0])
	dstText := extractSourceText(pf, unary.X)
	if keyText == "" || dstText == "" {
		return nil
	}
	newCall := fmt.Sprintf("%s.Export[%s](%s)", pkgIdent.Name, tStr, keyText)

	stmt, hasStmt := stmtOf[node]
	if !hasStmt {
		return nil
	}
	var newStmt string
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		newStmt = fmt.Sprintf("%s, _ = %s", dstText, newCall)
	case *ast.AssignStmt:
		if ifs, ok := ifInitAssigns[s]; ok {
			// if-init shape: turn
			//   if err := jwk.Export(k, &dst); err != nil { BODY }
			// into
			//   if dstV4, err := jwk.Export[T](k); err != nil { BODY } else { dst = dstV4 }
			// The temp dstV4 dodges the shadowing trap (the inner dst would
			// otherwise hide every outer read after the if), and the else
			// keeps v3's "leave dst alone on error" semantics for callers
			// whose BODY doesn't unconditionally exit.
			return fixJWKExportIfInit(pf, ifs, s, byteOffset, newCall, dstText)
		}
		if len(s.Lhs) != 1 {
			return nil
		}
		lhs := extractSourceText(pf, s.Lhs[0])
		tok := s.Tok.String()
		if lhs == "_" {
			newStmt = fmt.Sprintf("%s, _ %s %s", dstText, tok, newCall)
		} else {
			newStmt = fmt.Sprintf("%s, %s %s %s", dstText, lhs, tok, newCall)
		}
	}
	if newStmt == "" {
		return nil
	}
	start := byteOffset(stmt.Pos())
	end := byteOffset(stmt.End())
	return []Edit{{Start: start, End: end, New: newStmt}}
}

// fixJWKExportIfInit emits the if-init rewrite documented on
// fixJWKExportGeneric. Caller has already verified shape (jwk.Export
// with two args, &dst arg, dst type round-trip-safe) and computed
// newCall (`<jwk>.Export[T](k)`) and dstText (the source text of the
// dst variable). This function owns the surrounding-statement reshape:
// it edits the AssignStmt's LHS to add a temp, swaps in the new call,
// and inserts an else block that writes the temp back to dst.
//
// Skips when the if already has an else clause — merging into existing
// else (especially else-if chains) is more invasive than this fixer is
// willing to do; the call gets reported on the remaining-issues list.
func fixJWKExportIfInit(pf *ParsedGoFile, ifs *ast.IfStmt, init *ast.AssignStmt, byteOffset func(token.Pos) int, newCall, dstText string) []Edit {
	if ifs.Else != nil {
		return nil
	}
	if len(init.Lhs) != 1 {
		return nil
	}
	errText := extractSourceText(pf, init.Lhs[0])
	if errText == "" {
		return nil
	}
	tmpName := exportTempName(pf, dstText, ifs.Pos())

	// Three edits:
	//   1. Replace the AssignStmt's single LHS (`err`) with `<tmp>, err`.
	//   2. Replace the AssignStmt's RHS (the v3 call) with newCall.
	//   3. Append ` else { <dst> = <tmp> }` after the if's body.
	lhsStart := byteOffset(init.Lhs[0].Pos())
	lhsEnd := byteOffset(init.Lhs[0].End())
	rhsStart := byteOffset(init.Rhs[0].Pos())
	rhsEnd := byteOffset(init.Rhs[0].End())
	bodyEnd := byteOffset(ifs.Body.End())

	elseBlock := fmt.Sprintf(" else {\n\t%s = %s\n}", dstText, tmpName)

	return []Edit{
		{Start: lhsStart, End: lhsEnd, New: tmpName + ", " + errText},
		{Start: rhsStart, End: rhsEnd, New: newCall},
		{Start: bodyEnd, End: bodyEnd, New: elseBlock},
	}
}

// exportTempName returns a function-scope-unique identifier for the
// if-init temp. Falls back to a `<dst>V4Exported` form, then suffixes
// with a counter if that name is already in scope at pos. Picking
// the name from dst keeps the rewrite reading naturally — `keyV4Exported`
// in the generated else line tells the reader exactly which dst the
// temp is being assigned back to.
func exportTempName(pf *ParsedGoFile, dst string, pos token.Pos) string {
	base := dst + "V4Exported"
	if !nameInScope(pf, base, pos) {
		return base
	}
	for i := 2; i < 100; i++ {
		candidate := fmt.Sprintf("%s%d", base, i)
		if !nameInScope(pf, candidate, pos) {
			return candidate
		}
	}
	return base // last resort — caller will see a compile error
}

// nameInScope reports whether name is reachable from pos via the
// innermost lexical scope chain. Returns false when the file has no
// type info (no scopes were built); callers treat that as "name is
// safe" since we're already in untyped fallback territory.
func nameInScope(pf *ParsedGoFile, name string, pos token.Pos) bool {
	if pf.TypesInfo == nil {
		return false
	}
	pkgScope := pf.TypesInfo.Scopes[pf.ASTFile]
	if pkgScope == nil {
		return false
	}
	scope := pkgScope.Innermost(pos)
	if scope == nil {
		return false
	}
	_, obj := scope.LookupParent(name, pos)
	return obj != nil
}

// fixJWKFromRawToImport rewrites the v2-only `<jwk>.FromRaw(raw)` →
// `<jwk>.Import[<jwk>.Key](raw)`. Same shape as fixJWKImportGeneric but
// also renames FromRaw → Import in one edit so the result is gofmt-stable
// and we don't depend on edit ordering between two adjacent regions.
func fixJWKFromRawToImport(node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int) []Edit {
	if r.ID != ruleJWKFromRawV2 {
		return nil
	}
	sel, ok := node.Fun.(*ast.SelectorExpr)
	if !ok {
		return nil
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return nil
	}
	start := byteOffset(sel.Sel.Pos())
	end := byteOffset(sel.Sel.End())
	return []Edit{{Start: start, End: end, New: "Import[" + pkg.Name + ".Key]"}}
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
