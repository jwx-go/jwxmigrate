package main

import (
	_ "embed"
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

//go:embed v3-to-v4.yaml
var v3ToV4RulesYAML []byte

// RuleSet is the top-level structure of a migration YAML file.
type RuleSet struct {
	SchemaVersion string `yaml:"schema_version"`
	From          string `yaml:"from"`
	To            string `yaml:"to"`
	Rules         []Rule `yaml:"rules"`
}

// Rule is a single migration rule.
type Rule struct {
	ID              string   `yaml:"id"`
	Kind            string   `yaml:"kind"`
	Package         string   `yaml:"package"`
	Mechanical      bool     `yaml:"mechanical"`
	V3              string   `yaml:"v3,omitempty"`
	V4              string   `yaml:"v4,omitempty"`
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

// Example holds before/after code snippets.
type Example struct {
	Before string `yaml:"before"`
	After  string `yaml:"after"`
}

// CompiledRule is a Rule with pre-compiled search patterns.
type CompiledRule struct {
	Rule
	Patterns []*regexp.Regexp
}

func loadRules() ([]CompiledRule, error) {
	var rs RuleSet
	if err := yaml.Unmarshal(v3ToV4RulesYAML, &rs); err != nil {
		return nil, fmt.Errorf("failed to parse migration rules: %w", err)
	}

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
		compiled = append(compiled, cr)
	}

	return compiled, nil
}
