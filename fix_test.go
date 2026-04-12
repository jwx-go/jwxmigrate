package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestFixImportChange(t *testing.T) {
	src := `package example

import "github.com/lestrrat-go/jwx/v3/jwt"

func f() {}
`
	result := fixAndRead(t, src)
	require.Contains(t, result, `"github.com/lestrrat-go/jwx/v4/jwt"`)
	require.NotContains(t, result, "v3")
}

func TestFixRename(t *testing.T) {
	src := `package example

import "github.com/lestrrat-go/jwx/v3/jws"

func f() {
	var s jws.Signer2
	_ = s
}
`
	result := fixAndRead(t, src)
	require.Contains(t, result, "jws.Signer")
	require.NotContains(t, result, "Signer2")
}

func TestFixDecoderSettingsRename(t *testing.T) {
	src := `package example

import "github.com/lestrrat-go/jwx/v3"

func f() {
	jwx.DecoderSettings(jwx.WithUseNumber(true))
	println("keep this")
}
`
	result := fixAndRead(t, src)
	require.Contains(t, result, "jwx.Settings(jwx.WithUseNumber(true))")
	require.NotContains(t, result, "DecoderSettings")
	require.Contains(t, result, `println("keep this")`)
}

func TestFixReadFileToParseFS(t *testing.T) {
	src := `package example

import "github.com/lestrrat-go/jwx/v3/jwt"

func f() {
	tok, _ := jwt.ReadFile("token.jwt")
	_ = tok
}
`
	result := fixAndRead(t, src)
	require.Contains(t, result, "jwt.ParseFS")
	require.NotContains(t, result, "ReadFile")
}

func TestFixTypeReplacement(t *testing.T) {
	src := `package example

import "github.com/lestrrat-go/jwx/v3/jwt"

type myOpt jwt.ReadFileOption
`
	result := fixAndRead(t, src)
	require.Contains(t, result, "ParseOption")
	require.NotContains(t, result, "ReadFileOption")
}

func TestFixNoChanges(t *testing.T) {
	src := `package example

import "fmt"

func f() {
	fmt.Println("hello")
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "clean.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := FixFile(path, rules)
	require.NoError(t, err)
	require.Nil(t, result, "should return nil when no fixes apply")
}

func TestFixPreservesNonMechanical(t *testing.T) {
	src := `package example

import "github.com/lestrrat-go/jwx/v3/jwk"

func f() {
	key, _ := jwk.Import(rawKey)
	_ = key
}
`
	result := fixAndRead(t, src)
	// jwk.Import is mechanical: false — should not be changed.
	require.Contains(t, result, "jwk.Import")
	// But the import path should be fixed.
	require.Contains(t, result, "v4/jwk")
}

// fixAndRead writes src to a temp file, runs FixFile, and returns the result.
func fixAndRead(t *testing.T, src string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "test.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	_, err = FixFile(path, rules)
	require.NoError(t, err)

	out, err := os.ReadFile(path)
	require.NoError(t, err)
	return strings.TrimSpace(string(out))
}
