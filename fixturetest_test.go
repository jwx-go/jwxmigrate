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
	"golang.org/x/tools/txtar"
	"gopkg.in/yaml.v3"
)

// updateGolden, when set, causes runFixture to overwrite want_*.txt and
// want_fix/ files instead of asserting against them.
var updateGolden = flag.Bool("update", false, "update golden files in testdata/rules and testdata/edge")

// fixtureConfig is an optional per-fixture config.yaml.
//
// Defaults are inferred from the fixture path: fixtures under
// testdata/rules/v2/<rule-id>/ default to migration=v2-to-v4 with rule_id
// taken from the directory basename; everything else defaults to
// migration=v3-to-v4 with no rule_id filter. config.yaml is only needed
// when a fixture wants to override these defaults (e.g. skip_fix, a
// different migration, or mechanical_only).
type fixtureConfig struct {
	Migration      string `yaml:"migration"`
	MechanicalOnly bool   `yaml:"mechanical_only"`
	RuleID         string `yaml:"rule_id"`
	// SkipFix disables the fix-and-compare phase even if want_fix/ is present.
	SkipFix bool `yaml:"skip_fix"`
}

// defaultFixtureConfig returns the defaults for a fixture at the given
// path, derived from its location under testdata/.
func defaultFixtureConfig(fixtureDir string) *fixtureConfig {
	cfg := &fixtureConfig{Migration: "v3-to-v4"}
	abs, err := filepath.Abs(fixtureDir)
	if err != nil {
		return cfg
	}
	parts := strings.Split(filepath.ToSlash(abs), "/")
	// Look for .../testdata/rules/<slug>/<rule-id> and derive both
	// migration (v2/v3) and rule_id from the path.
	for i := range len(parts) - 3 {
		if parts[i] == "testdata" && parts[i+1] == "rules" {
			slug := parts[i+2]
			switch slug {
			case "v2":
				cfg.Migration = "v2-to-v4"
			case "v3":
				cfg.Migration = "v3-to-v4"
			}
			if i+3 < len(parts) {
				cfg.RuleID = parts[i+3]
			}
			return cfg
		}
	}
	return cfg
}

func loadFixtureConfig(fixtureDir string) (*fixtureConfig, error) {
	cfg := defaultFixtureConfig(fixtureDir)
	path := filepath.Join(fixtureDir, "config.yaml")
	data, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return cfg, nil
	}
	if err != nil {
		return nil, err
	}
	// Unmarshal into a separate struct so zero-value fields in the YAML
	// don't clobber path-derived defaults.
	var override fixtureConfig
	if err := yaml.Unmarshal(data, &override); err != nil {
		return nil, fmt.Errorf("parse %s: %w", path, err)
	}
	if override.Migration != "" {
		cfg.Migration = override.Migration
	}
	if override.RuleID != "" {
		cfg.RuleID = override.RuleID
	}
	if override.MechanicalOnly {
		cfg.MechanicalOnly = true
	}
	if override.SkipFix {
		cfg.SkipFix = true
	}
	return cfg, nil
}

// fixtureInputs holds a fixture's input files and expected goldens in
// memory, whether they were loaded from a directory layout or from a
// single fixture.txtar archive.
type fixtureInputs struct {
	// InputFiles maps relative path (e.g. "main.go", "subdir/x.go") to
	// file contents. Must be non-empty.
	InputFiles map[string][]byte
	// WantCheck is the expected check golden text. Empty string means
	// the fixture does not assert check output.
	WantCheck string
	// HasWantCheck is true when the fixture shipped a want_check.txt.
	HasWantCheck bool
	// WantFix maps relative path to expected post-fix contents. nil
	// means the fixture does not assert fix output (still OK to run the
	// fix pass for gofmt + idempotency checks under -update).
	WantFix map[string][]byte
	// HasWantFix is true when the fixture shipped fix-pass expectations.
	HasWantFix bool
	// IsTxtar indicates the fixture was loaded from fixture.txtar and
	// should be persisted back in txtar form under -update.
	IsTxtar bool
	// TxtarPath is the absolute path to fixture.txtar when IsTxtar.
	TxtarPath string
	// TxtarComment preserves any leading comment block in fixture.txtar.
	TxtarComment []byte
}

// loadFixtureInputs reads a fixture either from fixture.txtar (preferred
// if present) or from the legacy input/ + want_check.txt + want_fix/
// directory layout.
func loadFixtureInputs(t *testing.T, fixtureDir string) *fixtureInputs {
	t.Helper()
	txtarPath := filepath.Join(fixtureDir, "fixture.txtar")
	if _, err := os.Stat(txtarPath); err == nil {
		return loadFixtureTxtar(t, txtarPath)
	} else if !os.IsNotExist(err) {
		t.Fatalf("stat %s: %v", txtarPath, err)
	}
	return loadFixtureDir(t, fixtureDir)
}

