package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestWriteFormatted_RefusesBrokenSource covers EXT-201: if an edit produces
// bytes that gofmt cannot parse, writeFormatted must return an error instead
// of silently writing the unformatted buffer. The original file on disk
// must remain untouched so the user's `go build` still compiles.
func TestWriteFormatted_RefusesBrokenSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	original := []byte("package example\n\nfunc Hello() string { return \"hi\" }\n")
	require.NoError(t, os.WriteFile(path, original, 0o644))

	broken := []byte("package example\n\nfunc Hello() string { return \n")
	err := writeFormatted(path, broken, []string{"rule-a", "rule-b"}, false)
	require.Error(t, err, `writeFormatted should refuse to write syntactically-broken Go`)
	require.Contains(t, err.Error(), "refusing to write")
	require.Contains(t, err.Error(), "rule-a")
	require.Contains(t, err.Error(), "rule-b")

	after, readErr := os.ReadFile(path)
	require.NoError(t, readErr)
	require.Equal(t, original, after, `original file must not have been overwritten with broken bytes`)
}

// TestWriteFormatted_WritesValidSource is a smoke test that the happy path
// still writes gofmt-normalized bytes.
func TestWriteFormatted_WritesValidSource(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")

	// Intentionally unformatted — extra blank line and weird spacing.
	input := []byte("package example\n\n\nfunc Hello() string {return   \"hi\"}\n")
	require.NoError(t, writeFormatted(path, input, []string{"rule-a"}, false))

	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(got), `return "hi"`, `output should be gofmt-normalized`)
}
