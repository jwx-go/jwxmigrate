package main

import (
	"bytes"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubTidyRunner swaps runGoModTidy for the duration of the test, records the
// directories it was called with, and optionally returns a fixed error.
func stubTidyRunner(t *testing.T, err error) *[]string {
	t.Helper()
	var calls []string
	prev := runGoModTidy
	runGoModTidy = func(dir string, _, _ io.Writer) error {
		calls = append(calls, dir)
		return err
	}
	t.Cleanup(func() { runGoModTidy = prev })
	return &calls
}

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

// TestFixFilesRunsGoModTidyAfterRewrite pins that after -fix successfully
// rewrites a go.mod, the batch runs `go mod tidy` in that module's
// directory. Without this, callers have to remember to run tidy themselves
// and the generated go.mod carries a placeholder v4.0.0 that doesn't match
// the toolchain's actual selection.
func TestFixFilesRunsGoModTidyAfterRewrite(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	dir := t.TempDir()
	gomod := filepath.Join(dir, goModFilename)
	require.NoError(t, os.WriteFile(gomod, []byte(
		"module example\n\ngo 1.25\n\nrequire github.com/lestrrat-go/jwx/v3 v3.0.13\n",
	), 0o644))

	calls := stubTidyRunner(t, nil)

	var out, errw bytes.Buffer
	summary := fixFiles([]string{gomod}, rules, FixOptions{}, &out, &errw)

	require.Empty(t, summary.failures)
	absDir, err := filepath.Abs(dir)
	require.NoError(t, err)
	require.Equal(t, []string{absDir}, *calls, "tidy must run once in the go.mod's directory")
}

// TestFixFilesSkipsGoModTidyWhenNoGoModRewritten pins that a batch that
// only rewrote .go files does not shell out to `go mod tidy`. Running tidy
// without a go.mod change is a waste and risks surprising the user with
// unrelated dependency churn.
func TestFixFilesSkipsGoModTidyWhenNoGoModRewritten(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	dir := t.TempDir()
	good := filepath.Join(dir, "good.go")
	require.NoError(t, os.WriteFile(good, []byte("package x\n\nfunc Ok() string { return \"hi\" }\n"), 0o644))

	calls := stubTidyRunner(t, nil)

	var out, errw bytes.Buffer
	fixFiles([]string{good}, rules, FixOptions{}, &out, &errw)

	require.Empty(t, *calls, "no go.mod was rewritten, tidy must not run")
}

// TestFixFilesTidyFailureIsWarningOnly pins that a failing `go mod tidy`
// does not get folded into summary.failures. The go.mod rewrite already
// succeeded; tidy is best-effort follow-up and its failure gets surfaced
// as a stderr warning so users can rerun it manually.
func TestFixFilesTidyFailureIsWarningOnly(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	dir := t.TempDir()
	gomod := filepath.Join(dir, goModFilename)
	require.NoError(t, os.WriteFile(gomod, []byte(
		"module example\n\ngo 1.25\n\nrequire github.com/lestrrat-go/jwx/v3 v3.0.13\n",
	), 0o644))

	stubTidyRunner(t, errors.New("boom"))

	var out, errw bytes.Buffer
	summary := fixFiles([]string{gomod}, rules, FixOptions{}, &out, &errw)

	require.Empty(t, summary.failures, "tidy failure must not be recorded as a fix failure")
	require.Contains(t, errw.String(), "go mod tidy")
	require.Contains(t, errw.String(), "boom")
}
