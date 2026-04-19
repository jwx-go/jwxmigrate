package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
)

// maxScanBufferSize bounds the per-line buffer for bufio.Scanner in
// scanFile. The bufio default is 64 KiB, which silently skips any file
// with a longer line; 16 MiB is large enough to cover generated or
// minified content users might reasonably ship in their repos.
const maxScanBufferSize = 16 * 1024 * 1024

// Finding represents a single match of a migration rule against a source file.
type Finding struct {
	RuleID        string `json:"rule_id"`
	File          string `json:"file"`
	Line          int    `json:"line"`
	Text          string `json:"text"`
	SourceLine    string `json:"source_line,omitempty"`
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
	typedFindings, coveredFiles, prescans := checkGoFilesTyped(dir, rules, opts)

	absDir, _ := filepath.Abs(dir)

	// Phase 2a: scan the v3-importing files the prescans already found
	// but that packages.Load didn't cover. On a typical single-module
	// target this short list subsumes what a full phase-2 walk would
	// have produced — without re-walking the tree and re-parsing every
	// .go file.
	var untypedFindings []Finding
	for _, ps := range prescans {
		for _, absPath := range ps.V3Files {
			if _, covered := coveredFiles[absPath]; covered {
				continue
			}
			rel, err := filepath.Rel(absDir, absPath)
			if err != nil {
				rel = absPath
			}
			pf, err := parseGoFile(absPath, rel)
			if err != nil {
				// Scanner path intentionally ignores parse failures —
				// testdata fixtures are commonly unparseable.
				if errors.Is(err, errParseFailed) {
					continue
				}
				return append(typedFindings, untypedFindings...), err
			}
			if pf == nil {
				continue
			}
			untypedFindings = append(untypedFindings, scanGoFileAST(pf, rules, opts)...)
		}
	}

	// Phase 2b: orphan space — files under dir but outside every module
	// root (e.g. user points jwxmigrate at a parent directory containing
	// a mix of Go modules and loose files, or at a non-module tree
	// entirely). If absDir is itself a module root, every file inside it
	// was already classified by prescan and phase 2b is a no-op.
	moduleRoots := make(map[string]struct{}, len(prescans))
	for _, ps := range prescans {
		moduleRoots[ps.Root] = struct{}{}
	}
	if _, absIsModule := moduleRoots[absDir]; absIsModule {
		return append(typedFindings, untypedFindings...), nil
	}
	err := filepath.WalkDir(absDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}

		name := d.Name()
		if d.IsDir() {
			if shouldSkipWalkDir(name) {
				return filepath.SkipDir
			}
			if path != absDir {
				if _, isMod := moduleRoots[path]; isMod {
					return filepath.SkipDir
				}
			}
			return nil
		}

		if !strings.HasSuffix(name, ".go") {
			return nil
		}

		rel, err := filepath.Rel(absDir, path)
		if err != nil {
			rel = path
		}

		pf, err := parseGoFile(path, rel)
		if err != nil {
			if errors.Is(err, errParseFailed) {
				return nil
			}
			return err
		}
		if pf == nil {
			return nil
		}

		untypedFindings = append(untypedFindings, scanGoFileAST(pf, rules, opts)...)
		return nil
	})

	return append(typedFindings, untypedFindings...), err
}

