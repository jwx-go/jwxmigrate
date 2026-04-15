package main

import (
	"bytes"
	"errors"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"golang.org/x/tools/go/packages"
)

// errParseFailed wraps a parser.ParseFile failure returned from parseGoFile.
// Scanner callers swallow this sentinel (broken testdata files are expected);
// the --fix path surfaces it so users see which files were skipped instead of
// finishing with a false-clean report.
var errParseFailed = errors.New("parse failed")

var versionSuffix = regexp.MustCompile(`^v\d+$`)

// sourceImportPrefix is set by loadRules based on the migration's "from" field.
var sourceImportPrefix = "github.com/lestrrat-go/jwx/v3"

// ParsedGoFile holds a parsed Go file with its v3 import mappings.
// The Src and ASTFile fields are retained for future rewriting operations.
type ParsedGoFile struct {
	RelPath   string
	Src       []byte
	FileSet   *token.FileSet
	ASTFile   *ast.File
	V3Imports map[string]string // local name -> v3 import path
	TypesInfo *types.Info       // non-nil when type-checked loading succeeded
}

// shouldSkipWalkDir reports whether a directory should be skipped during
// source-tree walks (vendor, node_modules, dotfile-prefixed dirs).
func shouldSkipWalkDir(name string) bool {
	if name == "vendor" || name == "node_modules" {
		return true
	}
	return len(name) > 0 && name[0] == '.'
}

// parseGoFile parses a Go file and builds the v3 import map.
// Returns nil (not error) if the file does not import any v3 package.
func parseGoFile(filePath, rel string) (*ParsedGoFile, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, err
	}

	fset := token.NewFileSet()
	astFile, err := parser.ParseFile(fset, filePath, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", errParseFailed, err)
	}

	v3Imports := buildV3ImportMap(astFile)
	if len(v3Imports) == 0 {
		// Not an error — file simply doesn't import v3, skip it.
		return nil, nil //nolint:nilnil
	}

	return &ParsedGoFile{
		RelPath:   rel,
		Src:       src,
		FileSet:   fset,
		ASTFile:   astFile,
		V3Imports: v3Imports,
	}, nil
}

// parseGoFileTyped attempts to load a single Go file with type information
// via go/packages. Returns nil if type-checked loading fails.
func parseGoFileTyped(filePath string) *ParsedGoFile {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(absPath)

	cfg := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedFiles | packages.NeedName | packages.NeedImports | packages.NeedModule,
		Dir: dir,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil
	}

	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil || len(pkg.Errors) > 0 {
			continue
		}
		for i, astFile := range pkg.Syntax {
			if pkg.GoFiles[i] != absPath {
				continue
			}
			v3Imports := buildV3ImportMap(astFile)
			if len(v3Imports) == 0 {
				return nil
			}
			src, err := os.ReadFile(absPath)
			if err != nil {
				return nil
			}
			return &ParsedGoFile{
				RelPath:   filePath,
				Src:       src,
				FileSet:   pkg.Fset,
				ASTFile:   astFile,
				V3Imports: v3Imports,
				TypesInfo: pkg.TypesInfo,
			}
		}
	}
	return nil
}

// checkGoFilesTyped discovers all Go module roots under dir and uses
// go/packages to load packages with type information from each.
// Returns findings and the set of absolute file paths that were processed.
func checkGoFilesTyped(dir string, rules []CompiledRule, opts CheckOptions) ([]Finding, map[string]struct{}) {
	coveredFiles := make(map[string]struct{})

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, coveredFiles
	}

	// Discover all module roots (directories containing go.mod).
	moduleRoots := findModuleRoots(absDir)
	if len(moduleRoots) == 0 {
		return nil, coveredFiles
	}

	var findings []Finding
	for _, modRoot := range moduleRoots {
		ff := loadAndScanModule(modRoot, absDir, rules, opts, coveredFiles)
		findings = append(findings, ff...)
	}

	return findings, coveredFiles
}

// findModuleRoots walks the directory tree and returns all directories
// that contain a go.mod file.
func findModuleRoots(dir string) []string {
	var roots []string
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// Unreadable directories are skipped rather than aborting
			// the whole walk. The outer WalkDir error is already discarded.
			return nil //nolint:nilerr
		}
		name := d.Name()
		if d.IsDir() {
			if shouldSkipWalkDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if name == "go.mod" {
			roots = append(roots, filepath.Dir(p))
		}
		return nil
	})
	return roots
}

