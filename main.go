package main

import (
	"flag"
	"fmt"
	"os"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "jwxmigrate: %s\n", err)
		os.Exit(2)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("jwxmigrate", flag.ContinueOnError)
	format := fs.String("format", "text", "output format: text or json")
	mechanicalOnly := fs.Bool("mechanical", false, "only report mechanical (auto-fixable) rules")
	ruleID := fs.String("rule", "", "only check a specific rule by ID")

	if err := fs.Parse(args); err != nil {
		return err
	}

	dir := "."
	if fs.NArg() > 0 {
		dir = fs.Arg(0)
	}

	info, err := os.Stat(dir)
	if err != nil {
		return fmt.Errorf("cannot access %s: %w", dir, err)
	}
	if !info.IsDir() {
		return fmt.Errorf("%s is not a directory", dir)
	}

	rules, err := loadRules()
	if err != nil {
		return err
	}

	opts := CheckOptions{
		MechanicalOnly: *mechanicalOnly,
		RuleID:         *ruleID,
	}

	result, err := Check(dir, rules, opts)
	if err != nil {
		return err
	}

	switch *format {
	case "text":
		FormatText(os.Stdout, result)
	case "json":
		if err := FormatJSON(os.Stdout, result); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown format %q; available: text, json", *format)
	}

	if result.Total > 0 {
		os.Exit(1)
	}
	return nil
}
