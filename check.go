package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
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
}

// CheckResult holds the aggregate output of a migration check.
type CheckResult struct {
	Total      int       `json:"total"`
	Mechanical int       `json:"mechanical"`
	Judgment   int       `json:"judgment"`
	Findings   []Finding `json:"findings"`
}

var v3ImportPattern = regexp.MustCompile(`lestrrat-go/jwx/v3`)

// Check scans the given directory for v3 patterns and returns findings.
func Check(dir string, rules []CompiledRule, opts CheckOptions) (*CheckResult, error) {
	goRules, fileRules := splitRules(rules)

	var findings []Finding

	// Pass 1+2: Walk .go files, check for v3 imports, then scan for API patterns.
	goFindings, err := checkGoFiles(dir, goRules, opts)
	if err != nil {
		return nil, err
	}
	findings = append(findings, goFindings...)

	// Pass 3: Scan non-Go files for build-related rules.
	buildFindings, err := checkBuildFiles(dir, fileRules, opts)
	if err != nil {
		return nil, err
	}
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

// splitRules separates rules into Go-file rules (those with search_patterns)
// and build-file rules (those with file_patterns).
func splitRules(rules []CompiledRule) (goRules, fileRules []CompiledRule) {
	for _, r := range rules {
		if len(r.FilePatterns) > 0 {
			fileRules = append(fileRules, r)
		}
		if len(r.Patterns) > 0 {
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

func checkGoFiles(dir string, rules []CompiledRule, opts CheckOptions) ([]Finding, error) {
	var findings []Finding

	err := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		// Skip hidden directories and vendor
		name := d.Name()
		if d.IsDir() {
			if name == "vendor" || name == "node_modules" || (len(name) > 0 && name[0] == '.') {
				return filepath.SkipDir
			}
			return nil
		}

		if !strings.HasSuffix(name, ".go") {
			return nil
		}

		rel, err := filepath.Rel(dir, path)
		if err != nil {
			rel = path
		}

		ff, err := scanGoFile(path, rel, rules, opts)
		if err != nil {
			return err
		}
		findings = append(findings, ff...)
		return nil
	})

	return findings, err
}

func scanGoFile(path, rel string, rules []CompiledRule, opts CheckOptions) ([]Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	// Pass 1: Check if file imports jwx/v3.
	hasV3Import, err := fileContainsPattern(f, v3ImportPattern)
	if err != nil {
		return nil, err
	}
	if !hasV3Import {
		return nil, nil
	}

	// Reset to beginning for pass 2.
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	// Pass 2: Scan each line against all rule patterns.
	var findings []Finding
	scanner := bufio.NewScanner(f)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()

		for i := range rules {
			r := &rules[i]
			if shouldSkip(r, opts) {
				continue
			}
			for _, pat := range r.Patterns {
				if pat.MatchString(line) {
					finding := Finding{
						RuleID:     r.ID,
						File:       rel,
						Line:       lineNum,
						Text:       strings.TrimSpace(line),
						Mechanical: r.Mechanical,
						Note:       strings.TrimSpace(r.Note),
					}
					if r.Example != nil {
						finding.ExampleBefore = strings.TrimSpace(r.Example.Before)
						finding.ExampleAfter = strings.TrimSpace(r.Example.After)
					}
					findings = append(findings, finding)
					break // one finding per rule per line
				}
			}
		}
	}

	return findings, scanner.Err()
}

func fileContainsPattern(f *os.File, pat *regexp.Regexp) (bool, error) {
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		if pat.MatchString(scanner.Text()) {
			return true, nil
		}
	}
	return false, scanner.Err()
}

func checkBuildFiles(dir string, rules []CompiledRule, opts CheckOptions) ([]Finding, error) {
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
					continue // skip unreadable files
				}
				findings = append(findings, ff...)
			}
		}
	}

	return findings, nil
}

func scanFileForRule(path, rel string, r *CompiledRule) ([]Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

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
		fmt.Fprintf(w, "[%s] (%s) %s:%d: %s\n", f.RuleID, label, f.File, f.Line, f.Text)
		fmt.Fprintf(w, "  %s\n\n", f.Note)
	}

	fmt.Fprintf(w, "Summary: %d items remaining (%d mechanical, %d require judgment)\n",
		result.Total, result.Mechanical, result.Judgment)
}

// FormatJSON writes findings as JSON.
func FormatJSON(w io.Writer, result *CheckResult) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(result)
}
