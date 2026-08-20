package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"golang.org/x/mod/modfile"
)

// runGoModTidy runs `go mod tidy` inside dir. It's a package-level variable
// so tests can swap in a recording stub without shelling out to the real
// toolchain. Production code always goes through defaultRunGoModTidy.
var runGoModTidy = defaultRunGoModTidy

func defaultRunGoModTidy(dir string, out, errw io.Writer) error {
	cmd := exec.CommandContext(context.Background(), "go", "mod", "tidy")
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errw
	// jwx v4 depends on encoding/json/v2, which requires
	// GOEXPERIMENT=jsonv2 at build time. After our rewrite the
	// project's go.mod points at v4, so tidy will fail with
	// "build constraints exclude all Go files" if jsonv2 is not
	// in the env. Set it ourselves for this invocation; the
	// migration target requires it.
	cmd.Env = appendGOEXPERIMENT(os.Environ(), "jsonv2")
	return cmd.Run()
}

// appendGOEXPERIMENT returns env with GOEXPERIMENT extended to
// include exp (preserving any values the caller already set, in the
// order they appeared) so we never silently clobber the user's
// experiments. Idempotent if exp is already listed.
//
//nolint:unparam // exp is "jsonv2" today; kept parameterized so future migrations can layer additional experiments without rewriting the helper.
func appendGOEXPERIMENT(env []string, exp string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if !strings.HasPrefix(kv, "GOEXPERIMENT=") {
			out = append(out, kv)
			continue
		}
		found = true
		val := strings.TrimPrefix(kv, "GOEXPERIMENT=")
		if val == "" {
			out = append(out, "GOEXPERIMENT="+exp)
			continue
		}
		if slices.Contains(strings.Split(val, ","), exp) {
			out = append(out, kv)
			return out
		}
		out = append(out, "GOEXPERIMENT="+val+","+exp)
	}
	if !found {
		out = append(out, "GOEXPERIMENT="+exp)
	}
	return out
}

// goModFilename is the canonical filename of a Go module file, used by the
// build-file dispatcher and the fixable-file walker.
const goModFilename = "go.mod"

// FixBuildFile applies mechanical fixes to a non-Go build file (currently
// only go.mod). Returns nil if the file is not a recognized build target
// or has no applicable changes — callers treat nil like FixFile does.
// fired is the subset of rules that matched in this file's module during the
// same batch. Pass nil when that is not known; version floors then fall back
// to the rule set baseline.
func FixBuildFile(filePath string, rules, fired []CompiledRule) (*FixResult, error) {
	switch filepath.Base(filePath) {
	case goModFilename:
		return fixGoMod(filePath, rules, fired)
	}
	return nil, nil //nolint:nilnil
}

// fixGoMod brings a consumer's go.mod in line with the rules that fired in
// that module: it swaps the migration source path for the target path, raises
// any require that sits below a fired rule's declared floor, and adds
// companion modules those rules point at.
//
// `fired` is the subset of `rules` that actually matched in this module.
// Versions are resolved from it rather than from the whole rule set, so a
// consumer is never dragged up to the newest version some unrelated rule
// happens to mention.
//
// Modules no rule mentions are left untouched; downstream `go mod tidy`
// resolves transitive deps.
func fixGoMod(filePath string, rules, fired []CompiledRule) (*FixResult, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	mf, err := modfile.Parse(filePath, src, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filePath, err)
	}

	applied := make(map[string]struct{})

	// 1. Swap the migration source path for the target path, at the version
	//    the rules that fired actually need.
	for _, req := range mf.Require {
		for _, rw := range importRewriteRules(rules) {
			if req.Mod.Path != rw.from {
				continue
			}
			if err := mf.DropRequire(req.Mod.Path); err != nil {
				return nil, fmt.Errorf("dropping %s from %s: %w", req.Mod.Path, filePath, err)
			}
			if err := mf.AddRequire(rw.to, requiredVersionFor(rw.to, fired)); err != nil {
				return nil, fmt.Errorf("adding %s to %s: %w", rw.to, filePath, err)
			}
			applied[rw.ruleID] = struct{}{}
		}
	}

	// 2. Raise any require already present that sits below what a fired rule
	//    needs. A project that finished the path swap on an earlier run, or
	//    that was always on the target major, never reaches step 1 but can
	//    still be too old for the guidance it is being given.
	for _, u := range unmetFloors(fired, currentRequires(mf)) {
		if err := mf.AddRequire(u.Path, u.Need); err != nil {
			return nil, fmt.Errorf("raising %s in %s: %w", u.Path, filePath, err)
		}
		applied[u.RuleID] = struct{}{}
	}

	// 3. Add companion modules that fired moved_to_extension rules point at,
	//    at the floor those rules declare. Without a declared floor there is
	//    no version to write, so the requirement is left for `go mod tidy`.
	for path, r := range companionRequirements(fired) {
		if _, ok := currentRequires(mf)[path]; ok {
			continue
		}
		if err := mf.AddRequire(path, r.version); err != nil {
			return nil, fmt.Errorf("adding %s to %s: %w", path, filePath, err)
		}
		applied[r.ruleID] = struct{}{}
	}

	if len(applied) == 0 {
		return nil, nil //nolint:nilnil
	}

	mf.Cleanup()
	out, err := mf.Format()
	if err != nil {
		return nil, fmt.Errorf("formatting %s: %w", filePath, err)
	}
	origMode, err := statMode(filePath)
	if err != nil {
		return nil, fmt.Errorf("stat %s: %w", filePath, err)
	}
	if err := writeAtomicWithMode(filePath, out, origMode); err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(applied))
	for id := range applied {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	return &FixResult{File: filePath, Applied: ids}, nil
}

// importRewrite is a single from-path → to-path mapping derived from a
// kind=import_change, package=all, mechanical=true rule.
type importRewrite struct {
	ruleID string
	from   string
	to     string
}

// importRewriteRules returns the cross-package import rewrites from the
// rule set — currently just the main jwx v3→v4 (or v2→v4) path swap.
// Package-scoped import_change rules (e.g. dep-option-v2-to-v3) are
// excluded; they require a separate mechanical opt-in.
func importRewriteRules(rules []CompiledRule) []importRewrite {
	var out []importRewrite
	for _, r := range rules {
		if r.Kind != kindImportChange {
			continue
		}
		if r.Package != packageAll {
			continue
		}
		if !r.Mechanical {
			continue
		}
		from := r.FromVersion()
		to := r.ToVersion()
		if from == "" || to == "" {
			continue
		}
		out = append(out, importRewrite{ruleID: r.ID, from: from, to: to})
	}
	return out
}

// (writeAtomic was inlined into writeAtomicWithMode in fix.go to share
// the mode-preservation path with writeFormatted.)
