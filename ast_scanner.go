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
	"runtime"
	"slices"
	"sort"
	"strings"
	"sync"

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

// targetImportPrefix is set by loadRules based on the migration's "to" field.
// Exposed separately from sourceImportPrefix so rewrites can compute the
// v4 equivalent of a v3 import path — e.g. when a file references a jwx
// type transitively (via a helper's return type) and the fixer needs to
// inject an import for the matching v4 subpackage.
var targetImportPrefix = "github.com/lestrrat-go/jwx/v4"

// isJwxImport reports whether path is a jwx package — either the
// migration's source version (e.g. v3) or its target (v4). Used by the
// gates that decide "scan / fix this file?" so a project that has
// already been partially migrated (imports rewritten by hand or by
// `gopls`) still gets its leftover source-version API patterns
// rewritten. Path-shape rewrites remain anchored on sourceImportPrefix
// alone — those are no-ops on already-target paths.
func isJwxImport(path string) bool {
	return strings.HasPrefix(path, sourceImportPrefix) || strings.HasPrefix(path, targetImportPrefix)
}

// rewriteToTargetImport returns path with sourceImportPrefix rewritten
// to targetImportPrefix. When path is already on the target prefix it
// is returned unchanged, making the helper safe to call against either
// orientation.
func rewriteToTargetImport(path string) string {
	if strings.HasPrefix(path, targetImportPrefix) {
		return path
	}
	return strings.Replace(path, sourceImportPrefix, targetImportPrefix, 1)
}

// ParsedGoFile holds a parsed Go file with its jwx import mappings.
// The Src and ASTFile fields are retained for future rewriting operations.
type ParsedGoFile struct {
	RelPath    string
	Src        []byte
	FileSet    *token.FileSet
	ASTFile    *ast.File
	JwxImports map[string]string // local name -> jwx import path (source OR target version)
	TypesInfo  *types.Info       // non-nil when type-checked loading succeeded
}

// shouldSkipWalkDir reports whether a directory should be skipped during
// source-tree walks (vendor, node_modules, dotfile-prefixed dirs).
func shouldSkipWalkDir(name string) bool {
	if name == "vendor" || name == "node_modules" {
		return true
	}
	return len(name) > 0 && name[0] == '.'
}

// parseGoFile parses a Go file and builds its jwx import map.
// Returns nil (not error) if the file does not import any jwx package
// (either source or target version).
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

	jwxImports := buildJwxImportMap(astFile)
	if len(jwxImports) == 0 {
		// Not an error — file simply doesn't import jwx, skip it.
		return nil, nil //nolint:nilnil
	}

	return &ParsedGoFile{
		RelPath:    rel,
		Src:        src,
		FileSet:    fset,
		ASTFile:    astFile,
		JwxImports: jwxImports,
	}, nil
}

// parseGoFileTyped attempts to load a single Go file with type information
// via go/packages. Returns nil if type-checked loading fails.
//
// overlay, when non-nil, is forwarded to packages.Config.Overlay so the
// type checker reads the snapshot bytes for any covered file rather than
// the live disk content. Batch fixes (fixFiles) use this to keep
// in-progress rewrites from poisoning the package's compile state for
// later files.
func parseGoFileTyped(filePath string, overlay map[string][]byte) *ParsedGoFile {
	absPath, err := filepath.Abs(filePath)
	if err != nil {
		return nil
	}
	dir := filepath.Dir(absPath)

	cfg := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedFiles | packages.NeedName | packages.NeedImports | packages.NeedModule,
		Dir:     dir,
		Overlay: overlay,
		// Test files live in a separate package variant. Without
		// Tests: true, *_test.go files are not present in any pkg.GoFiles
		// so the per-file lookup below silently misses them and the
		// caller falls back to no-types mode — every type-info-dependent
		// fix (jwk-export-generic, get-to-field) silently no-ops on
		// tests.
		Tests: true,
	}
	pkgs, err := packages.Load(cfg, ".")
	if err != nil {
		return nil
	}

	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		// Tolerate package-level errors (typically: a sibling file in the
		// same package referenced an as-yet-unavailable v4 import after a
		// previous batch step rewrote it on disk). The overlay should
		// usually hide those, but if some sibling isn't in the batch the
		// type checker may still complain — accept partial type info as
		// long as the dst variables we care about have known types.
		for i, astFile := range pkg.Syntax {
			if pkg.GoFiles[i] != absPath {
				continue
			}
			jwxImports := buildJwxImportMap(astFile)
			if len(jwxImports) == 0 {
				return nil
			}
			src, err := os.ReadFile(absPath)
			if err != nil {
				return nil
			}
			return &ParsedGoFile{
				RelPath:    filePath,
				Src:        src,
				FileSet:    pkg.Fset,
				ASTFile:    astFile,
				JwxImports: jwxImports,
				TypesInfo:  pkg.TypesInfo,
			}
		}
	}
	return nil
}

