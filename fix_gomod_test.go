package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFixBuildFileRewritesJwxV3RequireToV4(t *testing.T) {
	tmpDir := t.TempDir()
	gomodPath := filepath.Join(tmpDir, goModFilename)
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
	require.Contains(t, gotStr, "github.com/lestrrat-go/jwx/v4 "+latestV4Version)
	require.NotContains(t, gotStr, "github.com/lestrrat-go/jwx/v3")
}

func TestFixBuildFileSkipsGoModWithoutJwxV3(t *testing.T) {
	tmpDir := t.TempDir()
	gomodPath := filepath.Join(tmpDir, goModFilename)
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
	gomodPath := filepath.Join(tmpDir, goModFilename)
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
	require.Contains(t, gotStr, "github.com/lestrrat-go/jwx/v4 "+latestV4Version)
	require.NotContains(t, gotStr, "github.com/lestrrat-go/jwx/v3")
	require.Contains(t, gotStr, "github.com/other/lib v1.0.0")
}
