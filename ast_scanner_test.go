package main

import (
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
	require.Contains(t, pf.V3Imports, "jwk")
	require.Contains(t, pf.V3Imports, "jwtalias")
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

func TestPackagesImportingSource(t *testing.T) {
	mod := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/m\n\ngo 1.26\n"), 0o644))

	// Bare module with nothing importing jwx: empty result skips packages.Load.
	require.NoError(t, os.WriteFile(filepath.Join(mod, "a.go"), []byte(`package m

import "fmt"

var _ = fmt.Println
`), 0o644))
	require.Empty(t, packagesImportingSource(mod))

	// Drop a v3 import into one subdir — only that directory should be listed.
	sub := filepath.Join(mod, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "b.go"), []byte(`package pkg

import "github.com/lestrrat-go/jwx/v3/jwk"

var _ = jwk.Import
`), 0o644))
	// Unrelated sibling: should NOT be in the result.
	require.NoError(t, os.MkdirAll(filepath.Join(mod, "billing"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(mod, "billing", "b.go"), []byte(`package billing

import "fmt"

var _ = fmt.Println
`), 0o644))
	require.Equal(t, []string{"./pkg"}, packagesImportingSource(mod))

	// Root package also importing v3: "." should show up.
	require.NoError(t, os.WriteFile(filepath.Join(mod, "a.go"), []byte(`package m

import "github.com/lestrrat-go/jwx/v3/jwt"

var _ = jwt.SubjectKey
`), 0o644))
	got := packagesImportingSource(mod)
	require.ElementsMatch(t, []string{".", "./pkg"}, got)

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
	require.Empty(t, packagesImportingSource(nested), "nested go.mod should prune scan")
	require.Equal(t, []string{"."}, packagesImportingSource(child))
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
	findings, coveredFiles := checkGoFilesTyped(mod, rules, CheckOptions{RuleID: "jwk-import-generic"})

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
