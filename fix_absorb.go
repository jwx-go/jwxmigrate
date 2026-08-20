package main

import (
	"bytes"
	"go/ast"
	"go/token"
	"strings"
)

// This file holds the edits needed when a feature moves out of an extension
// module and into the core module, which is the inverse of
// kindMovedToExtension. Three things have to happen, and none of them was
// expressible before:
//
//  1. The extension's import is deleted outright, usually a blank import kept
//     only for its registration side effect.
//  2. A key package the extension required is swapped for the standard
//     library one, which an ordinary import_change rule already covers.
//  3. A type that was a pointer in the old package becomes a value in the
//     new one, so `*pkg.T` has to lose its star at every use site.

// fixImportRemoval deletes an entire import line, including its leading
// indentation and trailing newline, so no blank line is left behind.
//
// Returns nil when the line cannot be bounded, which keeps a malformed file
// from producing a corrupting edit.
// rewritten lists selector nodes that are being re-qualified in the same
// pass, which therefore do not keep the import alive.
func fixImportRemoval(pf *ParsedGoFile, node *ast.ImportSpec, byteOffset func(token.Pos) int, rewritten map[ast.Node]struct{}) *Edit {
	// Refuse when the file still refers to the package through something this
	// pass is not rewriting. Deleting the import would leave undefined
	// references behind, which is worse than leaving the migration to a
	// human: the rule is still reported, just not applied. A blank import
	// binds no name and is always safe to drop.
	if node.Name == nil || node.Name.Name != "_" {
		local := importLocalName(node)
		if local != "" && packageIsReferenced(pf.ASTFile, local, rewritten) {
			return nil
		}
	}

	start := byteOffset(node.Pos())
	end := byteOffset(node.End())
	if start < 0 || end < 0 || end > len(pf.Src) {
		return nil
	}

	// Widen to the start of the line so the indentation goes too.
	lineStart := bytes.LastIndexByte(pf.Src[:start], '\n')
	if lineStart < 0 {
		lineStart = 0
	} else {
		lineStart++
	}

	// Widen past a trailing line comment and the newline itself.
	lineEnd := bytes.IndexByte(pf.Src[end:], '\n')
	if lineEnd < 0 {
		lineEnd = len(pf.Src)
	} else {
		lineEnd = end + lineEnd + 1
	}

	// Only swallow the whole line when nothing else shares it. A grouped
	// single-line import such as `import _ "x"` keeps its statement.
	if bytes.ContainsAny(bytes.TrimSpace(pf.Src[lineStart:start]), "abcdefghijklmnopqrstuvwxyz") {
		return &Edit{Start: start, End: end, New: ""}
	}

	return &Edit{Start: lineStart, End: lineEnd, New: ""}
}

// fixPointerToValue removes the `*` from a pointer type reference whose
// pointee is the matched selector, turning `*pkg.T` into `pkg.T`.
//
// A rule opts into this by declaring a type_change whose from/to differ only
// by a leading star, which is how a package that moved into the standard
// library commonly changes shape.
func fixPointerToValue(star *ast.StarExpr, byteOffset func(token.Pos) int) *Edit {
	start := byteOffset(star.Star)
	end := byteOffset(star.X.Pos())
	if start < 0 || end <= start {
		return nil
	}
	return &Edit{Start: start, End: end, New: ""}
}

// pointerDropRule reports whether a rule asks for the pointer-drop above:
// kind type_change, with from and to naming the same type and differing only
// by a leading `*`.
func pointerDropRule(r *CompiledRule) bool {
	if r.Kind != kindTypeChange {
		return false
	}
	from, to := r.FromVersion(), r.ToVersion()
	if from == "" || to == "" {
		return false
	}
	return len(from) == len(to)+1 && from[0] == '*' && from[1:] == to
}

// starParents maps each expression that sits directly under a StarExpr to
// that StarExpr, so a matcher that fired on the inner selector can reach the
// star it needs to delete.
func starParents(f *ast.File) map[ast.Node]*ast.StarExpr {
	out := map[ast.Node]*ast.StarExpr{}
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		if star, ok := n.(*ast.StarExpr); ok {
			out[star.X] = star
		}
		return true
	})
	return out
}

// absorbedModules returns the extension module paths that fired
// extension_absorbed rules say are no longer needed, keyed by module path.
func absorbedModules(fired []CompiledRule) map[string]string {
	out := map[string]string{}
	for _, r := range fired {
		if r.Kind != kindExtensionAbsorbed || r.ExtensionModule == "" {
			continue
		}
		if path := modulePathOf(r.ExtensionModule); path != "" {
			out[path] = r.ID
		}
	}
	return out
}

// localNameForImport returns the local name importPath is bound to in this
// file, and whether the file imports it at all. Covers an explicit alias and
// the implicit package name; a blank import binds no usable name.
func localNameForImport(f *ast.File, importPath string) (string, bool) {
	for _, imp := range f.Imports {
		if strings.Trim(imp.Path.Value, `"`) != importPath {
			continue
		}
		if imp.Name != nil {
			if imp.Name.Name == "_" {
				return "", false
			}
			return imp.Name.Name, true
		}
		return goPkgName(importPath), true
	}
	return "", false
}

// importLocalName returns the name an import binds in its file: the explicit
// alias when there is one, otherwise the package's own name.
func importLocalName(node *ast.ImportSpec) string {
	if node.Name != nil {
		return node.Name.Name
	}
	return goPkgName(strings.Trim(node.Path.Value, `"`))
}

