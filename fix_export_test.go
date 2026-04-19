package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubJWKPackage is a tiny v3-shaped jwk package: just enough surface for
// the type checker to resolve `jwk.Export(k, &dst)` calls and report dst's
// type back to fixJWKExportGeneric. Real v3 has many more types; we only
// need Key (interface) and Export (function) to drive the rewrite test.
const stubJWKPackage = `package jwk

type Key interface {
	dummy()
}

func Export(key Key, dst any) error {
	_, _ = key, dst
	return nil
}
`

// withStubJWKModule materializes a tempdir containing a go.mod that
// declares an import path of github.com/lestrrat-go/jwx/v3, plus the
// stub jwk subpackage. parseGoFileTyped will pick this up as the v3
// jwk package when it loads files in this module — the import map
// keys on sourceImportPrefix, and the type checker just needs *some*
// package at that path with the named types.
//
// Returns the module root directory; caller materializes its own
// main.go inside.
func withStubJWKModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module github.com/lestrrat-go/jwx/v3\n\ngo 1.21\n"), 0o644))

	jwkDir := filepath.Join(root, "jwk")
	require.NoError(t, os.MkdirAll(jwkDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(jwkDir, "jwk.go"),
		[]byte(stubJWKPackage), 0o644))

	return root
}

// runExportFixOnFile materializes src as caller/main.go inside root and
// runs FixFile against it. Returns the post-fix file contents.
func runExportFixOnFile(t *testing.T, root, src string) string {
	t.Helper()
	callerDir := filepath.Join(root, "caller")
	require.NoError(t, os.MkdirAll(callerDir, 0o755))
	mainPath := filepath.Join(callerDir, "main.go")
	require.NoError(t, os.WriteFile(mainPath, []byte(src), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	_, err = FixFile(mainPath, rules)
	require.NoError(t, err)

	out, err := os.ReadFile(mainPath)
	require.NoError(t, err)
	return string(out)
}

// TestFixExport_AnyDst pins the simplest safe rewrite: dst is `any`, so
// T = any and the return value can be assigned back to dst without any
// downstream type changes.
func TestFixExport_AnyDst(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import "github.com/lestrrat-go/jwx/v3/jwk"

func ExampleAny(k jwk.Key) (any, error) {
	var raw any
	err := jwk.Export(k, &raw)
	return raw, err
}
`)
	require.Contains(t, got, "raw, err := jwk.Export[any](k)",
		"any-typed dst should rewrite to Export[any] preserving dst's binding")
	require.NotContains(t, got, "jwk.Export(k, &raw)",
		"original v3 call shape must be gone")
}

// TestFixExport_KeyDst covers the jwk.Key interface case, which is the
// other interface-typed dst the fixer must accept.
func TestFixExport_KeyDst(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import "github.com/lestrrat-go/jwx/v3/jwk"

func ExampleKey(k jwk.Key) (jwk.Key, error) {
	var raw jwk.Key
	err := jwk.Export(k, &raw)
	return raw, err
}
`)
	require.Contains(t, got, "raw, err := jwk.Export[jwk.Key](k)",
		"jwk.Key dst should rewrite to Export[jwk.Key]")
}

// TestFixExport_PointerDst covers the second safe case: dst is a pointer
// type (`*rsa.PrivateKey`), so T = *rsa.PrivateKey returns the same
// dynamic type that v3 wrote through the pointer.
func TestFixExport_PointerDst(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import (
	"crypto/rsa"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

func ExamplePointer(k jwk.Key) (*rsa.PrivateKey, error) {
	var raw *rsa.PrivateKey
	err := jwk.Export(k, &raw)
	return raw, err
}
`)
	require.Contains(t, got, "raw, err := jwk.Export[*rsa.PrivateKey](k)",
		"pointer-typed dst should rewrite to Export[*rsa.PrivateKey]")
}

// TestFixExport_ValueDstSkipped pins the unsafe case: dst is a value
// type (`rsa.PrivateKey`). The mechanical equivalent would need a temp
// pointer + an explicit deref across statement boundaries; we leave it
// for the user. The original call must remain on disk and the rule
// must surface as a remaining-issues note.
func TestFixExport_ValueDstSkipped(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import (
	"crypto/rsa"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

func ExampleValue(k jwk.Key) (rsa.PrivateKey, error) {
	var raw rsa.PrivateKey
	err := jwk.Export(k, &raw)
	return raw, err
}
`)
	require.Contains(t, got, "jwk.Export(k, &raw)",
		"value-typed dst must NOT be auto-rewritten")
}

// TestFixExport_IfInitNoElse handles the bare if-init form by inserting
// a temp into the init's LHS and an else block that writes the temp
// back to the user's dst. The naive rewrite (`if dst, err := …`) would
// shadow the outer dst and leave reads after the if returning zero;
// using a fresh tmp name dodges that. The else (rather than splitting
// into three statements) preserves v3's "leave dst alone on error"
// semantics for callers whose body doesn't unconditionally exit.
func TestFixExport_IfInitNoElse(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import "github.com/lestrrat-go/jwx/v3/jwk"

func ExampleIfInit(k jwk.Key) error {
	var raw any
	if err := jwk.Export(k, &raw); err != nil {
		return err
	}
	_ = raw
	return nil
}
`)
	require.Contains(t, got, "if rawV4Exported, err := jwk.Export[any](k); err != nil",
		"if-init must use a fresh temp name for the typed Export call")
	require.Contains(t, got, "else {\n\t\traw = rawV4Exported\n\t}",
		"else block must write the temp back to the user's dst")
	require.NotContains(t, got, "if raw, err :=",
		"shadowing rewrite must never appear in output")
}

// TestFixExport_IfInitWithExistingElse pins that the if-init rewrite
// declines to merge into a pre-existing else clause. Merging an
// assignment into a user-authored else (especially else-if chains) is
// more reshape than this fixer is willing to do; the call gets reported
// on the remaining-issues list so the developer can decide.
func TestFixExport_IfInitWithExistingElse(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import "github.com/lestrrat-go/jwx/v3/jwk"

func ExampleIfInitElse(k jwk.Key) error {
	var raw any
	if err := jwk.Export(k, &raw); err != nil {
		return err
	} else {
		_ = raw
	}
	return nil
}
`)
	require.Contains(t, got, "if err := jwk.Export(k, &raw)",
		"if-init with existing else must not be auto-rewritten")
}