func loadFixtureDir(t *testing.T, fixtureDir string) *fixtureInputs {
	t.Helper()
	inputDir := filepath.Join(fixtureDir, "input")
	info, err := os.Stat(inputDir)
	require.NoError(t, err, "fixture %s missing input/ dir", fixtureDir)
	require.True(t, info.IsDir(), "fixture %s: input/ is not a directory", fixtureDir)

	fx := &fixtureInputs{
		InputFiles: map[string][]byte{},
	}
	err = filepath.WalkDir(inputDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			return nil
		}
		rel, relErr := filepath.Rel(inputDir, p)
		if relErr != nil {
			return relErr
		}
		data, readErr := os.ReadFile(p)
		if readErr != nil {
			return readErr
		}
		fx.InputFiles[filepath.ToSlash(rel)] = data
		return nil
	})
	require.NoError(t, err)

	if data, err := os.ReadFile(filepath.Join(fixtureDir, "want_check.txt")); err == nil {
		fx.WantCheck = string(data)
		fx.HasWantCheck = true
	} else if !os.IsNotExist(err) {
		t.Fatalf("read want_check.txt: %v", err)
	}

	wantFixDir := filepath.Join(fixtureDir, "want_fix")
	if info, err := os.Stat(wantFixDir); err == nil && info.IsDir() {
		fx.WantFix = map[string][]byte{}
		fx.HasWantFix = true
		walkErr := filepath.WalkDir(wantFixDir, func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}
			rel, relErr := filepath.Rel(wantFixDir, p)
			if relErr != nil {
				return relErr
			}
			data, readErr := os.ReadFile(p)
			if readErr != nil {
				return readErr
			}
			fx.WantFix[filepath.ToSlash(rel)] = data
			return nil
		})
		require.NoError(t, walkErr)
	}
	return fx
}

func loadFixtureTxtar(t *testing.T, txtarPath string) *fixtureInputs {
	t.Helper()
	data, err := os.ReadFile(txtarPath)
	require.NoError(t, err, "read %s", txtarPath)
	ar := txtar.Parse(data)
	fx := &fixtureInputs{
		InputFiles:   map[string][]byte{},
		IsTxtar:      true,
		TxtarPath:    txtarPath,
		TxtarComment: ar.Comment,
	}
	for _, f := range ar.Files {
		name := filepath.ToSlash(f.Name)
		switch {
		case name == "want_check.txt":
			fx.WantCheck = string(f.Data)
			fx.HasWantCheck = true
		case strings.HasPrefix(name, "want_fix/"):
			if fx.WantFix == nil {
				fx.WantFix = map[string][]byte{}
				fx.HasWantFix = true
			}
			fx.WantFix[strings.TrimPrefix(name, "want_fix/")] = f.Data
		case strings.HasPrefix(name, "input/"):
			fx.InputFiles[strings.TrimPrefix(name, "input/")] = f.Data
		default:
			t.Fatalf("%s: unexpected section %q (expected input/*, want_check.txt, or want_fix/*)", txtarPath, name)
		}
	}
	require.NotEmpty(t, fx.InputFiles, "%s: fixture.txtar has no input/ files", txtarPath)
	return fx
}

