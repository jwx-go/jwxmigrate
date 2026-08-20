package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// writeModule lays down a single-package module whose go.mod declares the
// given go directive and jwx requirement.
func writeModule(t *testing.T, goDirective, jwxVersion, src string) string {
	t.Helper()

	dir := t.TempDir()
	gomod := "module example.com/consumer\n\ngo " + goDirective + "\n"
	if jwxVersion != "" {
		gomod += "\nrequire github.com/lestrrat-go/jwx/v4 " + jwxVersion + "\n"
	}
	require.NoError(t, os.WriteFile(filepath.Join(dir, goModFilename), []byte(gomod), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(dir, "main.go"), []byte(src), 0o644))
	return dir
}

const jwkParseSource = `package consumer

import "github.com/lestrrat-go/jwx/v3/jwk"

func load(data []byte) {
	_, _ = jwk.Parse(data)
}
`

func floorFindingsIn(result *CheckResult) []Finding {
	var out []Finding
	for _, f := range result.Findings {
		if f.RequiresModule != "" {
			out = append(out, f)
		}
	}
	return out
}

func TestVersionFloorDiagnostic(t *testing.T) {
	rules, err := loadRules(migrationV3ToV4)
	require.NoError(t, err)

	t.Run("reports when go.mod is below a fired rule's floor", func(t *testing.T) {
		dir := writeModule(t, "1.26.0", "v4.1.0", jwkParseSource)

		result, err := Check(dir, rules, CheckOptions{})
		require.NoError(t, err)

		floors := floorFindingsIn(result)
		require.Len(t, floors, 1, "expected exactly one version-floor finding")

		f := floors[0]
		require.Equal(t, "jwk-parse-retains-unsupported-keys", f.RuleID)
		require.Equal(t, "github.com/lestrrat-go/jwx/v4", f.RequiresModule)
		require.Equal(t, "v4.2.0", f.RequiresVersion)
		require.Equal(t, "v4.1.0", f.CurrentVersion)
		require.Equal(t, goModFilename, filepath.Base(f.File))
		require.Positive(t, f.Line, "should point at the require line")
	})

	t.Run("stays quiet when go.mod already satisfies the floor", func(t *testing.T) {
		dir := writeModule(t, "1.26.0", "v4.2.0", jwkParseSource)

		result, err := Check(dir, rules, CheckOptions{})
		require.NoError(t, err)
		require.Empty(t, floorFindingsIn(result))
	})

	t.Run("stays quiet when the module is not required at all", func(t *testing.T) {
		// Nothing to raise: adding the requirement is the rewrite's job.
		dir := writeModule(t, "1.26.0", "", jwkParseSource)

		result, err := Check(dir, rules, CheckOptions{})
		require.NoError(t, err)
		require.Empty(t, floorFindingsIn(result))
	})

	t.Run("an unmet floor makes the run exit non-zero", func(t *testing.T) {
		dir := writeModule(t, "1.26.0", "v4.1.0", jwkParseSource)

		result, err := Check(dir, rules, CheckOptions{})
		require.NoError(t, err)
		// runCheck returns 1 whenever Total > 0, and floor findings are
		// counted like any other, so this is what drives the exit code.
		require.Positive(t, result.Total)
	})
}

func TestVersionFloorGoGating(t *testing.T) {
	gated := []CompiledRule{{
		Rule: Rule{
			ID:         "needs-go-127",
			Kind:       kindBehavioral,
			Package:    "jwk",
			Mechanical: false,
			Note:       "only actionable on go1.27",
			Requires:   &Requires{Go: "1.27"},
		},
	}}

	src := writeModule(t, "1.26.0", "v4.2.0", jwkParseSource)
	newer := writeModule(t, "1.27.0", "v4.2.0", jwkParseSource)

	finding := func(dir string) []Finding {
		return []Finding{{
			RuleID: "needs-go-127",
			File:   filepath.Join(dir, "main.go"),
			Line:   1,
		}}
	}

	t.Run("suppressed below the floor", func(t *testing.T) {
		require.Empty(t, applyVersionFloors(src, finding(src), gated))
	})

	t.Run("kept at or above the floor", func(t *testing.T) {
		require.Len(t, applyVersionFloors(newer, finding(newer), gated), 1)
	})

	t.Run("suppressed when the file is in no module", func(t *testing.T) {
		orphan := []Finding{{RuleID: "needs-go-127", File: string(filepath.Separator) + "nonexistent-dir-xyz/main.go", Line: 1}}
		require.Empty(t, applyVersionFloors(t.TempDir(), orphan, gated))
	})
}