// loadAndScanModule runs go/packages on a single module root and scans
// the successfully type-checked files. Covered file paths are added to
// the coveredFiles set.
func loadAndScanModule(modRoot, topDir string, rules []CompiledRule, opts CheckOptions, coveredFiles map[string]struct{}) []Finding {
	cfg := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedFiles | packages.NeedName | packages.NeedImports | packages.NeedModule,
		Dir: modRoot,
	}
	pkgs, err := packages.Load(cfg, "./...")
	if err != nil {
		return nil
	}

	var findings []Finding
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil || len(pkg.Errors) > 0 {
			continue
		}
		for i, astFile := range pkg.Syntax {
			filePath := pkg.GoFiles[i]
			coveredFiles[filePath] = struct{}{}

			rel, relErr := filepath.Rel(topDir, filePath)
			if relErr != nil {
				rel = filePath
			}

			v3Imports := buildV3ImportMap(astFile)
			if len(v3Imports) == 0 {
				continue
			}

			src, readErr := os.ReadFile(filePath)
			if readErr != nil {
				continue
			}

			pf := &ParsedGoFile{
				RelPath:   rel,
				Src:       src,
				FileSet:   pkg.Fset,
				ASTFile:   astFile,
				V3Imports: v3Imports,
				TypesInfo: pkg.TypesInfo,
			}

			ff := scanGoFileAST(pf, rules, opts)
			findings = append(findings, ff...)
		}
	}

	return findings
}

// buildV3ImportMap extracts v3 imports and their local names from an AST file.
func buildV3ImportMap(f *ast.File) map[string]string {
	imports := make(map[string]string)
	for _, imp := range f.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if !strings.HasPrefix(importPath, sourceImportPrefix) {
			continue
		}

		var localName string
		if imp.Name != nil {
			localName = imp.Name.Name
		} else {
			localName = goPkgName(importPath)
		}
		imports[localName] = importPath
	}
	return imports
}