// buildTypedFileCache loads the packages containing the given files with
// full type information via a single packages.Load per module root, and
// returns a map from absolute file path to a ready-to-use ParsedGoFile
// for every jwx-importing file covered by those loads.
//
// The fix batch uses this cache to avoid calling packages.Load once per
// file — a pattern that re-parses and re-type-checks every transitive
// dependency N times on large consumers. Files whose imports-only prescan
// turns up no jwx imports (source OR target version) are deliberately
// omitted; FixFileWithOptions treats a cache miss as "not a jwx file"
// and skips parseGoFileTyped entirely when a cache was supplied,
// preserving the speedup even for non-jwx files in the same batch.
//
// overlay is forwarded to packages.Config.Overlay so the type checker
// sees pre-batch content even after sibling files have been rewritten on
// disk — same semantics parseGoFileTyped used to provide per file.
func buildTypedFileCache(files []string, overlay map[string][]byte) map[string]*ParsedGoFile {
	if len(files) == 0 {
		return nil
	}
	moduleRoots := discoverModuleRootsForFiles(files)
	if len(moduleRoots) == 0 {
		return nil
	}

	cache := make(map[string]*ParsedGoFile)
	var mu sync.Mutex

	workers := min(runtime.GOMAXPROCS(0), len(moduleRoots))
	jobs := make(chan int)
	var wg sync.WaitGroup
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				ps := prescanModule(moduleRoots[i])
				if len(ps.Patterns) == 0 {
					continue
				}
				cfg := &packages.Config{
					Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
						packages.NeedFiles | packages.NeedName | packages.NeedImports | packages.NeedModule,
					Dir:     ps.Root,
					Overlay: overlay,
					Tests:   true,
				}
				pkgs, err := packages.Load(cfg, ps.Patterns...)
				if err != nil {
					continue
				}
				for _, pkg := range pkgs {
					if pkg.TypesInfo == nil {
						continue
					}
					for j, astFile := range pkg.Syntax {
						if j >= len(pkg.GoFiles) {
							continue
						}
						filePath := pkg.GoFiles[j]
						jwxImports := buildJwxImportMap(astFile)
						if len(jwxImports) == 0 {
							continue
						}
						src, readErr := os.ReadFile(filePath)
						if readErr != nil {
							continue
						}
						mu.Lock()
						if _, exists := cache[filePath]; !exists {
							cache[filePath] = &ParsedGoFile{
								RelPath:    filePath,
								Src:        src,
								FileSet:    pkg.Fset,
								ASTFile:    astFile,
								JwxImports: jwxImports,
								TypesInfo:  pkg.TypesInfo,
							}
						}
						mu.Unlock()
					}
				}
			}
		})
	}
	for i := range moduleRoots {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	return cache
}

