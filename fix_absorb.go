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
func fixImportRemoval(pf *ParsedGoFile, node *ast.ImportSpec, byteOffset func(token.Pos) int) *Edit {
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
