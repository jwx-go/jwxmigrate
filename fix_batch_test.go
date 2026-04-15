package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Regression for JWXMIGRATE-20260415151950-013: a single unparseable file
// must not abort the batch. Other files should still be processed and
// the failure should be collected into the summary so users get one
// manifest of what was skipped.
func TestFixFiles_ContinuesPastPerFileFailure(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	dir := t.TempDir()

	// Nonexistent file — FixFile falls through parseGoFileTyped to
	// parseGoFile, where os.ReadFile fails and an error bubbles up.
	missing := filepath.Join(dir, "missing.go")

	// Clean file that imports nothing from jwx — FixFile returns
	// (nil, nil) and contributes no changes, which is fine; what
	// matters is that the missing file did not prevent us from
	// reaching it.
	good := filepath.Join(dir, "good.go")
	require.NoError(t, os.WriteFile(good, []byte("package x\n\nfunc Ok() string { return \"hi\" }\n"), 0o644))

	var out, errw bytes.Buffer
	summary := fixFiles([]string{missing, good}, rules, &out, &errw)

	require.Len(t, summary.failures, 1, "missing file should be collected as a skipped failure")
	require.Equal(t, missing, summary.failures[0].file)
	require.Contains(t, errw.String(), "missing.go: skipped:", "stderr manifest should name the skipped file")
	require.Contains(t, errw.String(), "1 file(s) skipped due to errors:", "stderr should emit a summary footer")
}
