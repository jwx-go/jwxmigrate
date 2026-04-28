package main

import (
	"go/ast"
	"go/types"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Low-level unit tests for scanner internals. End-to-end scenarios are
// covered by the fixture harness in fixturetest_test.go.

func TestParseGoFile_V3Detected(t *testing.T) {
	src := `package sample

import (
	"github.com/lestrrat-go/jwx/v3/jwk"
	jwtalias "github.com/lestrrat-go/jwx/v3/jwt"
)

var _ = jwk.Import
var _ = jwtalias.SubjectKey
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	pf, err := parseGoFile(path, "a.go")
	require.NoError(t, err)
	require.NotNil(t, pf)
	require.Contains(t, pf.JwxImports, "jwk")
	require.Contains(t, pf.JwxImports, "jwtalias")
}

// TestParseGoFile_V4Detected pins the order-independence contract: a
// file that imports the migration's target version (already-migrated
// imports left over from a manual rewrite) is still surfaced for
// scanning, with the v4 import paths captured in JwxImports. Before the
// fix, parseGoFile bailed at the v3-only filter and the file was
// silently treated as "not jwx".
func TestParseGoFile_V4Detected(t *testing.T) {
	src := `package sample

import (
	"github.com/lestrrat-go/jwx/v4/jwk"
	jwtalias "github.com/lestrrat-go/jwx/v4/jwt"
)

var _ = jwk.Import[jwk.Key]
var _ = jwtalias.SubjectKey
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	pf, err := parseGoFile(path, "a.go")
	require.NoError(t, err)
	require.NotNil(t, pf)
	require.Equal(t, "github.com/lestrrat-go/jwx/v4/jwk", pf.JwxImports["jwk"])
	require.Equal(t, "github.com/lestrrat-go/jwx/v4/jwt", pf.JwxImports["jwtalias"])
}

func TestIsJwxImport(t *testing.T) {
	require.True(t, isJwxImport("github.com/lestrrat-go/jwx/v3/jwt"))
	require.True(t, isJwxImport("github.com/lestrrat-go/jwx/v3/jwk"))
	require.True(t, isJwxImport("github.com/lestrrat-go/jwx/v4/jwt"))
	require.True(t, isJwxImport("github.com/lestrrat-go/jwx/v4"))
	require.False(t, isJwxImport("github.com/lestrrat-go/jwx/v2/jwt"),
		"v2 is reachable via v2-to-v4 migration but with sourceImportPrefix rewritten by loadRules; default v3-to-v4 must reject v2 paths")
	require.False(t, isJwxImport("fmt"))
	require.False(t, isJwxImport("github.com/other/jwx/v3/jwt"))
}

func TestRewriteToTargetImport(t *testing.T) {
	require.Equal(t, "github.com/lestrrat-go/jwx/v4/jwt",
		rewriteToTargetImport("github.com/lestrrat-go/jwx/v3/jwt"),
		"v3 source path rewrites to v4")
	require.Equal(t, "github.com/lestrrat-go/jwx/v4/jwt",
		rewriteToTargetImport("github.com/lestrrat-go/jwx/v4/jwt"),
		"already-target path is returned unchanged so the helper is safe to call from either orientation")
}

func TestParseGoFile_NoV3ReturnsNil(t *testing.T) {
	src := `package sample

import "fmt"

var _ = fmt.Println
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	pf, err := parseGoFile(path, "a.go")
	require.NoError(t, err)
	require.Nil(t, pf, "file without v3 imports should return nil")
}

func TestPrescanModule(t *testing.T) {
	mod := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/m\n\ngo 1.26\n"), 0o644))

	// Bare module with nothing importing jwx: empty Patterns skips packages.Load,
	// empty V3Files skips phase 2.
	require.NoError(t, os.WriteFile(filepath.Join(mod, "a.go"), []byte(`package m

import "fmt"

var _ = fmt.Println
`), 0o644))
	ps := prescanModule(mod)
	require.Empty(t, ps.Patterns)
	require.Empty(t, ps.V3Files)

	// Drop a v3 import into one subdir — only that directory should be listed.
	sub := filepath.Join(mod, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	bGo := filepath.Join(sub, "b.go")
	require.NoError(t, os.WriteFile(bGo, []byte(`package pkg

import "github.com/lestrrat-go/jwx/v3/jwk"

var _ = jwk.Import
`), 0o644))
	// Unrelated sibling: should NOT be in the result.
	require.NoError(t, os.MkdirAll(filepath.Join(mod, "billing"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "billing", "b.go"), []byte(`package billing

import "fmt"

var _ = fmt.Println
`), 0o644))
	ps = prescanModule(mod)
	require.Equal(t, []string{"./pkg"}, ps.Patterns)
	require.Equal(t, []string{bGo}, ps.V3Files)

	// Root package also importing v3: "." should show up.
	aGo := filepath.Join(mod, "a.go")
	require.NoError(t, os.WriteFile(aGo, []byte(`package m

import "github.com/lestrrat-go/jwx/v3/jwt"

var _ = jwt.SubjectKey
`), 0o644))
	ps = prescanModule(mod)
	require.ElementsMatch(t, []string{".", "./pkg"}, ps.Patterns)
	require.ElementsMatch(t, []string{aGo, bGo}, ps.V3Files)

	// Nested go.mod must be pruned: v3 inside a submodule stays invisible
	// to the parent scan.
	nested := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(nested, "go.mod"), []byte("module example.com/parent\n\ngo 1.26\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(nested, "a.go"), []byte(`package parent
`), 0o644))
	child := filepath.Join(nested, "child")
	require.NoError(t, os.MkdirAll(child, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(child, "go.mod"), []byte("module example.com/child\n\ngo 1.26\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(child, "c.go"), []byte(`package child

import "github.com/lestrrat-go/jwx/v3/jwk"

var _ = jwk.Import
`), 0o644))
	require.Empty(t, prescanModule(nested).Patterns, "nested go.mod should prune scan")
	require.Equal(t, []string{"."}, prescanModule(child).Patterns)
}

// TestPrescanModule_AlreadyMigratedImports pins the order-independence
// counterpart of TestPrescanModule: a module whose imports were
// rewritten to v4 ahead of running jwxmigrate must still show up in the
// prescan so the fix path gets a chance to look at it. Before the
// broadened gate, V3Files was empty here and the file was silently
// excluded from every downstream pass.
func TestPrescanModule_AlreadyMigratedImports(t *testing.T) {
	mod := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"),
		[]byte("module example.com/m\n\ngo 1.26\n"), 0o644))

	v4File := filepath.Join(mod, "a.go")
	require.NoError(t, os.WriteFile(v4File, []byte(`package m

import "github.com/lestrrat-go/jwx/v4/jwt"

var _ = jwt.SubjectKey
`), 0o644))

	ps := prescanModule(mod)
	require.Equal(t, []string{"."}, ps.Patterns)
	require.Equal(t, []string{v4File}, ps.V3Files)
}

// TestParseGoFile_MixedV3AndV4Imports covers partial-migration files —
// some imports rewritten, some not. Both must end up in JwxImports so
// the rewriter has every local name it needs to resolve receiver
// packages.
func TestParseGoFile_MixedV3AndV4Imports(t *testing.T) {
	src := `package sample

import (
	"github.com/lestrrat-go/jwx/v3/jwk"
	"github.com/lestrrat-go/jwx/v4/jwt"
)

var _ = jwk.Import
var _ = jwt.SubjectKey
`
	dir := t.TempDir()
	path := filepath.Join(dir, "a.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	pf, err := parseGoFile(path, "a.go")
	require.NoError(t, err)
	require.NotNil(t, pf)
	require.Equal(t, "github.com/lestrrat-go/jwx/v3/jwk", pf.JwxImports["jwk"])
	require.Equal(t, "github.com/lestrrat-go/jwx/v4/jwt", pf.JwxImports["jwt"])
}

// TestIsJwxType_AcceptsBothVersions exercises the type-aware gate
// directly. It synthesizes named types in v3 and v4 jwx packages and
// confirms isJwxType returns true for both — and false for unrelated
// packages and unresolved expressions.
func TestIsJwxType_AcceptsBothVersions(t *testing.T) {
	v3Pkg := types.NewPackage("github.com/lestrrat-go/jwx/v3/jwt", "jwt")
	v4Pkg := types.NewPackage("github.com/lestrrat-go/jwx/v4/jwt", "jwt")
	otherPkg := types.NewPackage("example.com/other", "other")

	mkType := func(pkg *types.Package, name string) types.Type {
		obj := types.NewTypeName(0, pkg, name, nil)
		return types.NewNamed(obj, types.NewStruct(nil, nil), nil)
	}
	v3Type := mkType(v3Pkg, "Token")
	v4Type := mkType(v4Pkg, "Token")
	otherType := mkType(otherPkg, "Thing")

	v3Expr := &ast.Ident{Name: "tokV3"}
	v4Expr := &ast.Ident{Name: "tokV4"}
	otherExpr := &ast.Ident{Name: "thing"}
	unknownExpr := &ast.Ident{Name: "mystery"}

	info := &types.Info{Types: map[ast.Expr]types.TypeAndValue{
		v3Expr:    {Type: v3Type},
		v4Expr:    {Type: v4Type},
		otherExpr: {Type: otherType},
	}}

	require.True(t, isJwxType(info, v3Expr), "v3 receiver type qualifies")
	require.True(t, isJwxType(info, v4Expr), "v4 receiver type qualifies — order-independence")
	require.False(t, isJwxType(info, otherExpr), "unrelated package is not a jwx type")
	require.False(t, isJwxType(info, unknownExpr), "expression with no resolved type returns false")

	require.True(t, typeIsFromJwx(types.NewPointer(v3Type)), "pointer to v3 named type unwraps")
	require.True(t, typeIsFromJwx(types.NewPointer(v4Type)), "pointer to v4 named type unwraps")
}

func TestNamePatternMatchesWildcardFamily(t *testing.T) {
	// Rule using `jws\.Is\w+Error\(` should fire on any v2 IsXxxError call.
	rules, err := loadRules("v2-to-v4")
	require.NoError(t, err)

	src := `package example

import (
	"github.com/lestrrat-go/jwx/v2/jws"
)

func f(err error) {
	if jws.IsSignatureError(err) {
		return
	}
	if jws.IsVerificationError(err) {
		return
	}
	if jws.IsUnsupportedAlgorithmError(err) {
		return
	}
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "example.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	result, err := Check(dir, rules, CheckOptions{RuleID: "jws-isxxxerror-removed-v2"})
	require.NoError(t, err)
	require.Equal(t, 3, result.Total, "should find 3 IsXxxError calls")
	for _, f := range result.Findings {
		require.Equal(t, "ast", f.MatchedBy, "expected AST match, not regex fallback")
	}
}

func TestImportPathMatchesRemovedSubpackage(t *testing.T) {
	// jwk/x25519 was a v2 subpackage removed in v4. A rule with search
	// pattern `jwk/x25519` should match the import structurally.
	rules, err := loadRules("v2-to-v4")
	require.NoError(t, err)

	src := `package example

import (
	_ "github.com/lestrrat-go/jwx/v2/jwk/x25519"
	"github.com/lestrrat-go/jwx/v2/jwk"
)

var _ = jwk.Import
`
	dir := t.TempDir()
	path := filepath.Join(dir, "example.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	result, err := Check(dir, rules, CheckOptions{RuleID: "jwk-x25519-removed-v2"})
	require.NoError(t, err)
	require.GreaterOrEqual(t, result.Total, 1, "should find at least 1 match")
	var hasAST bool
	for _, f := range result.Findings {
		if f.MatchedBy == "ast" {
			hasAST = true
			break
		}
	}
	require.True(t, hasAST, "expected an AST match for jwk/x25519 import")
}

// TestLoadAndScanModule_TolerateTypeErrors pins the guard relaxation at
// the top of the for-pkgs loop in loadAndScanModule. When a package has
// type-check errors, the scanner must still surface findings rather than
// dropping the whole package. Two realistic ways this bites in the wild:
// a sibling file has an unrelated compile error, or the v3→v4 signature
// changes themselves (jwk.Import needs a type arg, jwk.Export takes
// fewer args) trip the type checker — which is exactly what the rule
// exists to flag, so the guard used to eat its own reason for existing.
func TestLoadAndScanModule_TolerateTypeErrors(t *testing.T) {
	mod := t.TempDir()

	// Stub the jwx v3 jwk package just enough that the main package's
	// import resolves when the scanner runs in offline CI. The stub
	// lives in a sibling directory and is wired in via `replace`.
	stub := filepath.Join(mod, "stub")
	require.NoError(t, os.MkdirAll(filepath.Join(stub, "jwk"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(stub, "go.mod"), []byte("module github.com/lestrrat-go/jwx/v3\n\ngo 1.21\n"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(stub, "jwk", "jwk.go"), []byte(`package jwk

type Key interface{}

func Import(raw any) (Key, error) { return nil, nil }
`), 0o644))

	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"), []byte(`module example.com/m

go 1.21

require github.com/lestrrat-go/jwx/v3 v3.0.0

replace github.com/lestrrat-go/jwx/v3 => ./stub
`), 0o644))

	// Main file: a legitimate v3 jwk.Import call site the scanner must
	// flag for jwk-import-generic. Type-checks fine against the stub.
	require.NoError(t, os.WriteFile(filepath.Join(mod, "main.go"), []byte(`package m

import "github.com/lestrrat-go/jwx/v3/jwk"

func Run(raw any) {
	k, _ := jwk.Import(raw)
	_ = k
}
`), 0o644))

	// Sibling file: a deliberate compile error in the same package.
	// Without the guard relaxation, packages.Load reports pkg.Errors>0
	// and loadAndScanModule would drop every file in the package —
	// including main.go's jwk.Import site.
	require.NoError(t, os.WriteFile(filepath.Join(mod, "broken.go"), []byte(`package m

var _ = undefinedSymbolThatDoesNotExist
`), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	// Drive the typed path directly. A covered main.go proves the
	// package was not dropped by the guard; Check()'s AST-only phase-2
	// fallback would also surface a finding but leaves coveredFiles
	// empty, which is what the pre-fix code produces.
	findings, coveredFiles, _ := checkGoFilesTyped(mod, rules, CheckOptions{RuleID: "jwk-import-generic"})

	mainAbs, err := filepath.Abs(filepath.Join(mod, "main.go"))
	require.NoError(t, err)
	_, covered := coveredFiles[mainAbs]
	require.True(t, covered, "typed scan dropped %s despite pkg.Errors>0; coveredFiles=%v", mainAbs, coveredFiles)

	var saw bool
	for _, f := range findings {
		if f.RuleID == "jwk-import-generic" {
			saw = true
			break
		}
	}
	require.True(t, saw, "typed scan did not surface jwk-import-generic finding; got %+v", findings)
}

func TestGoPkgName(t *testing.T) {
	tests := []struct {
		importPath string
		expected   string
	}{
		{"github.com/lestrrat-go/jwx/v3", "jwx"},
		{"github.com/lestrrat-go/jwx/v3/jwk", "jwk"},
		{"github.com/lestrrat-go/jwx/v3/jws", "jws"},
		{"github.com/lestrrat-go/jwx/v4", "jwx"},
		{"github.com/foo/bar", "bar"},
		{"fmt", "fmt"},
	}

	for _, tt := range tests {
		t.Run(tt.importPath, func(t *testing.T) {
			require.Equal(t, tt.expected, goPkgName(tt.importPath))
		})
	}
}

// TestRegexFallbackLongLine verifies that regexFallback does not silently
// skip source containing a line longer than bufio.Scanner's default 64 KiB
// cap. Switching from bufio.Scanner to bytes.Lines removes the cap entirely.
// See review item JWXMIGRATE-20260415151950-047.
func TestRegexFallbackLongLine(t *testing.T) {
	const padLen = 100 * 1024 // well over bufio.Scanner's 64 KiB default
	padding := strings.Repeat("a", padLen)
	// Single line, no newline — worst case for the old scanner.
	src := []byte(padding + " jws.SplitCompact(token) " + padding)

	pf := &ParsedGoFile{
		RelPath: "long.go",
		Src:     src,
	}
	rule := &CompiledRule{
		Rule: Rule{
			ID:         "test-splitcompact",
			Mechanical: true,
			Note:       "test rule",
		},
		Patterns: []*regexp.Regexp{regexp.MustCompile(`jws\.SplitCompact\(`)},
	}

	findings := regexFallback(pf, rule)
	require.Len(t, findings, 1, "regexFallback must not silently drop lines > 64 KiB")
	require.Equal(t, "test-splitcompact", findings[0].RuleID)
	require.Equal(t, 1, findings[0].Line)
	require.Equal(t, "long.go", findings[0].File)
}

// TestRegexFallbackCRLF verifies that CRLF line endings do not leak a
// trailing \r into Finding.Text. bytes.Lines yields lines *including* their
// terminator, so regexFallback must strip both \r and \n to match the
// behavior bufio.Scanner.Text() used to provide.
func TestRegexFallbackCRLF(t *testing.T) {
	src := []byte("package x\r\nvar _ = jws.SplitCompact(token)\r\n")
	pf := &ParsedGoFile{
		RelPath: "crlf.go",
		Src:     src,
	}
	rule := &CompiledRule{
		Rule: Rule{
			ID:         "test-splitcompact",
			Mechanical: true,
		},
		Patterns: []*regexp.Regexp{regexp.MustCompile(`jws\.SplitCompact\(`)},
	}

	findings := regexFallback(pf, rule)
	require.Len(t, findings, 1)
	require.Equal(t, 2, findings[0].Line)
	require.NotContains(t, findings[0].Text, "\r", "regexFallback must strip trailing CR from CRLF line endings")
}

func TestExtractNameFromPattern(t *testing.T) {
	tests := []struct {
		pattern  string
		expected string
	}{
		{`jwk\.Import\(`, "Import"},
		{`Signer2\b`, "Signer2"},
		{`\.Get\(`, "Get"},
		{`DecoderSettings\(`, "DecoderSettings"},
		{`lestrrat-go/jwx/v3`, ""},
		{`jwk\.NewCache\(`, "NewCache"},
		{`ReadFile\(`, "ReadFile"},
		{`\.Key\(\d`, "Key"},
		{`\.Key\([A-Za-z0-9_]+\)`, "Key"},
		{`\.Keys\(ctx`, "Keys"},
		{`\.Keys\(context`, "Keys"},
		{`jws\.Sign\(.*,`, "Sign"},
		{`^go 1\.(?:1\d|2[0-5])(?:\.\d+)?\s*$`, ""},
		{`jws\.Is\w+Error\(`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			require.Equal(t, tt.expected, extractNameFromPattern(tt.pattern))
		})
	}
}
