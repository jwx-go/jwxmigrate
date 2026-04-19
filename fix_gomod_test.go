package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFixBuildFileRewritesJwxV3RequireToV4(t *testing.T) {
	tmpDir := t.TempDir()
	gomodPath := filepath.Join(tmpDir, "go.mod")
	require.NoError(t, os.WriteFile(gomodPath, []byte(
		"module example\n\ngo 1.25\n\nrequire github.com/lestrrat-go/jwx/v3 v3.0.13\n",
	), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := FixBuildFile(gomodPath, rules)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Applied, "import-v3-to-v4")

	got, err := os.ReadFile(gomodPath)
	require.NoError(t, err)
	gotStr := string(got)
	require.Contains(t, gotStr, "github.com/lestrrat-go/jwx/v4")
	require.NotContains(t, gotStr, "github.com/lestrrat-go/jwx/v3")
	require.True(t, strings.HasPrefix(extractJwxRequireVersion(gotStr), "v4."),
		"jwx require version should be v4.x, got %s in %s", extractJwxRequireVersion(gotStr), gotStr)
}

func TestFixBuildFileSkipsGoModWithoutJwxV3(t *testing.T) {
	tmpDir := t.TempDir()
	gomodPath := filepath.Join(tmpDir, "go.mod")
	require.NoError(t, os.WriteFile(gomodPath, []byte(
		"module example\n\ngo 1.25\n\nrequire github.com/other/lib v1.0.0\n",
	), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := FixBuildFile(gomodPath, rules)
	require.NoError(t, err)
	require.Nil(t, result)
}

func TestFixBuildFileRewritesJwxV3RequireBlock(t *testing.T) {
	tmpDir := t.TempDir()
	gomodPath := filepath.Join(tmpDir, "go.mod")
	require.NoError(t, os.WriteFile(gomodPath, []byte(
		"module example\n\ngo 1.25\n\nrequire (\n\tgithub.com/lestrrat-go/jwx/v3 v3.0.13\n\tgithub.com/other/lib v1.0.0\n)\n",
	), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := FixBuildFile(gomodPath, rules)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Applied, "import-v3-to-v4")

	got, err := os.ReadFile(gomodPath)
	require.NoError(t, err)
	gotStr := string(got)
	require.Contains(t, gotStr, "github.com/lestrrat-go/jwx/v4")
	require.NotContains(t, gotStr, "github.com/lestrrat-go/jwx/v3")
	require.Contains(t, gotStr, "github.com/other/lib v1.0.0")
}

// extractJwxRequireVersion finds the version string after the jwx module path.
// Returns "" when not found.
func extractJwxRequireVersion(modContent string) string {
	for line := range strings.Lines(modContent) {
		trimmed := strings.TrimSpace(line)
		const prefix = "github.com/lestrrat-go/jwx/v4 "
		if idx := strings.Index(trimmed, prefix); idx >= 0 {
			rest := trimmed[idx+len(prefix):]
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return fields[0]
			}
		}
		if strings.HasPrefix(trimmed, "require github.com/lestrrat-go/jwx/v4 ") {
			rest := strings.TrimPrefix(trimmed, "require github.com/lestrrat-go/jwx/v4 ")
			fields := strings.Fields(rest)
			if len(fields) > 0 {
				return fields[0]
			}
		}
	}
	return ""
}
