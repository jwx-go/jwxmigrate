package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestNormalizeTarget(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    string
		wantErr bool
	}{
		{name: "empty stays empty", in: "", want: ""},
		{name: "plain dot", in: ".", want: "."},
		{name: "plain directory", in: "pkg", want: "pkg"},
		{name: "rooted directory", in: "./pkg", want: "./pkg"},
		{name: "absolute path", in: "/abs/path", want: "/abs/path"},
		{name: "dot dot dot maps to current dir", in: "...", want: "."},
		{name: "dot slash dot dot dot maps to current dir", in: "./...", want: "."},
		{name: "rooted recursive", in: "./pkg/...", want: "./pkg"},
		{name: "unrooted recursive", in: "pkg/...", want: "pkg"},
		{name: "deep recursive", in: "./a/b/...", want: "./a/b"},
		{name: "trailing slash recursive", in: "./pkg/.../", wantErr: true},
		{name: "ellipsis in middle", in: "./a/.../b", wantErr: true},
		{name: "bare double dot dot dot", in: "....", wantErr: true},
		{name: "ellipsis fragment in name", in: "./pk.../foo", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeTarget(tc.in)
			if tc.wantErr {
				require.Error(t, err)
				return
			}
			require.NoError(t, err)
			require.Equal(t, tc.want, got)
		})
	}
}

// TestRun_DotExpansion exercises the full run() entry point with
// `--fix ./...` against a scratch project, pinning that the
// pattern reaches runFix and walks subdirectories. Without
// normalizeTarget, run() bailed with "stat ./...: no such file
// or directory" (issue #2074).
func TestRun_DotExpansion(t *testing.T) {
	dir := t.TempDir()

	// A clean file with no jwx imports — runFix should walk into
	// it and report "no mechanical fixes" rather than refusing on
	// the literal "./..." path.
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte("package x\n"), 0o644))
	require.NoError(t, os.MkdirAll(filepath.Join(dir, "sub"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "sub", "b.go"), []byte("package y\n"), 0o644))

	t.Chdir(dir)

	code, err := run([]string{"--fix", "./..."})
	require.NoError(t, err)
	require.Equal(t, 0, code)
}

// TestRun_RejectsBadPattern confirms `./a/.../b` fails fast with a
// clear error rather than falling through to a confusing stat
// failure.
func TestRun_RejectsBadPattern(t *testing.T) {
	_, err := run([]string{"--fix", "./a/.../b"})
	require.Error(t, err)
	require.Contains(t, err.Error(), "...")
}
