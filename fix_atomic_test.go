package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteFormatted_AtomicRenameLeavesNoResidue verifies the happy-path
// write goes through the temp+rename dance and removes the tempfile.
// A scan of the target directory must find only the final file — any
// lingering `*.jwxmigrate.tmp.*` sibling means we regressed to a direct
// overwrite or forgot to clean up after a failed rename.
func TestWriteFormatted_AtomicRenameLeavesNoResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package example\n"), 0o644))

	input := []byte("package example\n\nfunc Hello() string {return   \"hi\"}\n")
	require.NoError(t, writeFormatted(path, input, []string{"rule-a"}, false))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.Contains(e.Name(), ".jwxmigrate.tmp"),
			"leftover temp file %q after successful write", e.Name())
	}
	require.Len(t, entries, 1, "only the rewritten file should exist")
}

// TestWriteFormatted_FormatErrorLeavesNoResidue pins the invariant that
// format.Source failures short-circuit before any temp file is created,
// so a failed fix attempt never litters the tree.
func TestWriteFormatted_FormatErrorLeavesNoResidue(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	original := []byte("package example\n")
	require.NoError(t, os.WriteFile(path, original, 0o644))

	broken := []byte("package example\n\nfunc Hello() string { return \n")
	require.Error(t, writeFormatted(path, broken, []string{"rule-a"}, false))

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	for _, e := range entries {
		require.False(t, strings.Contains(e.Name(), ".jwxmigrate.tmp"),
			"leftover temp file %q after format-error early return", e.Name())
		require.False(t, strings.HasSuffix(e.Name(), ".bak"),
			"unexpected backup file %q on format-error path", e.Name())
	}
}

// TestWriteFormatted_BackupSavesOriginal verifies --backup produces a
// `.bak` sibling containing the pre-fix bytes and leaves the target with
// the new formatted bytes.
func TestWriteFormatted_BackupSavesOriginal(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	original := []byte("package example\n\nfunc Old() {}\n")
	require.NoError(t, os.WriteFile(path, original, 0o644))

	input := []byte("package example\n\nfunc New() {}\n")
	require.NoError(t, writeFormatted(path, input, []string{"rule-a"}, true))

	bak, err := os.ReadFile(path + ".bak")
	require.NoError(t, err)
	require.Equal(t, original, bak, "backup must contain pre-fix bytes")

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(got), "func New()")
}

// TestWriteFormatted_NoBackupByDefault asserts the default behavior does
// not drop `.bak` files in the user's tree.
func TestWriteFormatted_NoBackupByDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte("package example\n"), 0o644))

	require.NoError(t, writeFormatted(path, []byte("package example\n\nfunc Hello() {}\n"), []string{"rule-a"}, false))

	_, err := os.Stat(path + ".bak")
	require.True(t, os.IsNotExist(err), "unexpected .bak file produced with backup=false")
}