// discoverModuleRootsForFiles returns the unique set of module roots
// (directories containing go.mod) that own the given files. Files whose
// tree contains no go.mod upward are silently skipped — buildTypedFileCache
// can't load them and the caller falls back to untyped parsing.
func discoverModuleRootsForFiles(files []string) []string {
	seen := make(map[string]struct{})
	var roots []string
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			continue
		}
		dir := filepath.Dir(abs)
		for d := dir; d != ""; {
			if _, err := os.Stat(filepath.Join(d, goModFilename)); err == nil {
				if _, ok := seen[d]; !ok {
					seen[d] = struct{}{}
					roots = append(roots, d)
				}
				break
			}
			parent := filepath.Dir(d)
			if parent == d {
				break
			}
			d = parent
		}
	}
	sort.Strings(roots)
	return roots
}

// checkGoFilesTyped discovers all Go module roots under dir and uses
// go/packages to load packages with type information from each.
// Returns findings, the set of absolute file paths that were
// type-checked, and the per-module prescans so the caller can drive
// phase 2 from the already-parsed v3-importing file set instead of
// re-walking the whole tree and re-parsing every .go file.
func checkGoFilesTyped(dir string, rules []CompiledRule, opts CheckOptions) ([]Finding, map[string]struct{}, []modulePrescan) {
	coveredFiles := make(map[string]struct{})

	absDir, err := filepath.Abs(dir)
	if err != nil {
		return nil, coveredFiles, nil
	}

	// Discover all module roots (directories containing go.mod).
	moduleRoots := findModuleRoots(absDir)
	if len(moduleRoots) == 0 {
		return nil, coveredFiles, nil
	}

	// Each loadAndScanModule call spawns `go list` + type-checks a
	// module, taking 70–300ms; on trees with many nested go.mods this
	// dominates wallclock. Run them concurrently, bounded by
	// GOMAXPROCS. Shared state (coveredFiles) is synchronized via mu;
	// per-module findings are collected into fixed slots and merged
	// after, so output order stays deterministic.
	workers := min(runtime.GOMAXPROCS(0), len(moduleRoots))
	perModule := make([][]Finding, len(moduleRoots))
	prescans := make([]modulePrescan, len(moduleRoots))
	var mu sync.Mutex
	addCovered := func(files []string) {
		mu.Lock()
		for _, f := range files {
			coveredFiles[f] = struct{}{}
		}
		mu.Unlock()
	}
	var wg sync.WaitGroup
	jobs := make(chan int)
	for range workers {
		wg.Go(func() {
			for i := range jobs {
				prescans[i] = prescanModule(moduleRoots[i])
				perModule[i] = loadAndScanModule(prescans[i], absDir, rules, opts, addCovered)
			}
		})
	}
	for i := range moduleRoots {
		jobs <- i
	}
	close(jobs)
	wg.Wait()

	var findings []Finding
	for _, ff := range perModule {
		findings = append(findings, ff...)
	}

	return findings, coveredFiles, prescans
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
		if name == goModFilename {
			roots = append(roots, filepath.Dir(p))
		}
		return nil
	})
	return roots
}

// modulePrescan is the result of walking a single module's .go files
// with imports-only parsing. Callers use it for two things:
//   - Patterns is the minimal set of package directories to hand to
//     packages.Load (narrower than "./..." when jwx lives in a subset
//     of packages, empty when no file imports jwx at all).
//   - V3Files is the absolute path of every .go file whose imports
//     include sourceImportPrefix. The untyped phase-2 scanner uses this
//     to avoid re-walking the module and re-parsing every .go file just
//     to re-derive information we already have.
type modulePrescan struct {
	Root     string
	Patterns []string
	V3Files  []string
}

