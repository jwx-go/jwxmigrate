package main

import (
	"bytes"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFixFiles_TidyFailureSurfaces pins the JWXMIGRATE-002 fix:
// when `go mod tidy` exits non-zero after a successful rewrite, the
// failure must land in summary.failures so runFix returns a non-zero
// exit code. Before the fix the error was printed to stderr and
// dropped on the floor, leaving the working tree unbuildable while
// the tool reported success.
func TestFixFiles_TidyFailureSurfaces(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "go.mod"), []byte(
		"module example.test\n\ngo 1.25\n\nrequire github.com/lestrrat-go/jwx/v3 v3.0.0\n"), 0o644))

	// Stub runGoModTidy with a recording-fail variant; restore on
	// cleanup so other tests in the package see the real toolchain.
	orig := runGoModTidy
	t.Cleanup(func() { runGoModTidy = orig })
	runGoModTidy = func(_ string, _, _ io.Writer) error {
		return fmt.Errorf("synthetic tidy failure")
	}

	var out, errw bytes.Buffer
	summary := fixFiles([]string{filepath.Join(dir, "go.mod")}, rules, FixOptions{}, &out, &errw)

	require.NotEmpty(t, summary.failures,
		"tidy failure must be recorded so runFix returns non-zero")
	var found bool
	for _, f := range summary.failures {
		if filepath.Base(f.file) == "go.mod" {
			found = true
			require.Contains(t, f.err.Error(), "go mod tidy",
				"failure must identify itself as a tidy failure")
		}
	}
	require.True(t, found, "tidy failure must reference the go.mod that was tidied")
}

// TestAppendGOEXPERIMENT covers the JWXMIGRATE-002 sub-fix that
// makes defaultRunGoModTidy set GOEXPERIMENT=jsonv2 in the tidy env
// (jwx v4 requires it; without it tidy fails with "build constraints
// exclude all Go files"). The helper must preserve any GOEXPERIMENT
// the user already set instead of clobbering it.
func TestAppendGOEXPERIMENT(t *testing.T) {
	t.Run("appends to empty env", func(t *testing.T) {
		got := appendGOEXPERIMENT([]string{"FOO=bar"}, "jsonv2")
		require.Contains(t, got, "GOEXPERIMENT=jsonv2")
	})
	t.Run("appends to existing GOEXPERIMENT", func(t *testing.T) {
		got := appendGOEXPERIMENT([]string{"FOO=bar", "GOEXPERIMENT=newinliner"}, "jsonv2")
		require.Contains(t, got, "GOEXPERIMENT=newinliner,jsonv2")
		require.NotContains(t, got, "GOEXPERIMENT=jsonv2",
			"original value must not be clobbered")
	})
	t.Run("idempotent if already present", func(t *testing.T) {
		got := appendGOEXPERIMENT([]string{"GOEXPERIMENT=foo,jsonv2,bar"}, "jsonv2")
		require.Contains(t, got, "GOEXPERIMENT=foo,jsonv2,bar")
		// no second GOEXPERIMENT entry
		count := 0
		for _, kv := range got {
			if len(kv) >= 13 && kv[:13] == "GOEXPERIMENT=" {
				count++
			}
		}
		require.Equal(t, 1, count, "must not duplicate")
	})
	t.Run("replaces empty value", func(t *testing.T) {
		got := appendGOEXPERIMENT([]string{"GOEXPERIMENT="}, "jsonv2")
		require.Contains(t, got, "GOEXPERIMENT=jsonv2")
	})
}

// TestWriteFormatted_PreservesMode pins the JWXMIGRATE-003 fix:
// the rewritten file inherits the original file mode instead of
// being silently re-created at 0o600 (the os.CreateTemp default).
func TestWriteFormatted_PreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows file modes only honor the write bit")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "x.go")
	require.NoError(t, os.WriteFile(path, []byte("package x\n"), 0o644))

	require.NoError(t, writeFormatted(path, []byte("package x\n\nvar Y = 1\n"), nil, false))

	info, err := os.Stat(path)
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o644), info.Mode().Perm(),
		"original file mode 0o644 must be preserved across the atomic rewrite")
}

// TestWriteFormatted_BackupPreservesMode pins the same fix on the
// backup path: the .bak file should inherit the original mode (so a
// 0o600 secret-bearing file produces a 0o600 backup), not the
// hardcoded 0o644 the pre-fix code used.
func TestWriteFormatted_BackupPreservesMode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows file modes only honor the write bit")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "secret.go")
	require.NoError(t, os.WriteFile(path, []byte("package x\n"), 0o600))

	require.NoError(t, writeFormatted(path, []byte("package x\n\nvar Y = 1\n"), nil, true))

	info, err := os.Stat(path + ".bak")
	require.NoError(t, err)
	require.Equal(t, os.FileMode(0o600), info.Mode().Perm(),
		"backup must inherit original mode (0o600), not the previous hardcoded 0o644")
}

