package main

import (
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// mldsaConsumer is a project shaped the way a Go 1.26 ML-DSA user's code is:
// a blank import of the extension for its registration side effect, plus raw
// keys from filippo.io/mldsa.
const mldsaConsumer = `package consumer

import (
	"filippo.io/mldsa"

	"github.com/lestrrat-go/jwx/v4/jwa"
	_ "github.com/jwx-go/mldsa/v4"
)

func generate() {
	var params *mldsa.Parameters = mldsa.MLDSA44()
	_, _ = mldsa.GenerateKey(params)
	_ = jwa.RS256()
}
`

func writeMLDSAModule(t *testing.T, goDirective string) string {
	t.Helper()

	dir := t.TempDir()
	gomod := "module example.com/consumer\n\ngo " + goDirective + `

require (
	filippo.io/mldsa v0.0.0-20260215214346-43d0283efc3e
	github.com/jwx-go/mldsa/v4 v4.0.4
	github.com/lestrrat-go/jwx/v4 v4.4.0
)
`
	require.NoError(t, os.WriteFile(filepath.Join(dir, goModFilename), []byte(gomod), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(mldsaConsumer), 0o644))
	return dir
}

func TestMLDSAAbsorbedIntoCore(t *testing.T) {
	t.Run("reported on go1.27", func(t *testing.T) {
		rules, err := loadRules(migrationV3ToV4)
		require.NoError(t, err)

		dir := writeMLDSAModule(t, "1.27.0")
		result, err := Check(dir, rules, CheckOptions{})
		require.NoError(t, err)

		fired := map[string]bool{}
		for _, f := range result.Findings {
			fired[f.RuleID] = true
		}
		require.True(t, fired["mldsa-extension-absorbed"], "extension import should be reported")
		require.True(t, fired["mldsa-key-package-to-stdlib"], "filippo import should be reported")
		require.True(t, fired["mldsa-parameters-value-type"], "pointer Parameters should be reported")
	})

	t.Run("silent on go1.26", func(t *testing.T) {
		// crypto/mldsa does not exist there, so none of this is actionable.
		rules, err := loadRules(migrationV3ToV4)
		require.NoError(t, err)

		dir := writeMLDSAModule(t, "1.26.0")
		result, err := Check(dir, rules, CheckOptions{})
		require.NoError(t, err)

		for _, f := range result.Findings {
			require.NotContains(t, f.RuleID, "mldsa-",
				"ML-DSA rules must be suppressed below go1.27")
		}
	})
}

func TestMLDSAFixRewritesEverything(t *testing.T) {
	prev := runGoModTidy
	tidied := false
	runGoModTidy = func(string, io.Writer, io.Writer) error { tidied = true; return nil }
	t.Cleanup(func() { runGoModTidy = prev })

	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	dir := writeMLDSAModule(t, "1.27.0")
	files, err := findFixableFiles(dir, io.Discard)
	require.NoError(t, err)
	fixFiles(files, rules, FixOptions{}, io.Discard, io.Discard)

	src, err := os.ReadFile(filepath.Join(dir, "main.go"))
	require.NoError(t, err)
	got := string(src)

	require.NotContains(t, got, "jwx-go/mldsa", "the extension import should be gone")
	require.Contains(t, got, `"crypto/mldsa"`, "the key package should be the stdlib one")
	require.NotContains(t, got, "filippo.io/mldsa", "the filippo import should be gone")
	require.NotContains(t, got, "*mldsa.Parameters", "Parameters is a value in crypto/mldsa")
	require.Contains(t, got, "var params mldsa.Parameters", "the value form should remain")

	// Untouched code must survive the rewrite.
	require.Contains(t, got, "jwa.RS256()")
	require.Contains(t, got, "mldsa.GenerateKey(params)")

	gomod, err := os.ReadFile(filepath.Join(dir, goModFilename))
	require.NoError(t, err)
	require.NotContains(t, string(gomod), "github.com/jwx-go/mldsa/v4",
		"the absorbed extension should be dropped from go.mod")
	require.True(t, tidied, "go mod tidy should run so filippo falls out too")
}

// namedExtensionConsumer imports the extension under a name and calls into
// it, which is how real code that predates native ML-DSA is written.
const namedExtensionConsumer = `package consumer

import (
	jwxmldsa "github.com/jwx-go/mldsa/v4"
	"github.com/lestrrat-go/jwx/v4/jws"
)

func sign(payload []byte, key any) ([]byte, error) {
	return jws.Sign(payload, jws.WithKey(jwxmldsa.MLDSA65(), key))
}
`

// unknownSymbolConsumer calls a symbol the rule does not know how to move.
// InteropMode has no home in jwa, so nothing can be rewritten here.
const unknownSymbolConsumer = `package consumer

import jwxmldsa "github.com/jwx-go/mldsa/v4"

func interop() bool {
	return jwxmldsa.InteropMode()
}
`

func fixMLDSAModule(t *testing.T, source string) (string, string) {
	t.Helper()

	prev := runGoModTidy
	runGoModTidy = func(string, io.Writer, io.Writer) error { return nil }
	t.Cleanup(func() { runGoModTidy = prev })

	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, goModFilename), []byte(
		"module example.com/consumer\n\ngo 1.27\n\nrequire (\n\tgithub.com/jwx-go/mldsa/v4 v4.0.5\n\tgithub.com/lestrrat-go/jwx/v4 v4.4.0\n)\n",
	), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(source), 0o644))

	files, err := findFixableFiles(dir, io.Discard)
	require.NoError(t, err)
	fixFiles(files, rules, FixOptions{}, io.Discard, io.Discard)

	src, err := os.ReadFile(filepath.Join(dir, "main.go"))
	require.NoError(t, err)
	gomod, err := os.ReadFile(filepath.Join(dir, goModFilename))
	require.NoError(t, err)
	return string(src), string(gomod)
}

