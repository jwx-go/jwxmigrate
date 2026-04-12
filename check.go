package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Finding represents a single match of a migration rule against a source file.
type Finding struct {
	RuleID        string `json:"rule_id"`
	File          string `json:"file"`
	Line          int    `json:"line"`
	Text          string `json:"text"`
	Mechanical    bool   `json:"mechanical"`
	Note          string `json:"note"`
	ExampleBefore string `json:"example_before,omitempty"`
	ExampleAfter  string `json:"example_after,omitempty"`

	// Modification-support fields: precise position and node classification.
	Col       int    `json:"col,omitempty"`
	EndLine   int    `json:"end_line,omitempty"`
	EndCol    int    `json:"end_col,omitempty"`
	NodeKind  string `json:"node_kind,omitempty"`
	MatchedBy string `json:"matched_by,omitempty"`
}

// CheckResult holds the aggregate output of a migration check.
type CheckResult struct {
	Total      int       `json:"total"`
	Mechanical int       `json:"mechanical"`
	Judgment   int       `json:"judgment"`
	Findings   []Finding `json:"findings"`
}

// Check scans the given directory for v3 patterns and returns findings.
func Check(dir string, rules []CompiledRule, opts CheckOptions) (*CheckResult, error) {
	goRules, fileRules := splitRules(rules)

	goFindings, err := checkGoFiles(dir, goRules, opts)
	if err != nil {
		return nil, err
	}
	buildFindings := checkBuildFiles(dir, fileRules, opts)

	findings := make([]Finding, 0, len(goFindings)+len(buildFindings))
	findings = append(findings, goFindings...)
	findings = append(findings, buildFindings...)

	sort.Slice(findings, func(i, j int) bool {
		if findings[i].File != findings[j].File {
			return findings[i].File < findings[j].File
		}
		return findings[i].Line < findings[j].Line
	})

	result := &CheckResult{Findings: findings}
	for _, f := range findings {
		result.Total++
		if f.Mechanical {
			result.Mechanical++
		} else {
			result.Judgment++
		}
	}
	return result, nil
}

// CheckOptions controls filtering behavior.
type CheckOptions struct {
	MechanicalOnly bool
	RuleID         string
}

// splitRules separates rules into Go-file rules and build-file rules.
// Go rules are those with AST matchers or search patterns.
// Build rules are those with file_patterns.
func splitRules(rules []CompiledRule) (goRules, fileRules []CompiledRule) {
	for _, r := range rules {
		if len(r.FilePatterns) > 0 {
			fileRules = append(fileRules, r)
		}
		if len(r.ASTMatchers) > 0 || len(r.Patterns) > 0 {
			goRules = append(goRules, r)
		}
	}
	return
}

func shouldSkip(r *CompiledRule, opts CheckOptions) bool {
	if opts.MechanicalOnly && !r.Mechanical {
		return true
	}
	if opts.RuleID != "" && r.ID != opts.RuleID {
		return true
	}
	return false
}

// checkGoFiles scans Go files in dir. It uses type-checked loading where
// possible (for precise receiver type matching on method calls), and falls
// back to AST-only scanning for files that couldn't be type-checked (e.g.
// in nested modules or with missing dependencies).
func checkGoFiles(dir string, rules []CompiledRule, opts CheckOptions) ([]Finding, error) {
	// Phase 1: type-checked loading — process whatever packages we can.
	typedFindings, coveredFiles := checkGoFilesTyped(dir, rules, opts)

	// Phase 2: walk all .go files, skip those already covered by typed loading.
	var untypedFindings []Finding

	absDir, _ := filepath.Abs(dir)
	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		name := d.Name()
		if d.IsDir() {
			if shouldSkipWalkDir(name) {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(name, ".go") {
			return nil
		}

		absPath, _ := filepath.Abs(path)
		if _, covered := coveredFiles[absPath]; covered {
			return nil
		}

		rel, err := filepath.Rel(absDir, absPath)
		if err != nil {
			rel = path
		}

		pf, err := parseGoFile(absPath, rel)
		if err != nil {
			return err
		}
		if pf == nil {
			return nil
		}

		ff := scanGoFileAST(pf, rules, opts)
		untypedFindings = append(untypedFindings, ff...)
		return nil
	})

	return append(typedFindings, untypedFindings...), err
}

// checkBuildFiles is lenient about per-file errors: malformed globs,
// unreadable files, and per-rule scan failures are skipped rather than
// aborting the whole scan.
func checkBuildFiles(dir string, rules []CompiledRule, opts CheckOptions) []Finding {
	var findings []Finding

	for i := range rules {
		r := &rules[i]
		if shouldSkip(r, opts) {
			continue
		}

		for _, globPat := range r.FilePatterns {
			matches, err := filepath.Glob(filepath.Join(dir, globPat))
			if err != nil {
				continue
			}
			for _, path := range matches {
				rel, err := filepath.Rel(dir, path)
				if err != nil {
					rel = path
				}
				ff, err := scanFileForRule(path, rel, r)
				if err != nil {
					continue
				}
				findings = append(findings, ff...)
			}
		}
	}

	return findings
}

func scanFileForRule(path, rel string, r *CompiledRule) ([]Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var findings []Finding
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		for _, pat := range r.Patterns {
			if pat.MatchString(line) {
				finding := Finding{
					RuleID:     r.ID,
					File:       rel,
					Line:       lineNum,
					Text:       strings.TrimSpace(line),
					Mechanical: r.Mechanical,
					Note:       strings.TrimSpace(r.Note),
					MatchedBy:  "regex",
				}
				if r.Example != nil {
					finding.ExampleBefore = strings.TrimSpace(r.Example.Before)
					finding.ExampleAfter = strings.TrimSpace(r.Example.After)
				}
				findings = append(findings, finding)
				break
			}
		}
	}

	return findings, scanner.Err()
}

// FormatText writes findings in human-readable text format.
func FormatText(w io.Writer, result *CheckResult) {
	for _, f := range result.Findings {
		label := "manual"
		if f.Mechanical {
			label = "auto"
		}
		_, _ = fmt.Fprintf(w, "[%s] (%s) %s:%d: %s\n", f.RuleID, label, f.File, f.Line, f.Text)
		_, _ = fmt.Fprintf(w, "  %s\n\n", f.Note)
	}

	_, _ = fmt.Fprintf(w, "Summary: %d items remaining (%d mechanical, %d require judgment)\n",
		result.Total, result.Mechanical, result.Judgment)
}

// FormatJSON writes findings as JSON.
func FormatJSON(w io.Writer, result *CheckResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
