package main

import (
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	code, err := run(os.Args[1:])
	if err != nil {
		fmt.Fprintf(os.Stderr, "jwxmigrate: %s\n", err)
		os.Exit(2)
	}
	os.Exit(code)
}

func run(args []string) (int, error) {
	fset := flag.NewFlagSet("jwxmigrate", flag.ContinueOnError)
	from := fset.String("from", "v3", "source version to migrate from: v2 or v3")
	format := fset.String("format", "text", "output format: text or json")
	mechanicalOnly := fset.Bool("mechanical", false, "only report mechanical (auto-fixable) rules")
	ruleID := fset.String("rule", "", "only check a specific rule by ID")
	fix := fset.Bool("fix", false, "apply mechanical fixes in-place")
	backup := fset.Bool("backup", false, "with -fix, save <file>.bak next to each rewritten file")

	if err := fset.Parse(args); err != nil {
		return 0, err
	}

	target := "."
	if fset.NArg() > 0 {
		target = fset.Arg(0)
	}

	migration := *from + "-to-v4"
	rules, err := loadRules(migration)
	if err != nil {
		return 0, err
	}

	if *fix {
		return runFix(target, rules, FixOptions{Backup: *backup})
	}
	if *backup {
		return 0, fmt.Errorf("-backup only has an effect together with -fix")
	}

	return runCheck(target, rules, *format, *mechanicalOnly, *ruleID)
}

func runCheck(dir string, rules []CompiledRule, format string, mechanicalOnly bool, ruleID string) (int, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return 0, fmt.Errorf("cannot access %s: %w", dir, err)
	}
	if !info.IsDir() {
		return 0, fmt.Errorf("%s is not a directory", dir)
	}

	opts := CheckOptions{
		MechanicalOnly: mechanicalOnly,
		RuleID:         ruleID,
	}

	result, err := Check(dir, rules, opts)
	if err != nil {
		return 0, err
	}

	switch format {
	case "text":
		FormatText(os.Stdout, result)
	case "json":
		if err := FormatJSON(os.Stdout, result); err != nil {
			return 0, err
		}
	default:
		return 0, fmt.Errorf("unknown format %q; available: text, json", format)
	}

	if result.Total > 0 {
		return 1, nil
	}
	return 0, nil
}

func runFix(target string, rules []CompiledRule, opts FixOptions) (int, error) {
	info, err := os.Stat(target)
	if err != nil {
		return 0, fmt.Errorf("cannot access %s: %w", target, err)
	}

	var files []string
	if info.IsDir() {
		files, err = findFixableFiles(target)
		if err != nil {
			return 0, err
		}
	} else {
		files = []string{target}
	}

	summary := fixFiles(files, rules, opts, os.Stdout, os.Stderr)
	if len(summary.failures) > 0 || len(summary.remaining) > 0 {
		return 1, nil
	}
	return 0, nil
}

type fixFailure struct {
	file string
	err  error
}

type fixBatchSummary struct {
	totalFixed int
	remaining  []Finding
	failures   []fixFailure
	// goModDirs is the set of absolute directories whose go.mod was
	// rewritten during this batch, used to drive the follow-up
	// `go mod tidy` step. Order matches first rewrite.
	goModDirs []string
}