// prescanModule walks modRoot and records, per .go file, whether it
// imports sourceImportPrefix. Uses parser.ImportsOnly, which is ~100×
// cheaper than a full type-checked packages.Load.
//
// Narrowing packages.Load from "./..." to Patterns stops the type
// checker from descending into sibling packages that were never going
// to match anything — the main speedup on large codebases where jwx
// only lives in a small fraction of packages. Reusing V3Files lets the
// untyped phase-2 scanner skip a whole-tree walk on files we've already
// determined have no v3 imports.
//
// Nested module roots are pruned so a parent module's scan doesn't
// re-enter a submodule we visit separately.
func prescanModule(modRoot string) modulePrescan {
	fset := token.NewFileSet()
	dirs := make(map[string]struct{})
	var v3Files []string
	_ = filepath.WalkDir(modRoot, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil //nolint:nilerr
		}
		if d.IsDir() {
			if p != modRoot && shouldSkipWalkDir(d.Name()) {
				return filepath.SkipDir
			}
			if p != modRoot {
				if _, statErr := os.Stat(filepath.Join(p, goModFilename)); statErr == nil {
					return filepath.SkipDir
				}
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") {
			return nil
		}
		f, parseErr := parser.ParseFile(fset, p, nil, parser.ImportsOnly)
		if parseErr != nil {
			// Unparseable file (e.g. testdata fixture) — skip and
			// keep walking; peer files may still have v3 imports.
			return nil //nolint:nilerr
		}
		for _, imp := range f.Imports {
			importPath := strings.Trim(imp.Path.Value, `"`)
			if isJwxImport(importPath) {
				dirs[filepath.Dir(p)] = struct{}{}
				v3Files = append(v3Files, p)
				break
			}
		}
		return nil
	})
	if len(dirs) == 0 {
		return modulePrescan{Root: modRoot}
	}
	patterns := make([]string, 0, len(dirs))
	for d := range dirs {
		rel, err := filepath.Rel(modRoot, d)
		if err != nil {
			continue
		}
		if rel == "." {
			patterns = append(patterns, ".")
		} else {
			patterns = append(patterns, "./"+filepath.ToSlash(rel))
		}
	}
	sort.Strings(patterns)
	return modulePrescan{Root: modRoot, Patterns: patterns, V3Files: v3Files}
}

// loadAndScanModule runs go/packages on a single module root and scans
// the successfully type-checked files. Covered file paths are reported
// via addCovered (a sink rather than a raw map so callers can
// synchronize concurrent module loads without exposing the mutex).
//
// An empty ps.Patterns means this module has zero jwx imports — skip
// packages.Load entirely. A non-empty set is narrower than "./...";
// the type checker only descends into packages that could actually
// match, and their deps.
func loadAndScanModule(ps modulePrescan, topDir string, rules []CompiledRule, opts CheckOptions, addCovered func([]string)) []Finding {
	if len(ps.Patterns) == 0 {
		return nil
	}

	cfg := &packages.Config{
		Mode: packages.NeedSyntax | packages.NeedTypes | packages.NeedTypesInfo |
			packages.NeedFiles | packages.NeedName | packages.NeedImports | packages.NeedModule,
		Dir: ps.Root,
	}
	pkgs, err := packages.Load(cfg, ps.Patterns...)
	if err != nil {
		return nil
	}

	var findings []Finding
	var covered []string
	for _, pkg := range pkgs {
		if pkg.TypesInfo == nil {
			continue
		}
		// Tolerate package-level errors, mirroring parseGoFileTyped.
		// Two realistic cases land here: (1) an unrelated sibling file
		// in the same package has a compile error, and (2) the v3→v4
		// signature changes themselves (jwk.Import missing type arg,
		// jwk.Export extra arg) surface as type errors — which is
		// exactly what these rules exist to flag. Per-rule fixers
		// already guard individual nodes when node-level type info is
		// missing (see fixJWKExportGeneric, fixGetToField), so partial
		// type info stays safe.
		for i, astFile := range pkg.Syntax {
			filePath := pkg.GoFiles[i]
			covered = append(covered, filePath)

			rel, relErr := filepath.Rel(topDir, filePath)
			if relErr != nil {
				rel = filePath
			}

			jwxImports := buildJwxImportMap(astFile)
			if len(jwxImports) == 0 {
				continue
			}

			src, readErr := os.ReadFile(filePath)
			if readErr != nil {
				continue
			}

			pf := &ParsedGoFile{
				RelPath:    rel,
				Src:        src,
				FileSet:    pkg.Fset,
				ASTFile:    astFile,
				JwxImports: jwxImports,
				TypesInfo:  pkg.TypesInfo,
			}

			ff := scanGoFileAST(pf, rules, opts)
			findings = append(findings, ff...)
		}
	}

	if len(covered) > 0 {
		addCovered(covered)
	}
	return findings
}

