package main

import (
	"bytes"
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

	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	result, err := FixBuildFile(gomodPath, rules)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Applied, importRuleID)

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

	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	result, err := FixBuildFile(gomodPath, rules)
	require.NoError(t, err)
	require.Nil(t, result)
}

// TestFixBuildFileIdempotentOnJwxV4 pins that a go.mod already pinned at
// the migration's target version is left alone — fixGoMod returns nil
// (no edits) so the batch doesn't trigger an unnecessary `go mod tidy`
// or report a spurious "applied" entry. The contract matters for the
// order-independence path: a user who ran `go get jwx/v4` before
// invoking `--fix` shouldn't see jwxmigrate undo and redo their work.
func TestFixBuildFileIdempotentOnJwxV4(t *testing.T) {
	tmpDir := t.TempDir()
	gomodPath := filepath.Join(tmpDir, goModFilename)
	original := "module example\n\ngo 1.25\n\nrequire github.com/lestrrat-go/jwx/v4 v4.0.0\n"
	require.NoError(t, os.WriteFile(gomodPath, []byte(original), 0o644))

	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	result, err := FixBuildFile(gomodPath, rules)
	require.NoError(t, err)
	require.Nil(t, result, "no v3 require entries means no fixes to apply")

	got, err := os.ReadFile(gomodPath)
	require.NoError(t, err)
	require.Equal(t, original, string(got),
		"go.mod content must be byte-identical when nothing was rewritten")
}

func TestFixBuildFileRewritesJwxV3RequireBlock(t *testing.T) {
	tmpDir := t.TempDir()
	gomodPath := filepath.Join(tmpDir, goModFilename)
	require.NoError(t, os.WriteFile(gomodPath, []byte(
		"module example\n\ngo 1.25\n\nrequire (\n\tgithub.com/lestrrat-go/jwx/v3 v3.0.13\n\tgithub.com/other/lib v1.0.0\n)\n",
	), 0o644))

	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	result, err := FixBuildFile(gomodPath, rules)
	require.NoError(t, err)
	require.NotNil(t, result)
	require.Contains(t, result.Applied, importRuleID)

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
	rules, err := loadRules(migrationV3ToV4)
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
	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	dir := t.TempDir()
	good := filepath.Join(dir, "good.go")
	require.NoError(t, os.WriteFile(good, []byte("package x\n\nfunc Ok() string { return \"hi\" }\n"), 0o644))

	calls := stubTidyRunner(t, nil)

	var out, errw bytes.Buffer
	fixFiles([]string{good}, rules, FixOptions{}, &out, &errw)

	require.Empty(t, *calls, "no go.mod was rewritten, tidy must not run")
}

// (TestFixFilesTidyFailureIsWarningOnly was inverted by the
// JWXMIGRATE-002 fix — see TestFixFiles_TidyFailureSurfaces in
// fix_robustness_test.go for the new contract: a failing
// `go mod tidy` is now recorded in summary.failures so runFix
// returns a non-zero exit code.)