// TestFixExport_IfInitTempNameCollision pins that exportTempName picks
// a non-colliding identifier when the obvious choice (`<dst>V4Exported`)
// is already taken in the surrounding function scope.
func TestFixExport_IfInitTempNameCollision(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import "github.com/lestrrat-go/jwx/v3/jwk"

func ExampleCollision(k jwk.Key) error {
	rawV4Exported := "already taken"
	_ = rawV4Exported
	var raw any
	if err := jwk.Export(k, &raw); err != nil {
		return err
	}
	_ = raw
	return nil
}
`)
	require.Contains(t, got, "if rawV4Exported2, err := jwk.Export[any](k)",
		"colliding base name should fall through to a numeric suffix")
}

// TestFixExport_BareCallExprAssignsBlank covers the rarer pattern where
// the call's return value is discarded (`jwk.Export(k, &raw)` with no
// receiver). The rewrite emits `raw, _ = jwk.Export[T](k)` so dst still
// gets written.
func TestFixExport_BareCallExpr(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import "github.com/lestrrat-go/jwx/v3/jwk"

func ExampleBare(k jwk.Key) any {
	var raw any
	jwk.Export(k, &raw)
	return raw
}
`)
	require.Contains(t, got, "raw, _ = jwk.Export[any](k)",
		"bare call should rewrite with blank err receiver")
}

// TestFixExport_AliasedJWKImport pins that the fixer threads the file's
// local import name through into the rewritten call — `myjwk.Export[T]`,
// not `jwk.Export[T]`.
func TestFixExport_AliasedJWKImport(t *testing.T) {
	root := withStubJWKModule(t)
	got := runExportFixOnFile(t, root, `package caller

import myjwk "github.com/lestrrat-go/jwx/v3/jwk"

func ExampleAlias(k myjwk.Key) (any, error) {
	var raw any
	err := myjwk.Export(k, &raw)
	return raw, err
}
`)
	require.Contains(t, got, "raw, err := myjwk.Export[any](k)",
		"aliased import should produce alias-qualified call")
}
