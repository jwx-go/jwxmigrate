package main

import (
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
		if f.File == "v3_sample.go" && f.RuleID == "jwk-import-generic" {
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

	result, err := Check("testdata", rules, CheckOptions{RuleID: "get-to-field"})
	require.NoError(t, err)

	var found bool
	for _, f := range result.Findings {
		if f.File == "v3_sample.go" && f.RuleID == "get-to-field" {
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
	}

	for _, tt := range tests {
		t.Run(tt.pattern, func(t *testing.T) {
			require.Equal(t, tt.expected, extractNameFromPattern(tt.pattern))
		})
	}
}