// runFixture executes a single fixture directory.
//
// Fixture layout — a fixture is either a txtar archive OR a directory tree:
//
//	<fixtureDir>/
//	  fixture.txtar          (preferred) archive with input/*, optional
//	                         want_check.txt, optional want_fix/*
//	  input/                 (legacy) Go source files
//	  want_check.txt         (legacy) expected FormatText output
//	  want_fix/              (legacy) expected file contents after FixFile
//	  config.yaml            (optional) overrides — only needed for
//	                         skip_fix, mechanical_only, or non-default
//	                         migration/rule_id. When absent, defaults are
//	                         inferred from the fixture path.
//
// When the global -update flag is set, want_check.txt and want_fix/
// contents are overwritten with the actual tool output — either in the
// txtar archive or the directory tree, matching the fixture's native
// layout.
func runFixture(t *testing.T, fixtureDir string) {
	t.Helper()

	cfg, err := loadFixtureConfig(fixtureDir)
	require.NoError(t, err)

	fx := loadFixtureInputs(t, fixtureDir)

	rules, err := loadRules(cfg.Migration)
	require.NoError(t, err)

	// Materialize inputs into a tempdir so fixer code can open + rewrite
	// them as real files. The fixture source is never touched.
	workDir := t.TempDir()
	for rel, data := range fx.InputFiles {
		dst := filepath.Join(workDir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
		require.NoError(t, os.WriteFile(dst, data, 0o644))
	}

	checkOpts := CheckOptions{
		MechanicalOnly: cfg.MechanicalOnly,
		RuleID:         cfg.RuleID,
	}

	result, err := Check(workDir, rules, checkOpts)
	require.NoError(t, err)

	gotCheck := renderCheckGolden(result)
	if !*updateGolden && fx.HasWantCheck {
		require.Equal(t, fx.WantCheck, gotCheck, "check output mismatch for %s (run with -update to regenerate)", fixtureDir)
	}

	// When updating, always refresh the check golden; when not updating
	// with no want_fix baseline, we're done.
	if cfg.SkipFix || (!fx.HasWantFix && !*updateGolden) {
		if *updateGolden {
			fx.WantCheck = gotCheck
			fx.HasWantCheck = gotCheck != ""
			persistFixtureGolden(t, fixtureDir, fx)
		}
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

	gotFix := map[string][]byte{}
	for _, f := range inputFiles {
		rel, relErr := filepath.Rel(workDir, f)
		require.NoError(t, relErr)
		gotFix[filepath.ToSlash(rel)] = mustReadFile(t, f)
	}

	if *updateGolden {
		fx.WantCheck = gotCheck
		fx.HasWantCheck = gotCheck != ""
		fx.WantFix = gotFix
		fx.HasWantFix = len(gotFix) > 0
		persistFixtureGolden(t, fixtureDir, fx)
		return
	}

	require.True(t, fx.HasWantFix, "fixture %s has no want_fix (run with -update to create)", fixtureDir)
	for rel, got := range gotFix {
		want, ok := fx.WantFix[rel]
		require.True(t, ok, "missing want_fix entry for %s in %s (run with -update to create)", rel, fixtureDir)
		require.Equal(t, string(want), string(got), "fix output mismatch for %s (run with -update to regenerate)", rel)
	}
}

// persistFixtureGolden writes updated goldens back to the fixture's
// original storage form — txtar archive or directory tree.
func persistFixtureGolden(t *testing.T, fixtureDir string, fx *fixtureInputs) {
	t.Helper()
	if fx.IsTxtar {
		persistFixtureTxtar(t, fx)
		return
	}
	persistFixtureDir(t, fixtureDir, fx)
}

func persistFixtureDir(t *testing.T, fixtureDir string, fx *fixtureInputs) {
	t.Helper()
	wantCheckPath := filepath.Join(fixtureDir, "want_check.txt")
	if fx.HasWantCheck {
		require.NoError(t, os.WriteFile(wantCheckPath, []byte(fx.WantCheck), 0o644))
	} else {
		_ = os.Remove(wantCheckPath)
	}

	wantFixDir := filepath.Join(fixtureDir, "want_fix")
	require.NoError(t, os.RemoveAll(wantFixDir))
	if !fx.HasWantFix {
		return
	}
	require.NoError(t, os.MkdirAll(wantFixDir, 0o755))
	for rel, data := range fx.WantFix {
		dst := filepath.Join(wantFixDir, filepath.FromSlash(rel))
		require.NoError(t, os.MkdirAll(filepath.Dir(dst), 0o755))
		require.NoError(t, os.WriteFile(dst, data, 0o644))
	}
}

func persistFixtureTxtar(t *testing.T, fx *fixtureInputs) {
	t.Helper()
	ar := &txtar.Archive{Comment: fx.TxtarComment}
	// Stable ordering: input/* sorted, then want_check.txt, then want_fix/* sorted.
	inputKeys := make([]string, 0, len(fx.InputFiles))
	for k := range fx.InputFiles {
		inputKeys = append(inputKeys, k)
	}
	sort.Strings(inputKeys)
	for _, k := range inputKeys {
		ar.Files = append(ar.Files, txtar.File{Name: "input/" + k, Data: fx.InputFiles[k]})
	}
	if fx.HasWantCheck {
		ar.Files = append(ar.Files, txtar.File{Name: "want_check.txt", Data: []byte(fx.WantCheck)})
	}
	if fx.HasWantFix {
		fixKeys := make([]string, 0, len(fx.WantFix))
		for k := range fx.WantFix {
			fixKeys = append(fixKeys, k)
		}
		sort.Strings(fixKeys)
		for _, k := range fixKeys {
			ar.Files = append(ar.Files, txtar.File{Name: "want_fix/" + k, Data: fx.WantFix[k]})
		}
	}
	require.NoError(t, os.WriteFile(fx.TxtarPath, txtar.Format(ar), 0o644))
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