// scanGoFileAST walks the AST of a parsed Go file and matches all rules.
// Rules with ASTMatchers use structural matching; rules without fall back to regex.
func scanGoFileAST(pf *ParsedGoFile, rules []CompiledRule, opts CheckOptions) []Finding {
	type ruleMatch struct {
		rule    *CompiledRule
		matcher *ASTMatcher
	}

	var (
		importMatchers   []ruleMatch
		callMatchers     []ruleMatch
		methodMatchers   []ruleMatch
		selectorMatchers []ruleMatch
		identMatchers    []ruleMatch
		regexRules       []*CompiledRule
	)

	for i := range rules {
		r := &rules[i]
		if shouldSkip(r, opts) {
			continue
		}
		if len(r.ASTMatchers) == 0 {
			if len(r.Patterns) > 0 {
				regexRules = append(regexRules, r)
			}
			continue
		}
		for j := range r.ASTMatchers {
			m := &r.ASTMatchers[j]
			rm := ruleMatch{rule: r, matcher: m}
			switch m.Kind {
			case MatchImportSpec:
				importMatchers = append(importMatchers, rm)
			case MatchCallExpr:
				callMatchers = append(callMatchers, rm)
			case MatchMethodCall:
				methodMatchers = append(methodMatchers, rm)
			case MatchSelectorExpr:
				selectorMatchers = append(selectorMatchers, rm)
			case MatchIdent:
				identMatchers = append(identMatchers, rm)
			}
		}
	}

	var findings []Finding

	type lineKey struct {
		ruleID string
		line   int
	}
	matched := make(map[lineKey]struct{})

	localToBasePkg := make(map[string]string)
	for localName, importPath := range pf.V3Imports {
		localToBasePkg[localName] = goPkgName(importPath)
	}

	matchesPkg := func(localName, expectedPkg string) bool {
		basePkg, ok := localToBasePkg[localName]
		if !ok {
			return false
		}
		return basePkg == expectedPkg
	}

	addFinding := func(r *CompiledRule, n ast.Node, nodeKind string) {
		pos := pf.FileSet.Position(n.Pos())
		end := pf.FileSet.Position(n.End())

		key := lineKey{ruleID: r.ID, line: pos.Line}
		if _, ok := matched[key]; ok {
			return
		}
		matched[key] = struct{}{}

		text := extractNodeText(pf, n)
		f := Finding{
			RuleID:     r.ID,
			File:       pf.RelPath,
			Line:       pos.Line,
			Col:        pos.Column,
			EndLine:    end.Line,
			EndCol:     end.Column,
			Text:       text,
			Mechanical: r.Mechanical,
			Note:       strings.TrimSpace(r.Note),
			NodeKind:   nodeKind,
			MatchedBy:  "ast",
		}
		if r.Example != nil {
			f.ExampleBefore = strings.TrimSpace(r.Example.Before)
			f.ExampleAfter = strings.TrimSpace(r.Example.After)
		}
		findings = append(findings, f)
	}

	// Pre-collect call expression function nodes to avoid double-matching
	// them as standalone SelectorExpr.
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

	ast.Inspect(pf.ASTFile, func(n ast.Node) bool {
		if n == nil {
			return false
		}

		switch node := n.(type) {
		case *ast.ImportSpec:
			importPath := strings.Trim(node.Path.Value, `"`)
			for _, rm := range importMatchers {
				if strings.Contains(importPath, rm.matcher.ImportPath) {
					addFinding(rm.rule, node, "ImportSpec")
				}
			}

		case *ast.CallExpr:
			sel, ok := node.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			funcName := sel.Sel.Name

			if ident, ok := sel.X.(*ast.Ident); ok {
				// Package-qualified calls: pkg.Func(...)
				for _, rm := range callMatchers {
					if !rm.matcher.MatchesName(funcName) {
						continue
					}
					if matchesPkg(ident.Name, rm.matcher.PkgName) {
						addFinding(rm.rule, node, "CallExpr")
						continue
					}
					// Method-on-local-var fallback: when the receiver is a
					// local variable whose declared type is from a v3
					// package (e.g. func iterate(set jwk.Set) { set.Len() }),
					// treat set.Len() as a call on the jwk package for
					// matching purposes. This lets rules like jwk-set-iterator
					// fire on method calls without needing type-checked
					// loading — the parser-populated Obj chain is enough for
					// func params and explicitly-typed var declarations.
					if localPkg := localIdentDeclPackage(ident); localPkg != "" && localPkg == rm.matcher.PkgName {
						if _, imported := pf.V3Imports[localPkg]; imported {
							addFinding(rm.rule, node, "CallExpr")
						}
					}
				}
			}

			// Method calls: receiver.Method(...)
			for _, rm := range methodMatchers {
				if !rm.matcher.MatchesName(funcName) {
					continue
				}
				if pf.TypesInfo != nil {
					// Type info available — only match if receiver is a v3 type.
					if !isV3Type(pf.TypesInfo, sel.X) {
						continue
					}
				}
				addFinding(rm.rule, node, "CallExpr")
			}

		case *ast.SelectorExpr:
			if _, isCallFun := callFuns[node]; isCallFun {
				return true
			}

			selName := node.Sel.Name
			if ident, ok := node.X.(*ast.Ident); ok {
				for _, rm := range selectorMatchers {
					if rm.matcher.MatchesName(selName) && matchesPkg(ident.Name, rm.matcher.PkgName) {
						addFinding(rm.rule, node, "SelectorExpr")
					}
				}
			}

		case *ast.Ident:
			for _, rm := range identMatchers {
				if rm.matcher.MatchesName(node.Name) {
					if rm.matcher.PkgName == "" {
						addFinding(rm.rule, node, "Ident")
					} else if _, hasDot := pf.V3Imports["."]; hasDot {
						addFinding(rm.rule, node, "Ident")
					}
				}
			}
		}

		return true
	})

	for _, r := range regexRules {
		ff := regexFallback(pf, r)
		findings = append(findings, ff...)
	}

	return findings
}

