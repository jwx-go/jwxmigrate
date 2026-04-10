package main

import (
	"flag"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "jwxmigrate: %s\n", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	fset := flag.NewFlagSet("jwxmigrate", flag.ContinueOnError)
	format := fset.String("format", "text", "output format: text or json")
	mechanicalOnly := fset.Bool("mechanical", false, "only report mechanical (auto-fixable) rules")
	ruleID := fset.String("rule", "", "only check a specific rule by ID")
	fix := fset.Bool("fix", false, "apply mechanical fixes in-place")

	if err := fset.Parse(args); err != nil {
		return err
	}

	target := "."
	if fset.NArg() > 0 {
		target = fset.Arg(0)
	}

	rules, err := loadRules()
	if err != nil {
		return err
	}

	if *fix {
		return runFix(target, rules)
	}

	return runCheck(target, rules, *format, *mechanicalOnly, *ruleID)
}

func runCheck(dir string, rules []CompiledRule, format string, mechanicalOnly bool, ruleID string) error {
	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	opts := CheckOptions{
		MechanicalOnly: mechanicalOnly,
		RuleID:         ruleID,
	}

	result, err := Check(dir, rules, opts)
	if err != nil {
		return err
	}

	switch format {
	case "text":
		FormatText(os.Stdout, result)
	case "json":
		if err := FormatJSON(os.Stdout, result); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown format %q; available: text, json", format)
	}

	if result.Total > 0 {
		os.Exit(1)
	}
	return nil
}

func runFix(target string, rules []CompiledRule) error {
	info, err := os.Stat(target)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", target, err)
	}

	var files []string
	if info.IsDir() {
		files, err = findGoFiles(target)
		if err != nil {
			return err
		}
	} else {
		files = []string{target}
	}

	var totalFixed int
	var allRemaining []Finding
	for _, f := range files {
		result, err := FixFile(f, rules)
		if err != nil {
			return fmt.Errorf("fixing %s: %w", f, err)
		}
		if result == nil {
			continue
		}
		if len(result.Applied) > 0 {
			totalFixed += len(result.Applied)
			fmt.Fprintf(os.Stdout, "%s: applied %s\n", result.File, strings.Join(result.Applied, ", "))
		}
		allRemaining = append(allRemaining, result.Remaining...)
	}

	if totalFixed == 0 {
		fmt.Fprintln(os.Stdout, "no mechanical fixes to apply")
	} else {
		fmt.Fprintf(os.Stdout, "\n%d rule(s) applied\n", totalFixed)
	}

	if len(allRemaining) > 0 {
		fmt.Fprintf(os.Stdout, "\nRemaining issues (%d):\n\n", len(allRemaining))
		for _, f := range allRemaining {
			fmt.Fprintf(os.Stdout, "  %s:%d:\n", f.File, f.Line)
			fmt.Fprintf(os.Stdout, "    %s\n\n", f.Note)
		}
		os.Exit(1)
	}
	return nil
}

func findGoFiles(dir string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		name := d.Name()
		if d.IsDir() {
			if name == "vendor" || name == "node_modules" || (len(name) > 0 && name[0] == '.') {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(name, ".go") {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
