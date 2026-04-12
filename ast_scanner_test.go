package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseGoFile(t *testing.T) {
	t.Run("v3 imports detected", func(t *testing.T) {
		pf, err := parseGoFile("testdata/v3_sample.go", "v3_sample.go")
		require.NoError(t, err)
		require.NotNil(t, pf, "file with v3 imports should parse")
		require.NotEmpty(t, pf.V3Imports)

		found := make(map[string]struct{})
		for localName := range pf.V3Imports {
			found[localName] = struct{}{}
		}
		require.Contains(t, found, "jwk")
		require.Contains(t, found, "jws")
		require.Contains(t, found, "jwt")
		require.Contains(t, found, "jwx")
	})

	t.Run("v4 file returns nil", func(t *testing.T) {
		pf, err := parseGoFile("testdata/v4_clean.go", "v4_clean.go")
		require.NoError(t, err)
		require.Nil(t, pf, "file without v3 imports should return nil")
	})

	t.Run("aliased imports resolved", func(t *testing.T) {
		pf, err := parseGoFile("testdata/v3_aliased.go", "v3_aliased.go")
		require.NoError(t, err)
		require.NotNil(t, pf)

		require.Contains(t, pf.V3Imports, "myjwk")
		require.Contains(t, pf.V3Imports, "myjwt")
	})
}

func TestNoFalsePositivesInComments(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{})
	require.NoError(t, err)

	var commentFindings []Finding
	for _, f := range result.Findings {
		if f.File == "v3_comments.go" {
			commentFindings = append(commentFindings, f)
		}
	}

	for _, f := range commentFindings {
		require.Equal(t, "import-v3-to-v4", f.RuleID,
			"v3_comments.go should only trigger import rules, got %s on line %d: %s",
			f.RuleID, f.Line, f.Text)
	}
	require.NotEmpty(t, commentFindings, "should find at least the import")
}

func TestASTMatchAliasedImports(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{})
	require.NoError(t, err)

	foundRules := make(map[string]struct{})
	for _, f := range result.Findings {
		if f.File == "v3_aliased.go" {
			foundRules[f.RuleID] = struct{}{}
		}
	}

	expected := []string{
		"import-v3-to-v4",
		"jwk-import-generic",
		"readfile-to-parsefs",
		"register-custom-field-generic",
	}
	for _, id := range expected {
		_, ok := foundRules[id]
		require.True(t, ok, "expected rule %s to trigger on v3_aliased.go, found: %v", id, foundRules)
	}
}

func TestASTMatchPositionInfo(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{RuleID: "jwk-import-generic"})
	require.NoError(t, err)

	var found *Finding
	for i, f := range result.Findings {
		if f.File == v3SampleFile && f.RuleID == "jwk-import-generic" {
			found = &result.Findings[i]
			break
		}
	}
	require.NotNil(t, found, "should find jwk-import-generic in v3_sample.go")

	require.Greater(t, found.Col, 0, "Col should be set")
	require.Greater(t, found.EndLine, 0, "EndLine should be set")
	require.Greater(t, found.EndCol, 0, "EndCol should be set")
	require.Equal(t, "CallExpr", found.NodeKind)
	require.Equal(t, "ast", found.MatchedBy)
}

func TestASTMatchMethodCall(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{RuleID: ruleGetToField})
	require.NoError(t, err)

	var found bool
	for _, f := range result.Findings {
		if f.File == v3SampleFile && f.RuleID == ruleGetToField {
			found = true
			require.Equal(t, "ast", f.MatchedBy)
			break
		}
	}
	require.True(t, found, "get-to-field should trigger on v3_sample.go")
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
	// At least one finding should be AST-matched.
	var hasAST bool
	for _, f := range result.Findings {
		if f.MatchedBy == "ast" {
			hasAST = true
			break
		}
	}
	require.True(t, hasAST, "expected an AST match for jwk/x25519 import")
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
		// Patterns with arguments inside parentheses — identifier is still extractable.
		{`\.Key\(\d`, "Key"},
		{`\.Key\([A-Za-z0-9_]+\)`, "Key"},
		{`\.Keys\(ctx`, "Keys"},
		{`\.Keys\(context`, "Keys"},
		// Pkg-qualified with trailing args.
		{`jws\.Sign\(.*,`, "Sign"},
		// Anchored patterns.
		{`^go 1\.(?:1\d|2[0-5])(?:\.\d+)?\s*$`, ""},
		// Wildcard name — no single identifier.
		{`jws\.Is\w+Error\(`, ""},
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			require.Equal(t, tt.expected, extractNameFromPattern(tt.pattern))
		})
	}
}
