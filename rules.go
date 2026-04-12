package main

import (
	_ "embed"
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

//go:embed v3-to-v4.yaml
var v3ToV4RulesYAML []byte

//go:embed v2-to-v4.yaml
var v2ToV4RulesYAML []byte

// migrations maps migration names to their embedded rule data.
var migrations = map[string][]byte{
	"v3-to-v4": v3ToV4RulesYAML,
	"v2-to-v4": v2ToV4RulesYAML,
}

// RuleSet is the top-level structure of a migration YAML file.
type RuleSet struct {
	SchemaVersion string `yaml:"schema_version"`
	From          string `yaml:"from"`
	To            string `yaml:"to"`
	Rules         []Rule `yaml:"rules"`
}

// Rule is a single migration rule.
// The Old/New fields are populated from whichever version-specific YAML keys
// are present (v2/v4 or v3/v4).
type Rule struct {
	ID              string   `yaml:"id"`
	Kind            string   `yaml:"kind"`
	Package         string   `yaml:"package"`
	Mechanical      bool     `yaml:"mechanical"`
	V2              string   `yaml:"v2,omitempty"`
	V3              string   `yaml:"v3,omitempty"`
	V4              string   `yaml:"v4,omitempty"`
	V2Signature     string   `yaml:"v2_signature,omitempty"`
	V3Signature     string   `yaml:"v3_signature,omitempty"`
	V4Signature     string   `yaml:"v4_signature,omitempty"`
	Replacement     string   `yaml:"replacement,omitempty"`
	ExtensionModule string   `yaml:"extension_module,omitempty"`
	SearchPatterns  []string `yaml:"search_patterns,omitempty"`
	CompilerHints   []string `yaml:"compiler_hints,omitempty"`
	FilePatterns    []string `yaml:"file_patterns,omitempty"`
	Note            string   `yaml:"note"`
	Example         *Example `yaml:"example,omitempty"`
}

// FromVersion returns the source version identifier (v2 or v3 field, whichever is set).
func (r *Rule) FromVersion() string {
	if r.V2 != "" {
		return r.V2
	}
	return r.V3
}

// ToVersion returns the target version identifier (v4 field).
func (r *Rule) ToVersion() string {
	return r.V4
}

// FromSignature returns the source version signature.
func (r *Rule) FromSignature() string {
	if r.V2Signature != "" {
		return r.V2Signature
	}
	return r.V3Signature
}

// Example holds before/after code snippets.
type Example struct {
	Before string `yaml:"before"`
	After  string `yaml:"after"`
}

// CompiledRule is a Rule with pre-compiled search patterns and AST matchers.
type CompiledRule struct {
	Rule

	Patterns    []*regexp.Regexp
	ASTMatchers []ASTMatcher
}

func loadRules(migration string) ([]CompiledRule, error) {
	data, ok := migrations[migration]
	if !ok {
		return nil, fmt.Errorf("unknown migration %q; available: v3-to-v4, v2-to-v4", migration)
	}

	var rs RuleSet
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("failed to parse migration rules: %w", err)
	}

	sourceImportPrefix = rs.From

	compiled := make([]CompiledRule, 0, len(rs.Rules))
	for _, r := range rs.Rules {
		cr := CompiledRule{Rule: r}
		for _, p := range r.SearchPatterns {
			re, err := regexp.Compile(p)
			if err != nil {
				return nil, fmt.Errorf("rule %s: invalid search pattern %q: %w", r.ID, p, err)
			}
			cr.Patterns = append(cr.Patterns, re)
		}
		cr.ASTMatchers = deriveASTMatchers(&r)
		compiled = append(compiled, cr)
	}

	return compiled, nil
}