// checkBuildFiles walks the tree rooted at dir and scans every file whose
// basename matches any active rule's file_patterns. The walk recurses into
// dotfile directories like .github/ because build-tag usage overwhelmingly
// lives in GitHub Actions workflows and similar hidden config paths; only
// vendor/ and node_modules/ are pruned.
//
// It is lenient about per-file errors: bad patterns, unreadable files, and
// per-rule scan failures are skipped rather than aborting the whole scan.
func checkBuildFiles(dir string, rules []CompiledRule, opts CheckOptions) []Finding {
	active := make([]*CompiledRule, 0, len(rules))
	for i := range rules {
		r := &rules[i]
		if shouldSkip(r, opts) {
			continue
		}
		if len(r.FilePatterns) == 0 {
			continue
		}
		active = append(active, r)
	}
	if len(active) == 0 {
		return nil
	}

	// Collect matching files in a cheap walk; defer the expensive bit
	// (opening + line-by-line regex) to a parallel stage.
	type job struct {
		path  string
		rel   string
		rules []*CompiledRule
	}
	var jobs []job
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if d != nil && d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if path != dir && (name == "vendor" || name == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}

		base := d.Name()
		var matched []*CompiledRule
		for _, r := range active {
			for _, pat := range r.FilePatterns {
				ok, mErr := filepath.Match(pat, base)
				if mErr == nil && ok {
					matched = append(matched, r)
					break
				}
			}
		}
		if len(matched) == 0 {
			return nil
		}

		rel, relErr := filepath.Rel(dir, path)
		if relErr != nil {
			rel = path
		}
		jobs = append(jobs, job{path: path, rel: rel, rules: matched})
		return nil
	})

	if len(jobs) == 0 {
		return nil
	}

	// Parallel scan: one open + one line-by-line pass per file, applying
	// every matched rule's patterns per line. The previous implementation
	// opened and re-scanned each file once per matching rule, which was
	// the dominant cost on trees full of *.yml / *.sh / Makefile hits.
	perJob := make([][]Finding, len(jobs))
	workers := min(runtime.GOMAXPROCS(0), len(jobs))
	var wg sync.WaitGroup
	ch := make(chan int)
	for range workers {
		wg.Go(func() {
			for i := range ch {
				ff, scanErr := scanFile(jobs[i].path, jobs[i].rel, jobs[i].rules)
				if scanErr != nil {
					fmt.Fprintf(os.Stderr, "jwxmigrate: warning: scanning %s: %v\n", jobs[i].rel, scanErr)
					continue
				}
				perJob[i] = ff
			}
		})
	}
	for i := range jobs {
		ch <- i
	}
	close(ch)
	wg.Wait()

	var findings []Finding
	for _, ff := range perJob {
		findings = append(findings, ff...)
	}
	return findings
}

// scanFile reads path once and applies every supplied rule's regex
// patterns to each line. Callers that pass N rules save N-1 file opens
// and N-1 line-by-line scans compared to the old per-rule helper —
// meaningful because rules commonly share file_patterns (e.g. *.sh is
// listed by four v3→v4 rules).
func scanFile(path, rel string, rules []*CompiledRule) ([]Finding, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var findings []Finding
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 0, 64*1024), maxScanBufferSize)
	lineNum := 0
	for scanner.Scan() {
		lineNum++
		line := scanner.Text()
		for _, r := range rules {
			for _, pat := range r.Patterns {
				if pat.MatchString(line) {
					findings = append(findings, newFileFinding(r, rel, lineNum, line))
					break
				}
			}
		}
	}
	return findings, scanner.Err()
}

func newFileFinding(r *CompiledRule, rel string, lineNum int, line string) Finding {
	f := Finding{
		RuleID:     r.ID,
		File:       rel,
		Line:       lineNum,
		Text:       strings.TrimSpace(line),
		SourceLine: line,
		Mechanical: r.Mechanical,
		Note:       strings.TrimSpace(r.Note),
		MatchedBy:  "regex",
	}
	if r.Example != nil {
		f.ExampleBefore = strings.TrimSpace(r.Example.Before)
		f.ExampleAfter = strings.TrimSpace(r.Example.After)
	}
	return f
}

// FormatText writes findings in human-readable text format.
func FormatText(w io.Writer, result *CheckResult) {
	for _, f := range result.Findings {
		label := "manual"
		if f.Mechanical {
			label = "auto"
		}
		_, _ = fmt.Fprintf(w, "[%s] (%s) %s:%d: %s\n", f.RuleID, label, f.File, f.Line, f.Text)
		if f.SourceLine != "" {
			_, _ = fmt.Fprintf(w, "  | %s\n", f.SourceLine)
		}
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
