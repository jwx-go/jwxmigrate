package main

import (
	"fmt"
	"go/ast"
	"go/format"
	"go/token"
	"go/types"
	"os"
	"sort"
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
		return nil, nil // no v3 imports
	}

	edits := collectEdits(pf, rules)

	// Track which rules were fixed so we can exclude them from remaining.
	fixedRules := make(map[string]struct{})
	for _, e := range edits {
		fixedRules[e.ruleID] = struct{}{}
	}

	// Collect findings and filter out those that are mechanical or were fixed.
	allFindings := scanGoFileAST(pf, rules, CheckOptions{})
	var unfixed []Finding
	for _, f := range allFindings {
		if f.Mechanical {
			continue
		}
		if _, fixed := fixedRules[f.RuleID]; fixed {
			continue
		}
		unfixed = append(unfixed, f)
	}
	if len(edits) == 0 {
		if len(unfixed) == 0 {
			return nil, nil
		}
		return &FixResult{File: filePath, Remaining: unfixed}, nil
	}

	result := applyEdits(pf.Src, edits)
	formatted, err := format.Source(result)
	if err != nil {
		formatted = result
	}

	if err := os.WriteFile(filePath, formatted, 0o644); err != nil {
		return nil, fmt.Errorf("writing %s: %w", filePath, err)
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

	return &FixResult{File: filePath, Applied: ids, Remaining: unfixed}, nil
}

// taggedEdit is an Edit with its originating rule ID for reporting.
type taggedEdit struct {
	Edit
	ruleID string
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
						edits = append(edits, taggedEdit{Edit: *e, ruleID: r.ID})
					}

				case *ast.CallExpr:
					sel, ok := node.Fun.(*ast.SelectorExpr)
					if !ok {
						return true
					}

					switch m.Kind {
					case MatchCallExpr:
						ident, ok := sel.X.(*ast.Ident)
						if !ok || sel.Sel.Name != m.Name || !matchesPkg(ident.Name, m.PkgName) {
							return true
						}
					case MatchMethodCall:
						if sel.Sel.Name != m.Name {
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
						edits = append(edits, taggedEdit{Edit: e, ruleID: r.ID})
					}

				case *ast.SelectorExpr:
					if _, isCallFun := callFuns[node]; isCallFun {
						return true
					}
					if m.Kind != MatchSelectorExpr {
						return true
					}
					ident, ok := node.X.(*ast.Ident)
					if !ok || node.Sel.Name != m.Name || !matchesPkg(ident.Name, m.PkgName) {
						return true
					}
					e := fixSelectorExpr(pf, node, r, byteOffset)
					if e != nil {
						edits = append(edits, taggedEdit{Edit: *e, ruleID: r.ID})
					}

				case *ast.Ident:
					if m.Kind != MatchIdent {
						return true
					}
					if node.Name != m.Name {
						return true
					}
					if m.PkgName != "" {
						if _, hasDot := pf.V3Imports["."]; !hasDot {
							return true
						}
					}
					e := fixIdent(pf, node, r, byteOffset)
					if e != nil {
						edits = append(edits, taggedEdit{Edit: *e, ruleID: r.ID})
					}
				}
				return true
			})
		}
	}

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
	case "removed":
		return fixDeleteStatement(node, byteOffset, stmtOf)
	case "rename":
		sel, ok := node.Fun.(*ast.SelectorExpr)
		if !ok || r.ToVersion() == "" {
			return nil
		}
		start := byteOffset(sel.Sel.Pos())
		end := byteOffset(sel.Sel.End())
		return []Edit{{Start: start, End: end, New: r.ToVersion()}}
	case "signature_change":
		if ee := fixWithVerifyFalse(pf, node, r, byteOffset); ee != nil {
			return ee
		}
		if ee := fixGetToField(pf, node, r, byteOffset, stmtOf); ee != nil {
			return ee
		}
		return fixSignatureChange(node, r, byteOffset)
	}
	return nil
}

// canFixWithTypes returns true if a non-mechanical rule can be fixed when
// type information is available.
func canFixWithTypes(r *CompiledRule, pf *ParsedGoFile) bool {
	return r.ID == "get-to-field" && pf.TypesInfo != nil
}

// fixSignatureChange handles function renames where v3 != v4.
// fixGetToField rewrites obj.Get(name, &dst) → dst, err = pkg.Get[T](obj, name)
// when type info is available to infer T and the receiver package.
// Returns nil if this rule/call doesn't match the pattern.
func fixGetToField(pf *ParsedGoFile, node *ast.CallExpr, r *CompiledRule, byteOffset func(token.Pos) int, stmtOf map[ast.Node]ast.Stmt) []Edit {
	if r.ID != "get-to-field" || pf.TypesInfo == nil {
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
		if isWithVerifyFalse(pf, arg) {
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
func isWithVerifyFalse(pf *ParsedGoFile, expr ast.Expr) bool {
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

// fixSelectorExpr renames a selector expression (e.g. jws.Signer2 → jws.Signer).
func fixSelectorExpr(_ *ParsedGoFile, node *ast.SelectorExpr, r *CompiledRule, byteOffset func(token.Pos) int) *Edit {
	newName := r.ToVersion()
	if newName == "" {
		newName = r.Replacement
	}
	if newName == "" {
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
	if newName == "" {
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
