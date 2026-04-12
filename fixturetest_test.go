package main

import (
	"bytes"
	"flag"
	"fmt"
	"go/format"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// updateGolden, when set, causes runFixture to overwrite want_*.txt and
// want_fix/ files instead of asserting against them.
var updateGolden = flag.Bool("update", false, "update golden files in testdata/rules and testdata/edge")

// fixtureConfig is an optional per-fixture config.yaml.
//
// Defaults: migration=v3-to-v4, no filters, fix enabled if want_fix/ exists.
type fixtureConfig struct {
	Migration      string `yaml:"migration"`
	MechanicalOnly bool   `yaml:"mechanical_only"`
	RuleID         string `yaml:"rule_id"`
	// SkipFix disables the fix-and-compare phase even if want_fix/ is present.
	SkipFix bool `yaml:"skip_fix"`
}

func loadFixtureConfig(fixtureDir string) (*fixtureConfig, error) {
	cfg := &fixtureConfig{Migration: "v3-to-v4"}
	path := filepath.Join(fixtureDir, "config.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if cfg.Migration == "" {
		cfg.Migration = "v3-to-v4"
	}
	return cfg, nil
}

// runFixture executes a single fixture directory.
//
// Fixture layout:
//
//	<fixtureDir>/
//	  input/                 (required) Go source files
//	  want_check.txt         (optional) expected FormatText output
//	  want_fix/              (optional) expected file contents after FixFile
//	  config.yaml            (optional) migration + filters
//
// When the global -update flag is set, want_check.txt and want_fix/ are
// overwritten instead of compared.
func runFixture(t *testing.T, fixtureDir string) {
	t.Helper()

	cfg, err := loadFixtureConfig(fixtureDir)
	require.NoError(t, err)

	inputDir := filepath.Join(fixtureDir, "input")
	info, err := os.Stat(inputDir)
	require.NoError(t, err, "fixture %s missing input/ dir", fixtureDir)
	require.True(t, info.IsDir(), "fixture %s: input/ is not a directory", fixtureDir)

	rules, err := loadRules(cfg.Migration)
	require.NoError(t, err)

	// Work on a copy so the fixture is never modified.
	workDir := copyTree(t, inputDir)

	checkOpts := CheckOptions{
		MechanicalOnly: cfg.MechanicalOnly,
		RuleID:         cfg.RuleID,
	}

	result, err := Check(workDir, rules, checkOpts)
	require.NoError(t, err)

	gotCheck := renderCheckGolden(result)

	wantCheckPath := filepath.Join(fixtureDir, "want_check.txt")
	if *updateGolden {
		require.NoError(t, os.WriteFile(wantCheckPath, []byte(gotCheck), 0o644))
	} else if data, err := os.ReadFile(wantCheckPath); err == nil {
		require.Equal(t, string(data), gotCheck, "check output mismatch for %s (run with -update to regenerate)", fixtureDir)
	} else if !os.IsNotExist(err) {
		t.Fatalf("read %s: %v", wantCheckPath, err)
	}

	wantFixDir := filepath.Join(fixtureDir, "want_fix")
	wantFixInfo, wantFixErr := os.Stat(wantFixDir)
	hasWantFix := wantFixErr == nil && wantFixInfo.IsDir()

	if cfg.SkipFix || (!hasWantFix && !*updateGolden) {
		return
	}

	// Apply fixes to every .go file in the work dir.
	inputFiles := listGoFiles(t, workDir)
	for _, f := range inputFiles {
		_, fixErr := FixFile(f, rules)
		require.NoError(t, fixErr, "FixFile %s", f)
	}

	// gofmt round-trip: each fixed file must equal its own format.Source.
	// FixFile already calls format.Source, so this is a consistency check.
	for _, f := range inputFiles {
		assertGofmtStable(t, f)
	}

	// Idempotency: running fix a second time must not change any file.
	for _, f := range inputFiles {
		before := mustReadFile(t, f)
		_, fixErr := FixFile(f, rules)
		require.NoError(t, fixErr, "FixFile (second pass) %s", f)
		after := mustReadFile(t, f)
		require.Equal(t, string(before), string(after), "idempotency violation: %s changed on second fix pass", f)
	}

	if *updateGolden {
		require.NoError(t, os.RemoveAll(wantFixDir))
		require.NoError(t, os.MkdirAll(wantFixDir, 0o755))
		for _, f := range inputFiles {
			rel, relErr := filepath.Rel(workDir, f)
			require.NoError(t, relErr)
			dst := filepath.Join(wantFixDir, rel)
			require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
			require.NoError(t, os.WriteFile(dst, mustReadFile(t, f), 0o644))
		}
		return
	}

	// Compare every file under workDir to its counterpart in want_fix/.
	require.True(t, hasWantFix, "fixture %s has no want_fix/ dir", fixtureDir)
	for _, f := range inputFiles {
		rel, err := filepath.Rel(workDir, f)
		require.NoError(t, err)
		golden := filepath.Join(wantFixDir, rel)
		want, err := os.ReadFile(golden)
		require.NoError(t, err, "missing golden %s (run with -update to create)", golden)
		got := mustReadFile(t, f)
		require.Equal(t, string(want), string(got), "fix output mismatch for %s (run with -update to regenerate)", rel)
	}
}

// renderCheckGolden produces a deterministic text rendering of CheckResult
// suitable for storing as a golden file. It intentionally omits the "Summary"
// tail from FormatText (which jitters with test ordering) and uses a stable
// per-finding format.
func renderCheckGolden(result *CheckResult) string {
	if result == nil || len(result.Findings) == 0 {
		return "(no findings)\n"
	}
	// Findings are already sorted by Check; render each as:
	//   <file>:<line>: [<rule>] (mechanical|manual) <text>
	var buf bytes.Buffer
	for _, f := range result.Findings {
		label := "manual"
		if f.Mechanical {
			label = "mechanical"
		}
		fmt.Fprintf(&buf, "%s:%d: [%s] (%s) %s\n", f.File, f.Line, f.RuleID, label, f.Text)
	}
	fmt.Fprintf(&buf, "\ntotal=%d mechanical=%d manual=%d\n", result.Total, result.Mechanical, result.Judgment)
	return buf.String()
}

// copyTree recursively copies src into a freshly created tempdir and returns
// the tempdir path.
func copyTree(t *testing.T, src string) string {
	t.Helper()
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, relErr := filepath.Rel(src, p)
		if relErr != nil {
			return relErr
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		return os.WriteFile(target, data, 0o644)
	})
	require.NoError(t, err)
	return dst
}