// TestFindFixableFiles_SkipsSymlinks pins the JWXMIGRATE-004 fix:
// symbolic links to .go / go.mod files are skipped by the walker
// (they would otherwise be followed and replaced with regular files
// by the atomic rewrite). Skipped paths are reported on errw.
func TestFindFixableFiles_SkipsSymlinks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symlink semantics differ on windows")
	}
	dir := t.TempDir()

	regular := filepath.Join(dir, "real.go")
	require.NoError(t, os.WriteFile(regular, []byte("package x\n"), 0o644))

	target := filepath.Join(dir, "target.go")
	require.NoError(t, os.WriteFile(target, []byte("package x\n"), 0o644))
	link := filepath.Join(dir, "linked.go")
	require.NoError(t, os.Symlink(target, link))

	var errw bytes.Buffer
	files, err := findFixableFiles(dir, &errw)
	require.NoError(t, err)

	// real.go AND target.go should be picked up; linked.go should NOT.
	var paths []string
	for _, f := range files {
		paths = append(paths, filepath.Base(f))
	}
	require.Contains(t, paths, "real.go")
	require.Contains(t, paths, "target.go")
	require.NotContains(t, paths, "linked.go",
		"symlink must be skipped to avoid replacing it with a regular file")

	require.Contains(t, errw.String(), "linked.go",
		"skipped symlink should be logged so the user sees what was passed over")
	require.Contains(t, errw.String(), "skipped symlink")
}

// TestFixDeleteStatement_PreservesContext pins the JWXMIGRATE-005
// fix: kindRemoved+mechanical deletes are refused when the matched
// call sits in a context where deleting the entire statement would
// take other code with it (named LHS, multi-RHS, or nested call
// position). Bare-call statements and `_ = call(...)` patterns are
// still safely deleted.
func TestFixDeleteStatement_PreservesContext(t *testing.T) {
	cases := []struct {
		name     string
		src      string
		callExpr string // text of the matched call to find in source
		wantNil  bool   // true: refuse delete; false: produce a delete edit
	}{
		{
			name:     "bare call statement is deleted",
			src:      "package x\nfunc F() {\n\tjwt.WithFS(fsys)\n}\n",
			callExpr: "jwt.WithFS(fsys)",
			wantNil:  false,
		},
		{
			name:     "blank-LHS assign is deleted",
			src:      "package x\nfunc F() {\n\t_ = jwt.WithFS(fsys)\n}\n",
			callExpr: "jwt.WithFS(fsys)",
			wantNil:  false,
		},
		{
			name:     "named-LHS assign is refused",
			src:      "package x\nfunc F() {\n\topt := jwt.WithFS(fsys)\n\t_ = opt\n}\n",
			callExpr: "jwt.WithFS(fsys)",
			wantNil:  true,
		},
		{
			name:     "nested call in another expression is refused",
			src:      "package x\nfunc F() {\n\tfoo(jwt.WithFS(fsys), other)\n}\n",
			callExpr: "jwt.WithFS(fsys)",
			wantNil:  true,
		},
		{
			name:     "return value position is refused",
			src:      "package x\nfunc F() any {\n\treturn jwt.WithFS(fsys)\n}\n",
			callExpr: "jwt.WithFS(fsys)",
			wantNil:  true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fset := token.NewFileSet()
			f, err := parser.ParseFile(fset, "x.go", tc.src, 0)
			require.NoError(t, err)

			// Locate the matched call expression by visiting all
			// CallExprs and slicing the source text by token position.
			src := []byte(tc.src)
			var match *ast.CallExpr
			ast.Inspect(f, func(n ast.Node) bool {
				if call, ok := n.(*ast.CallExpr); ok {
					start := fset.Position(call.Pos()).Offset
					end := fset.Position(call.End()).Offset
					if string(src[start:end]) == tc.callExpr {
						match = call
						return false
					}
				}
				return true
			})
			require.NotNil(t, match, "could not find call expression %q in source", tc.callExpr)

			// Use the production stmtOf builder so this test
			// exercises fixDeleteStatement against the same input
			// shape it sees at runtime (only the immediate
			// ExprStmt.X / AssignStmt.Rhs[i] mappings, no nested
			// CallExpr coverage — that's what makes "nested call in
			// another expression" naturally fall through to the
			// !ok branch).
			stmtOf := buildStmtMap(f)

			byteOffset := func(p token.Pos) int { return fset.Position(p).Offset }
			edits := fixDeleteStatement(match, byteOffset, stmtOf)
			if tc.wantNil {
				require.Nil(t, edits, "context-loss case must refuse the delete")
			} else {
				require.NotNil(t, edits, "safe case must produce a delete edit")
				require.Len(t, edits, 1)
			}
		})
	}
}

