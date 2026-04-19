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

func TestModuleImportsSource(t *testing.T) {
	mod := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(mod, "go.mod"), []byte("module example.com/m\n\ngo 1.26\n"), 0o644))

	// Bare module with nothing importing jwx: fast path should skip.
	require.NoError(t, os.WriteFile(filepath.Join(mod, "a.go"), []byte(`package m

import "fmt"

var _ = fmt.Println
`), 0o644))
	require.False(t, moduleImportsSource(mod))

	// Drop a v3 import into a subdir — should now detect.
	sub := filepath.Join(mod, "pkg")
	require.NoError(t, os.MkdirAll(sub, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sub, "b.go"), []byte(`package pkg

import "github.com/lestrrat-go/jwx/v3/jwk"

var _ = jwk.Import
`), 0o644))
	require.True(t, moduleImportsSource(mod))

	// Nested go.mod must be pruned: if the v3 import lives only in a
	// submodule, the parent scan should NOT report true (that submodule
	// gets its own pass).
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
	require.False(t, moduleImportsSource(nested), "nested go.mod should prune scan")
	require.True(t, moduleImportsSource(child))
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