// listGoFiles returns absolute paths of every .go file under dir, sorted.
func listGoFiles(t *testing.T, dir string) []string {
	t.Helper()
	var files []string
	err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() && strings.HasSuffix(d.Name(), ".go") {
			files = append(files, p)
		}
		return nil
	})
	require.NoError(t, err)
	sort.Strings(files)
	return files
}

func mustReadFile(t *testing.T, p string) []byte {
	t.Helper()
	data, err := os.ReadFile(p)
	require.NoError(t, err)
	return data
}

// assertGofmtStable verifies that the file is already gofmt-stable: running
// format.Source on its contents yields the same bytes.
func assertGofmtStable(t *testing.T, path string) {
	t.Helper()
	src := mustReadFile(t, path)
	formatted, err := format.Source(src)
	if err != nil {
		return // not valid Go; not the harness's concern
	}
	require.Equal(t, string(formatted), string(src), "file %s is not gofmt-stable after fix", path)
}

// TestRulesFixtures walks testdata/rules/{v2,v3}/* and runs each fixture.
func TestRulesFixtures(t *testing.T) {
	for _, migration := range []string{"v3", "v2"} {
		root := filepath.Join("testdata", "rules", migration)
		entries, err := os.ReadDir(root)
		if os.IsNotExist(err) {
			continue
		}
		require.NoError(t, err)
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			name := e.Name()
			fixtureDir := filepath.Join(root, name)
			t.Run(migration+"/"+name, func(t *testing.T) {
				runFixture(t, fixtureDir)
			})
		}
	}
}

// TestEdgeCases walks testdata/edge/* and runs each fixture.
func TestEdgeCases(t *testing.T) {
	root := filepath.Join("testdata", "edge")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return
	}
	require.NoError(t, err)
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		fixtureDir := filepath.Join(root, e.Name())
		t.Run(e.Name(), func(t *testing.T) {
			runFixture(t, fixtureDir)
		})
	}
}