// fixFiles applies FixFile to every file and keeps going when individual
// files fail. Errors are collected into summary.failures so the caller
// emits a single manifest at the end instead of aborting the batch and
// leaving the working tree half-migrated with no record of what was
// skipped.
//
// Type-info loading uses a content overlay (see snapshotBatchOverlay) so
// the type checker sees the original v3 source for every batch file
// throughout the run. Without this, once one file in a package gets its
// imports rewritten to v4, sibling files fail to type-check (the v4
// module isn't yet in go.mod) — and any rule that requires types
// (jwk-export-generic, get-to-field) silently downgrades to "no types,
// no rewrite" for those siblings.
func fixFiles(files []string, rules []CompiledRule, opts FixOptions, out, errw io.Writer) fixBatchSummary {
	var summary fixBatchSummary
	overlay := snapshotBatchOverlay(files)
	opts.overlay = overlay
	if opts.typedCache == nil {
		opts.typedCache = buildTypedFileCache(files, overlay)
	}
	for _, f := range files {
		result, err := fixOneFile(f, rules, opts)
		if err != nil {
			summary.failures = append(summary.failures, fixFailure{file: f, err: err})
			_, _ = fmt.Fprintf(errw, "%s: skipped: %s\n", f, err)
			continue
		}
		if result == nil {
			continue
		}
		if len(result.Applied) > 0 {
			summary.totalFixed += len(result.Applied)
			_, _ = fmt.Fprintf(out, "%s: applied %s\n", result.File, strings.Join(result.Applied, ", "))
			if filepath.Base(result.File) == goModFilename {
				if abs, absErr := filepath.Abs(filepath.Dir(result.File)); absErr == nil {
					summary.goModDirs = append(summary.goModDirs, abs)
				}
			}
		}
		summary.remaining = append(summary.remaining, result.Remaining...)
	}

	for _, dir := range summary.goModDirs {
		_, _ = fmt.Fprintf(out, "%s: running go mod tidy\n", dir)
		if err := runGoModTidy(dir, out, errw); err != nil {
			_, _ = fmt.Fprintf(errw, "%s: go mod tidy failed: %s\n", dir, err)
		}
	}

	if summary.totalFixed == 0 {
		_, _ = fmt.Fprintln(out, "no mechanical fixes to apply")
	} else {
		_, _ = fmt.Fprintf(out, "\n%d rule(s) applied across %d file(s)\n", summary.totalFixed, len(files)-len(summary.failures))
	}

	if len(summary.failures) > 0 {
		_, _ = fmt.Fprintf(errw, "\n%d file(s) skipped due to errors:\n", len(summary.failures))
		for _, fail := range summary.failures {
			_, _ = fmt.Fprintf(errw, "  %s: %s\n", fail.file, fail.err)
		}
	}

	if len(summary.remaining) > 0 {
		_, _ = fmt.Fprintf(out, "\nRemaining issues (%d):\n\n", len(summary.remaining))
		for _, f := range summary.remaining {
			_, _ = fmt.Fprintf(out, "  %s:%d:\n", f.File, f.Line)
			_, _ = fmt.Fprintf(out, "    %s\n\n", f.Note)
		}
	}
	return summary
}

// fixOneFile dispatches to the right fixer based on the file's basename:
// .go files use the AST-based FixFileWithOptions, go.mod uses the
// modfile-based FixBuildFile. Anything else returns nil so the caller
// treats it as a no-op.
func fixOneFile(filePath string, rules []CompiledRule, opts FixOptions) (*FixResult, error) {
	if strings.HasSuffix(filePath, ".go") {
		return FixFileWithOptions(filePath, rules, opts)
	}
	return FixBuildFile(filePath, rules)
}

// snapshotBatchOverlay reads the pre-batch contents of every file in the
// batch into a map keyed by absolute path, ready to hand to
// packages.Config.Overlay. Files we can't read are silently skipped —
// FixFile will surface the read failure via its own error path when it
// gets to them.
func snapshotBatchOverlay(files []string) map[string][]byte {
	out := make(map[string][]byte, len(files))
	for _, f := range files {
		abs, err := filepath.Abs(f)
		if err != nil {
			continue
		}
		data, err := os.ReadFile(abs)
		if err != nil {
			continue
		}
		out[abs] = data
	}
	return out
}

// findFixableFiles returns every .go file under dir plus every go.mod
// file the fixer knows how to rewrite. Build files other than go.mod
// stay check-only for now.
func findFixableFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			// The first WalkDir visit is the root itself. When the caller
			// passes `.` or `./`, d.Name() returns `.`, which would otherwise
			// satisfy the dotfile-prefix rule and skip the entire walk —
			// silently making `jwxmigrate -fix .` a no-op. Always descend
			// into the root regardless of its name.
			if path != dir && shouldSkipWalkDir(name) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") || name == goModFilename {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