// buildJwxImportMap extracts jwx imports (source AND target version)
// and their local names from an AST file. Including the target
// version is what lets the fixer rewrite leftover source-version API
// shapes in files whose imports have already been pointed at the new
// version — `pkg.Path() == importPath` lookups in the rewriter still
// resolve to a local name, and gates that just ask "is this a jwx
// receiver?" still answer yes.
func buildJwxImportMap(f *ast.File) map[string]string {
	imports := make(map[string]string)
	for _, imp := range f.Imports {
		importPath := strings.Trim(imp.Path.Value, `"`)
		if !isJwxImport(importPath) {
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
	for localName, importPath := range pf.JwxImports {
		localToBasePkg[localName] = goPkgName(importPath)
	}

	matchesPkg := func(localName, expectedPkg string) bool {
		basePkg, ok := localToBasePkg[localName]
		if !ok {
			return false
		}
		return basePkg == expectedPkg
	}

	// matchesMatcherPkg resolves the matcher's package. A matcher carrying an
	// ImportPath names a package outside jwx, which localToBasePkg cannot
	// resolve because it only holds jwx imports.
	matchesMatcherPkg := func(m *ASTMatcher, localName string) bool {
		if m.ImportPath != "" && m.Kind != MatchImportSpec {
			want, ok := localNameForImport(pf.ASTFile, m.ImportPath)
			return ok && want == localName
		}
		return matchesPkg(localName, m.PkgName)
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
			SourceLine: sourceLineAt(pf.Src, pos.Line),
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
					if !ruleArgPredicateOK(rm.rule, node) {
						continue
					}
					if matchesMatcherPkg(rm.matcher, ident.Name) {
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
						if _, imported := pf.JwxImports[localPkg]; imported {
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
					if !isJwxType(pf.TypesInfo, sel.X) {
						continue
					}
				} else if !receiverDeclaredAsJwx(sel.X, pf.JwxImports) {
					// No type info — accept only receivers whose declared type
					// is package-qualified to a v3 import. Without this guard
					// every .Get(), .Set(), etc. in a v3-importing file would
					// match, producing false positives on unrelated code.
					continue
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
					if rm.matcher.MatchesName(selName) && matchesMatcherPkg(rm.matcher, ident.Name) {
						addFinding(rm.rule, node, "SelectorExpr")
					}
				}
			}

		case *ast.Ident:
			for _, rm := range identMatchers {
				if rm.matcher.MatchesName(node.Name) {
					if rm.matcher.PkgName == "" {
						addFinding(rm.rule, node, "Ident")
					} else if _, hasDot := pf.JwxImports["."]; hasDot {
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

// receiverDeclaredAsJwx is the untyped-mode receiver check for
// MatchMethodCall. It accepts simple-identifier receivers in two shapes:
//
//  1. The identifier names a jwx package import (e.g. jwt.ReadFile(...))
//  2. The identifier is a local var/param whose declared type is
//     package-qualified to a jwx import (e.g. tok.Get(...) where tok jwt.Token)
//
// Both source and target version imports qualify, since jwxImports now
// includes either. Chained calls, type assertions, and locally-typed
// receivers fall through, sacrificing recall for precision in scans
// without go/packages type info.
func receiverDeclaredAsJwx(expr ast.Expr, jwxImports map[string]string) bool {
	ident, ok := expr.(*ast.Ident)
	if !ok {
		return false
	}
	if _, ok := jwxImports[ident.Name]; ok {
		return true
	}
	pkg := localIdentDeclPackage(ident)
	if pkg == "" {
		return false
	}
	_, ok = jwxImports[pkg]
	return ok
}

// isJwxType checks whether the type of an expression belongs to a jwx
// package — either the migration's source version or its target. The
// broader test means rules that gate on "is this a jwx receiver?" still
// fire on files whose imports have already been pointed at the target
// version with leftover source-shape API calls.
func isJwxType(info *types.Info, expr ast.Expr) bool {
	tv, ok := info.Types[expr]
	if !ok {
		return false
	}
	return typeIsFromJwx(tv.Type)
}

// typeIsFromJwx reports whether a type (or the type it points to) is
// defined in a jwx package (source OR target version).
func typeIsFromJwx(t types.Type) bool {
	// Unwrap pointer.
	if ptr, ok := t.(*types.Pointer); ok {
		t = ptr.Elem()
	}

	// Named type — check the defining package.
	if named, ok := t.(*types.Named); ok {
		obj := named.Obj()
		if obj != nil && obj.Pkg() != nil {
			return isJwxImport(obj.Pkg().Path())
		}
	}

	// Interface satisfied by a named type embedded within.
	if iface, ok := t.Underlying().(*types.Interface); ok {
		_ = iface // interface whose concrete type we can't resolve statically
		if named, ok := t.(*types.Named); ok {
			obj := named.Obj()
			if obj != nil && obj.Pkg() != nil {
				return isJwxImport(obj.Pkg().Path())
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
					SourceLine: line,
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

// sourceLineAt returns the 1-indexed source line from src, trimmed of its
// terminating \r\n but preserving leading whitespace (so diagnostics keep
// their original indentation). Returns "" when line is out of range.
func sourceLineAt(src []byte, line int) string {
	if line <= 0 {
		return ""
	}
	i := 1
	for raw := range bytes.Lines(src) {
		if i == line {
			return string(bytes.TrimRight(raw, "\r\n"))
		}
		i++
	}
	return ""
}

// ruleArgPredicateOK is a per-rule precondition gate applied during call-
// expression matching. Most rules don't need it; rules that fix a
// specific call shape return false for calls that lack the shape, so
// the finding (and its note text) only fires on calls the fixer would
// actually rewrite. Without this, an LLM-driven migration consuming
// --format json could follow a misleading note and rewrite calls that
// already have the right shape — for jwt-withverify-false-to-parseinsecure-v2,
// that means silently disabling signature verification.
func ruleArgPredicateOK(r *CompiledRule, call *ast.CallExpr) bool {
	switch r.ID {
	case "jwt-withverify-false-to-parseinsecure-v2":
		return slices.ContainsFunc(call.Args, isWithVerifyFalse)
	case "jwk-parse-retains-unsupported-keys":
		return !slices.ContainsFunc(call.Args, isJWKSetParseMode)
	default:
		return true
	}
}

// isJWKSetParseMode reports whether arg explicitly selects a JWK Set parsing
// mode that preserves the caller's v3 behavior in v4. Strict mode does so for
// either value. Ignore-parse-error mode does so only when it is explicitly
// enabled; false leaves each version's different default in effect.
func isJWKSetParseMode(arg ast.Expr) bool {
	call, ok := arg.(*ast.CallExpr)
	if !ok {
		return false
	}

	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return false
	}

	switch sel.Sel.Name {
	case "WithStrictKeySetParsing":
		return true
	case "WithIgnoreParseError":
		return len(call.Args) == 1 && isTrueLiteral(call.Args[0])
	default:
		return false
	}
}

func isTrueLiteral(expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "true"
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
