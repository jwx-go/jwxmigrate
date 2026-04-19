package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"

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
	return cmd.Run()
}

// goModFilename is the canonical filename of a Go module file, used by the
// build-file dispatcher and the fixable-file walker.
const goModFilename = "go.mod"

// latestV4Version is the jwx/v4 module version that go.mod rewrites pin to.
// Bump on every jwxmigrate release that needs to track a newer v4 minimum.
// fixFiles runs `go mod tidy` after the rewrite, which floats the version
// to whatever the toolchain selects, so this only needs to be a valid
// version that go can resolve, not necessarily the absolute latest.
const latestV4Version = "v4.0.0"

// FixBuildFile applies mechanical fixes to a non-Go build file (currently
// only go.mod). Returns nil if the file is not a recognized build target
// or has no applicable changes — callers treat nil like FixFile does.
func FixBuildFile(filePath string, rules []CompiledRule) (*FixResult, error) {
	switch filepath.Base(filePath) {
	case goModFilename:
		return fixGoMod(filePath, rules)
	}
	return nil, nil //nolint:nilnil
}

// fixGoMod rewrites jwx require entries from the migration's source path
// to its target path, pinned at latestV4Version. Other modules are left
// untouched; downstream `go mod tidy` is responsible for resolving
// transitive deps and version selection.
func fixGoMod(filePath string, rules []CompiledRule) (*FixResult, error) {
	src, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("reading %s: %w", filePath, err)
	}

	mf, err := modfile.Parse(filePath, src, nil)
	if err != nil {
		return nil, fmt.Errorf("parsing %s: %w", filePath, err)
	}

	rewrites := importRewriteRules(rules)
	if len(rewrites) == 0 {
		return nil, nil //nolint:nilnil
	}

	applied := make(map[string]struct{})
	for _, req := range mf.Require {
		for _, rw := range rewrites {
			if req.Mod.Path != rw.from {
				continue
			}
			if err := mf.DropRequire(req.Mod.Path); err != nil {
				return nil, fmt.Errorf("dropping %s from %s: %w", req.Mod.Path, filePath, err)
			}
			if err := mf.AddRequire(rw.to, latestV4Version); err != nil {
				return nil, fmt.Errorf("adding %s to %s: %w", rw.to, filePath, err)
			}
			applied[rw.ruleID] = struct{}{}
		}
	}

	if len(applied) == 0 {
		return nil, nil //nolint:nilnil
	}

	mf.Cleanup()
	out, err := mf.Format()
	if err != nil {
		return nil, fmt.Errorf("formatting %s: %w", filePath, err)
	}
	if err := writeAtomic(filePath, out); err != nil {
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

// writeAtomic writes data to filePath via a sibling temp file + rename,
// matching the durability guarantees writeFormatted gives Go-file rewrites.
func writeAtomic(filePath string, data []byte) error {
	dir := filepath.Dir(filePath)
	tmp, err := os.CreateTemp(dir, filepath.Base(filePath)+".jwxmigrate.tmp.*")
	if err != nil {
		return fmt.Errorf("creating temp for %s: %w", filePath, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("writing temp for %s: %w", filePath, err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		cleanup()
		return fmt.Errorf("fsync temp for %s: %w", filePath, err)
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return fmt.Errorf("closing temp for %s: %w", filePath, err)
	}
	if err := os.Rename(tmpPath, filePath); err != nil {
		cleanup()
		return fmt.Errorf("renaming temp to %s: %w", filePath, err)
	}
	return nil
}
