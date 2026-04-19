package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubJWTPackage provides the minimum v3-jwt surface the type checker
// needs to see tok.Get("name", &dst) as a method call on a v3 Token.
// fixGetToField reads the receiver type to derive recvPkgLocal, so the
// Token type's package import path must match sourceImportPrefix; the
// stub module at withStubJWTModule below owns that.
const stubJWTPackage = `package jwt

type Token interface {
	Get(name string, dst any) error
}

const SubjectKey = "sub"
`

// stubJWAPackage exists so TransitiveImport's caller file can import
// v3 jwx for *something* (the scanner skips files with no v3 imports),
// while the get-to-field rewrite targets a receiver whose package
// comes in transitively.
const stubJWAPackage = `package jwa

type SignatureAlgorithm string
`

func withStubJWTModule(t *testing.T) string {
	t.Helper()
	root := t.TempDir()

	require.NoError(t, os.WriteFile(filepath.Join(root, "go.mod"),
		[]byte("module github.com/lestrrat-go/jwx/v3\n\ngo 1.21\n"), 0o644))

	jwtDir := filepath.Join(root, "jwt")
	require.NoError(t, os.MkdirAll(jwtDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(jwtDir, "jwt.go"),
		[]byte(stubJWTPackage), 0o644))

	jwaDir := filepath.Join(root, "jwa")
	require.NoError(t, os.MkdirAll(jwaDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(jwaDir, "jwa.go"),
		[]byte(stubJWAPackage), 0o644))

	return root
}

func runGetToFieldFixOnFile(t *testing.T, root, src string) string {
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

// TestFixGetToField_IfInit pins the OPA-style if-init rewrite shape.
// Before this fix, fixGetToField only handled bare ExprStmt and simple
// AssignStmt shapes; the if-init form fell through to fixSignatureChange
// which naively renamed .Get → .Field, producing code that didn't
// compile because v4's Field signature differs from v3's Get.
func TestFixGetToField_IfInit(t *testing.T) {
	root := withStubJWTModule(t)
	got := runGetToFieldFixOnFile(t, root, `package caller

import "github.com/lestrrat-go/jwx/v3/jwt"

func Read(tok jwt.Token) string {
	var gotKid string
	if err := tok.Get("keyid", &gotKid); err != nil {
		panic(err)
	}
	return gotKid
}
`)
	require.Contains(t, got, "jwt.Get[string](tok, \"keyid\")",
		"if-init should rewrite to the generic free function form")
	require.Contains(t, got, "gotKid = gotKidV4",
		"else block should copy the temp back to the outer dst")
	require.NotContains(t, got, "tok.Field(",
		"naive .Get → .Field rename must be blocked — v4 Field's signature differs")
	require.NotContains(t, got, "tok.Get(\"keyid\", &gotKid)",
		"original v3 call shape must be gone")
}

// TestFixGetToField_TransitiveImport covers OPA's pattern: a file
// references jwt.Token only through a helper's return type, never
// importing jwt directly. Before this fix, fixGetToField bailed when
// the receiver's package wasn't in V3Imports, falling through to the
// naive .Get → .Field rename. Now it derives the local name from
// TypesInfo and queues an import injection so the rewritten call
// binds against a real imported package.
func TestFixGetToField_TransitiveImport(t *testing.T) {
	root := withStubJWTModule(t)

	// The caller package has its own helper that returns jwt.Token;
	// main.go itself never imports jwt. The stub jwt package is
	// reachable via the helper's return type in TypesInfo.
	callerDir := filepath.Join(root, "caller")
	require.NoError(t, os.MkdirAll(callerDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(callerDir, "helper.go"),
		[]byte(`package caller

import "github.com/lestrrat-go/jwx/v3/jwt"

func MakeToken() jwt.Token { return nil }
`), 0o644))

	// main.go imports v3 jwa (so the scanner doesn't skip it for
	// lacking v3 imports entirely), but never imports jwt directly.
	// jwt.Token is reachable only via MakeToken's return type.
	mainSrc := `package caller

import (
	"github.com/lestrrat-go/jwx/v3/jwa"
)

var _ jwa.SignatureAlgorithm

func Read() string {
	tok := MakeToken()
	var kid string
	if err := tok.Get("kid", &kid); err != nil {
		panic(err)
	}
	return kid
}
`
	mainPath := filepath.Join(callerDir, "main.go")
	require.NoError(t, os.WriteFile(mainPath, []byte(mainSrc), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	_, err = FixFile(mainPath, rules)
	require.NoError(t, err)

	out, err := os.ReadFile(mainPath)
	require.NoError(t, err)
	got := string(out)

	require.Contains(t, got, "jwt.Get[string](tok, \"kid\")",
		"reshape should use derived jwt local name even without a direct import")
	require.Contains(t, got, "\"github.com/lestrrat-go/jwx/v4/jwt\"",
		"missing jwt import should be injected so the rewritten call binds")
	require.NotContains(t, got, "tok.Field(",
		"naive rename must not fire")
}

// TestFixGetToField_NoNaiveRename_OnUnhandledShape pins the safety
// guarantee: even when fixGetToField can't synthesize a reshape (here,
// dst is an unaddressable map-index expression that slips past the
// TypesInfo lookup without a known type), the fallthrough to
// fixSignatureChange's naive .Get → .Field rename stays blocked.
func TestFixGetToField_NoNaiveRename_OnUnhandledShape(t *testing.T) {
	root := withStubJWTModule(t)
	got := runGetToFieldFixOnFile(t, root, `package caller

import "github.com/lestrrat-go/jwx/v3/jwt"

func Read(tok jwt.Token) {
	var sub string
	if err := tok.Get("sub", &sub); err == nil {
		_ = sub
	} else {
		panic(err)
	}
}
`)
	// The if-init here already has an else clause, which
	// fixGetToFieldIfInit refuses to reshape (merging into an
	// existing else is out of scope). The call stays as-is.
	require.Contains(t, got, "tok.Get(\"sub\", &sub)",
		"call with existing else should remain untouched")
	require.NotContains(t, got, "tok.Field(",
		"naive rename must be blocked when reshape is skipped")
}
