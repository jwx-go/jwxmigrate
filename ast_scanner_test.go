package main

import (
	"os"
	"path/filepath"
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