// packageIsReferenced reports whether any `local.Something` selector appears
// in the file. Import specs are skipped, since the import itself is not a use.
func packageIsReferenced(f *ast.File, local string, rewritten map[ast.Node]struct{}) bool {
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		if n == nil || found {
			return false
		}
		if _, isImport := n.(*ast.ImportSpec); isImport {
			return false
		}
		sel, ok := n.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		if ident, ok := sel.X.(*ast.Ident); ok && ident.Name == local {
			if _, beingFixed := rewritten[n]; !beingFixed {
				found = true
				return false
			}
		}
		return true
	})
	return found
}

// isStdlibImportPath reports whether an import path belongs to the standard
// library. The module path of anything else contains a dot in its first
// element, because it starts with a hostname.
func isStdlibImportPath(path string) bool {
	first, _, _ := strings.Cut(path, "/")
	return !strings.Contains(first, ".")
}

// fixAbsorbedSelector re-qualifies `ext.Sym` to `core.Sym`, where core is the
// package named by the rule's absorbed_into path. The symbol name is kept:
// a feature that moved into the core module keeps its name, and a rule that
// needs a rename can say so with a separate rename rule.
func fixAbsorbedSelector(node *ast.SelectorExpr, r *CompiledRule, byteOffset func(token.Pos) int) *Edit {
	if r.Kind != kindExtensionAbsorbed || r.AbsorbedInto == "" {
		return nil
	}
	xIdent, ok := node.X.(*ast.Ident)
	if !ok {
		return nil
	}
	newPkg := goPkgName(r.AbsorbedInto)
	if newPkg == "" || newPkg == xIdent.Name {
		return nil
	}
	return &Edit{Start: byteOffset(xIdent.Pos()), End: byteOffset(xIdent.End()), New: newPkg}
}

// absorbedReferences returns the selector nodes this rule will re-qualify, so
// the import-removal guard does not count them as reasons to keep the import.
// Without this the guard and the rewrite would deadlock: every usage is being
// fixed, yet the import would still look "in use".
func absorbedReferences(pf *ParsedGoFile, r *CompiledRule) map[ast.Node]struct{} {
	out := map[ast.Node]struct{}{}
	if r.Kind != kindExtensionAbsorbed || r.AbsorbedInto == "" {
		return out
	}
	local, ok := localNameForImport(pf.ASTFile, r.ExtensionModule)
	if !ok {
		return out
	}
	names := map[string]struct{}{}
	for i := range r.ASTMatchers {
		if n := r.ASTMatchers[i].Name; n != "" {
			names[n] = struct{}{}
		}
	}
	ast.Inspect(pf.ASTFile, func(n ast.Node) bool {
		if n == nil {
			return false
		}
		sel, isSel := n.(*ast.SelectorExpr)
		if !isSel {
			return true
		}
		ident, isIdent := sel.X.(*ast.Ident)
		if !isIdent || ident.Name != local {
			return true
		}
		if _, wanted := names[sel.Sel.Name]; wanted {
			out[sel] = struct{}{}
		}
		return true
	})
	return out
}

// ensureAbsorbedImports adds the absorbed_into import for any rule that
// re-qualified a symbol in this file, mirroring ensureExtensionImports.
func ensureAbsorbedImports(pf *ParsedGoFile, edits []taggedEdit, rules []CompiledRule, byteOffset func(token.Pos) int) []taggedEdit {
	if len(edits) == 0 {
		return edits
	}
	byID := make(map[string]*CompiledRule, len(rules))
	for i := range rules {
		byID[rules[i].ID] = &rules[i]
	}
	seen := map[string]struct{}{}
	for _, e := range edits {
		r, ok := byID[e.ruleID]
		if !ok || r.Kind != kindExtensionAbsorbed || r.AbsorbedInto == "" {
			continue
		}
		if _, dup := seen[r.AbsorbedInto]; dup {
			continue
		}
		seen[r.AbsorbedInto] = struct{}{}
		if importedAs(pf, r.AbsorbedInto) {
			continue
		}
		edits = appendImportEditAt(pf, edits, byteOffset, r.AbsorbedInto, e.ruleID, insertAnchor(pf, edits, byteOffset))
	}
	return edits
}

// insertAnchor picks the byte offset to insert a new import at: the first
// import spec that no pending edit deletes.
//
// appendImportEdit anchors on the first import unconditionally, which
// collides when that very import is the one being removed. The insert and the
// delete then overlap and corrupt the line.
func insertAnchor(pf *ParsedGoFile, edits []taggedEdit, byteOffset func(token.Pos) int) int {
	deleted := func(pos int) bool {
		for _, e := range edits {
			if e.New == "" && pos >= e.Start && pos < e.End {
				return true
			}
		}
		return false
	}
	for _, imp := range pf.ASTFile.Imports {
		pos := byteOffset(imp.Pos())
		if !deleted(pos) {
			return pos
		}
	}
	return -1
}

// appendImportEditAt inserts `"path"` at the given offset. A negative offset
// means no safe anchor exists, and the edit is skipped rather than guessed at.
func appendImportEditAt(pf *ParsedGoFile, edits []taggedEdit, _ func(token.Pos) int, importPath, ruleID string, at int) []taggedEdit {
	if at < 0 || len(pf.ASTFile.Imports) == 0 {
		return edits
	}
	return append(edits, taggedEdit{
		Edit:   Edit{Start: at, End: at, New: "\"" + importPath + "\"\n\t"},
		ruleID: ruleID,
	})
}
