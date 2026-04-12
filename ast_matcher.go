package main

import (
	"go/ast"
	"go/token"
	"regexp"
)

// ASTMatchKind classifies what AST structure a matcher targets.
type ASTMatchKind int

const (
	// MatchImportSpec matches *ast.ImportSpec nodes (import paths).
	MatchImportSpec ASTMatchKind = iota
	// MatchSelectorExpr matches *ast.SelectorExpr nodes (pkg.Name references).
	MatchSelectorExpr
	// MatchCallExpr matches *ast.CallExpr with a SelectorExpr function (pkg.Func() calls).
	MatchCallExpr
	// MatchMethodCall matches *ast.CallExpr where the selector is a method
	// on any receiver (e.g. .Get() on any type in a v3-importing file).
	MatchMethodCall
	// MatchIdent matches bare *ast.Ident nodes (unqualified names when package
	// is imported, e.g. via dot import or within the same package).
	MatchIdent
)

// ASTMatcher describes a structural pattern to look for in the AST.
type ASTMatcher struct {
	Kind       ASTMatchKind
	ImportPath string // for MatchImportSpec: the import path substring to match
	PkgName    string // for MatchSelectorExpr/MatchCallExpr: the expected package name (e.g. "jwk")
	Name       string // the identifier name (function, type, method)
	// NamePattern, when non-nil, replaces exact-Name matching with a regex.
	// Used for rules like `jws\.Is\w+Error\(` where the target is a family
	// of identifiers rather than a single name.
	NamePattern *regexp.Regexp
}

// MatchesName reports whether the matcher matches a given identifier name.
// Uses NamePattern if set, otherwise falls back to exact comparison with Name.
func (m *ASTMatcher) MatchesName(name string) bool {
	if m.NamePattern != nil {
		return m.NamePattern.MatchString(name)
	}
	return m.Name == name
}

// ASTMatch is a single match result from walking the AST. It retains the
// matched node for potential future rewriting operations.
type ASTMatch struct {
	Matcher *ASTMatcher
	Node    ast.Node       // the matched AST node
	Pos     token.Position // resolved start position
	End     token.Position // resolved end position
	Text    string         // source text of the matched node
}