// localIdentDeclPackage returns the local package name of the type an
// identifier was declared as, using the parser-populated ast.Ident.Obj
// chain. Handles function parameters and `var x T` declarations without
// needing type-checked loading. Returns "" when the type is unknown,
// from the built-in scope, or not package-qualified.
//
// Supported forms:
//
//	func f(x jwk.Set)          → "jwk"
//	func f(x *jwk.Set)         → "jwk"
//	var x jwk.Set              → "jwk"
//	var x []jwk.Set            → "jwk"
//
// Unsupported (return ""):
//
//	x := jwk.NewSet()          (no explicit type)
//	f(x SomeLocalType)         (type is local, not package-qualified)
//	x.Field (struct field)
func localIdentDeclPackage(ident *ast.Ident) string {
	if ident.Obj == nil {
		return ""
	}
	var typeExpr ast.Expr
	switch d := ident.Obj.Decl.(type) {
	case *ast.Field:
		typeExpr = d.Type
	case *ast.ValueSpec:
		typeExpr = d.Type
	default:
		return ""
	}
	for typeExpr != nil {
		switch t := typeExpr.(type) {
		case *ast.StarExpr:
			typeExpr = t.X
		case *ast.ArrayType:
			typeExpr = t.Elt
		case *ast.SelectorExpr:
			pkgIdent, ok := t.X.(*ast.Ident)
			if !ok {
				return ""
			}
			return pkgIdent.Name
		default:
			return ""
		}
	}
	return ""
}

// isV3Type checks whether the type of an expression belongs to a v3 jwx package.
func isV3Type(info *types.Info, expr ast.Expr) bool {
	tv, ok := info.Types[expr]
	if !ok {
		return false
	}
	return typeIsFromV3(tv.Type)
}

// typeIsFromV3 checks whether a type (or the type it points to) is defined
// in a v3 jwx package.
func typeIsFromV3(t types.Type) bool {
	// Unwrap pointer.
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}

	// Named type — check the defining package.
	if named, ok := t.(*types.Named); ok {
		obj := named.Obj()
		if obj != nil && obj.Pkg() != nil {
			return strings.HasPrefix(obj.Pkg().Path(), sourceImportPrefix)
		}
	}

	// Interface satisfied by a named type embedded within.
	if iface, ok := t.Underlying().(*types.Interface); ok {
		_ = iface // interface whose concrete type we can't resolve statically
		// For interfaces, check if the type itself is named and from v3.
		if named, ok := t.(*types.Named); ok {
			obj := named.Obj()
			if obj != nil && obj.Pkg() != nil {
				return strings.HasPrefix(obj.Pkg().Path(), sourceImportPrefix)
			}
		}
	}

	return false
}

// regexFallback performs line-by-line regex matching on the source bytes.
// pf.Src is already in memory, so we iterate via bytes.Lines instead of
// bufio.Scanner — no per-line size cap and no error path to worry about.
func regexFallback(pf *ParsedGoFile, r *CompiledRule) []Finding {
	var findings []Finding
	lineNum := 0
	for rawLine := range bytes.Lines(pf.Src) {
		lineNum++
		line := string(bytes.TrimRight(rawLine, "\r\n"))
		for _, pat := range r.Patterns {
			if pat.MatchString(line) {
				f := Finding{
					RuleID:     r.ID,
					File:       pf.RelPath,
					Line:       lineNum,
					Text:       strings.TrimSpace(line),
					Mechanical: r.Mechanical,
					Note:       strings.TrimSpace(r.Note),
					MatchedBy:  "regex",
				}
				if r.Example != nil {
					f.ExampleBefore = strings.TrimSpace(r.Example.Before)
					f.ExampleAfter = strings.TrimSpace(r.Example.After)
				}
				findings = append(findings, f)
				break
			}
		}
	}
	return findings
}

// goPkgName returns the Go package name for an import path.
func goPkgName(importPath string) string {
	base := path.Base(importPath)
	if versionSuffix.MatchString(base) {
		dir := path.Dir(importPath)
		return path.Base(dir)
	}
	return base
}

// extractNodeText extracts the source text corresponding to an AST node.
func extractNodeText(pf *ParsedGoFile, n ast.Node) string {
	file := pf.FileSet.File(pf.ASTFile.Pos())
	if file == nil {
		return ""
	}
	base := file.Base()
	start := int(n.Pos()) - base
	end := int(n.End()) - base
	if start < 0 || end > len(pf.Src) || start >= end {
		return ""
	}
	text := string(pf.Src[start:end])
	return strings.Join(strings.Fields(text), " ")
}