func TestNamedExtensionImportIsRewritten(t *testing.T) {
	// Every usage moves to jwa, so the import can go with them. Verified
	// against the real jwx-go/examples file this mirrors, which compiles and
	// passes on Go 1.27 after the same rewrite.
	got, gomod := fixMLDSAModule(t, namedExtensionConsumer)

	require.NotContains(t, got, "jwx-go/mldsa", "the extension import should be gone")
	require.NotContains(t, got, "jwxmldsa.", "no usage may reference the dropped import")
	require.Contains(t, got, "jwa.MLDSA65()", "the call should move to jwa")
	require.Contains(t, got, `"github.com/lestrrat-go/jwx/v4/jwa"`, "jwa must be imported")
	require.NotContains(t, gomod, "github.com/jwx-go/mldsa/v4", "the require should be dropped")
}

func TestUnknownExtensionSymbolBlocksRemoval(t *testing.T) {
	// Deleting the import would leave jwxmldsa.InteropMode undefined. The
	// rule still reports; it just must not apply.
	got, gomod := fixMLDSAModule(t, unknownSymbolConsumer)

	require.Contains(t, got, `jwxmldsa "github.com/jwx-go/mldsa/v4"`,
		"an import with an unmovable usage must survive --fix")
	require.Contains(t, got, "jwxmldsa.InteropMode()")
	require.Contains(t, gomod, "github.com/jwx-go/mldsa/v4",
		"the require must stay while the import does")
}

func TestStdlibTargetIsNeverRequired(t *testing.T) {
	// crypto/mldsa is in the standard library. Writing it as a require
	// produces a go.mod that will not parse at all.
	rules := []CompiledRule{{
		Rule: Rule{
			ID:         "to-stdlib",
			Kind:       kindImportChange,
			Package:    packageAll,
			Mechanical: true,
			V3:         "filippo.io/mldsa",
			V4:         "crypto/mldsa",
		},
	}}
	require.Empty(t, importRewriteRules(rules),
		"a stdlib target has no module to require")

	require.True(t, isStdlibImportPath("crypto/mldsa"))
	require.True(t, isStdlibImportPath("encoding/json/v2"))
	require.False(t, isStdlibImportPath("github.com/jwx-go/mldsa/v4"))
	require.False(t, isStdlibImportPath("filippo.io/mldsa"))
}
