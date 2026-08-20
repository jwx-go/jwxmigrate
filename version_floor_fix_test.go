package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// fixInPlace runs the batch fixer over a module directory with `go mod tidy`
// stubbed out, and returns the resulting go.mod.
func fixInPlace(t *testing.T, dir string) string {
	t.Helper()

	prev := runGoModTidy
	runGoModTidy = func(string, io.Writer, io.Writer) error { return nil }
	t.Cleanup(func() { runGoModTidy = prev })

	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	files, err := findFixableFiles(dir, io.Discard)
	require.NoError(t, err)

	fixFiles(files, rules, FixOptions{}, io.Discard, io.Discard)

	got, err := os.ReadFile(filepath.Join(dir, goModFilename))
	require.NoError(t, err)
	return string(got)
}

func TestFixRaisesVersionToRuleFloor(t *testing.T) {
	t.Run("path swap pins the floor a fired rule needs", func(t *testing.T) {
		// The v3 import makes the swap fire; jwk.Parse makes the
		// v4.2.0 rule fire. The result must be v4.2.0, not the baseline.
		dir := writeModule(t, "1.26.0", "", jwkParseSource)
		require.NoError(t, os.WriteFile(filepath.Join(dir, goModFilename), []byte(
			"module example.com/consumer\n\ngo 1.26.0\n\nrequire github.com/lestrrat-go/jwx/v3 v3.0.13\n",
		), 0o644))

		got := fixInPlace(t, dir)
		require.Contains(t, got, "github.com/lestrrat-go/jwx/v4 v4.2.0")
		require.NotContains(t, got, "jwx/v3")
	})

	t.Run("raises an existing target require that is too old", func(t *testing.T) {
		// Already on v4, so no path swap happens. The floor still applies.
		src := `package consumer

import "github.com/lestrrat-go/jwx/v4/jwk"

func load(data []byte) {
	_, _ = jwk.Parse(data)
}
`
		dir := writeModule(t, "1.26.0", "v4.1.0", src)

		got := fixInPlace(t, dir)
		require.Contains(t, got, "github.com/lestrrat-go/jwx/v4 v4.2.0")
	})

	t.Run("leaves a require that already satisfies the floor", func(t *testing.T) {
		src := `package consumer

import "github.com/lestrrat-go/jwx/v4/jwk"

func load(data []byte) {
	_, _ = jwk.Parse(data)
}
`
		dir := writeModule(t, "1.26.0", "v4.3.0", src)

		got := fixInPlace(t, dir)
		require.Contains(t, got, "github.com/lestrrat-go/jwx/v4 v4.3.0",
			"a newer version must not be downgraded to the floor")
	})

	t.Run("baseline applies when no fired rule declares more", func(t *testing.T) {
		// jwt.Parse has no version floor, so only the baseline applies.
		src := `package consumer

import "github.com/lestrrat-go/jwx/v3/jwt"

func load(data []byte) {
	_, _ = jwt.Parse(data)
}
`
		dir := writeModule(t, "1.26.0", "", src)
		require.NoError(t, os.WriteFile(filepath.Join(dir, goModFilename), []byte(
			"module example.com/consumer\n\ngo 1.26.0\n\nrequire github.com/lestrrat-go/jwx/v3 v3.0.13\n",
		), 0o644))

		got := fixInPlace(t, dir)
		require.Contains(t, got, "github.com/lestrrat-go/jwx/v4 "+targetMinimumVersion)
	})
}

func TestFixAddsCompanionAtDeclaredFloor(t *testing.T) {
	rules := []CompiledRule{{
		Rule: Rule{
			ID:              "cache-moved",
			Kind:            kindMovedToExtension,
			Package:         "jwk",
			ExtensionModule: "github.com/jwx-go/jwkfetch/v4",
			Requires: &Requires{Modules: []ModuleRequirement{
				{Path: "github.com/jwx-go/jwkfetch/v4", Version: "v4.0.2"},
			}},
		},
	}}

	dir := t.TempDir()
	gomod := filepath.Join(dir, goModFilename)
	require.NoError(t, os.WriteFile(gomod, []byte(
		"module example.com/consumer\n\ngo 1.26.0\n\nrequire github.com/lestrrat-go/jwx/v4 v4.2.0\n",
	), 0o644))

	result, err := FixBuildFile(gomod, rules, rules)
	require.NoError(t, err)
	require.NotNil(t, result, "companion requirement should have been added")

	got, err := os.ReadFile(gomod)
	require.NoError(t, err)
	require.Contains(t, string(got), "github.com/jwx-go/jwkfetch/v4 v4.0.2")

	t.Run("not added when the rule did not fire", func(t *testing.T) {
		dir := t.TempDir()
		gomod := filepath.Join(dir, goModFilename)
		require.NoError(t, os.WriteFile(gomod, []byte(
			"module example.com/consumer\n\ngo 1.26.0\n\nrequire github.com/lestrrat-go/jwx/v4 v4.2.0\n",
		), 0o644))

		result, err := FixBuildFile(gomod, rules, nil)
		require.NoError(t, err)
		require.Nil(t, result, "no rule fired, so nothing should be written")

		got, err := os.ReadFile(gomod)
		require.NoError(t, err)
		require.NotContains(t, string(got), "jwkfetch")
	})
}
