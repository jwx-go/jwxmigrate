package main

import (
	_ "embed"
	"fmt"
	"regexp"

	"gopkg.in/yaml.v3"
)

// Migration name identifiers.
const (
	migrationV3ToV4 = "v3-to-v4"
	migrationV2ToV4 = "v2-to-v4"
)

//go:embed v3-to-v4.yaml
var v3ToV4RulesYAML []byte

//go:embed v2-to-v4.yaml
var v2ToV4RulesYAML []byte

// migrations maps migration names to their embedded rule data.
var migrations = map[string][]byte{
	migrationV3ToV4: v3ToV4RulesYAML,
	migrationV2ToV4: v2ToV4RulesYAML,
}

// RuleSet is the top-level structure of a migration YAML file.
type RuleSet struct {
	SchemaVersion string `yaml:"schema_version"`
	From          string `yaml:"from"`
	To            string `yaml:"to"`
	// MinimumTargetVersion is the version of To written to a consumer's
	// go.mod when no rule that fired asks for more. Rules that need newer
	// API raise it through their own Requires block.
	MinimumTargetVersion string `yaml:"minimum_target_version"`
	Rules                []Rule `yaml:"rules"`
}

// Requires expresses the preconditions a rule's guidance depends on. A nil
// *Requires means the rule applies unconditionally and the rule set's
// MinimumTargetVersion is enough for it.
type Requires struct {
	// Go is a `go` directive value such as "1.27". A rule carrying one is
	// suppressed entirely on projects below it, because its guidance cannot
	// be acted on there.
	Go string `json:"go,omitempty" yaml:"go,omitempty"`
	// Modules are the module versions the rule's guidance needs in order to
	// compile.
	Modules []ModuleRequirement `json:"modules,omitempty" yaml:"modules,omitempty"`
}

// ModuleRequirement is one module version floor.
type ModuleRequirement struct {
	Path    string `json:"path"    yaml:"path"`
	Version string `json:"version" yaml:"version"`
}

// Rule is a single migration rule.
// The Old/New fields are populated from whichever version-specific YAML keys
// are present (v2/v4 or v3/v4).
type Rule struct {
	ID      string `yaml:"id"`
	Kind    string `yaml:"kind"`
	Package string `yaml:"package"`
	// PackageImport names the import path that Package refers to, for a rule
	// targeting a package outside jwx. Without it a matcher can only resolve
	// local names against the file's jwx imports.
	PackageImport   string `yaml:"package_import,omitempty"`
	Mechanical      bool   `yaml:"mechanical"`
	V2              string `yaml:"v2,omitempty"`
	V3              string `yaml:"v3,omitempty"`
	V4              string `yaml:"v4,omitempty"`
	V2Signature     string `yaml:"v2_signature,omitempty"`
	V3Signature     string `yaml:"v3_signature,omitempty"`
	V4Signature     string `yaml:"v4_signature,omitempty"`
	Replacement     string `yaml:"replacement,omitempty"`
	ExtensionModule string `yaml:"extension_module,omitempty"`
	// AbsorbedInto is the import path that now provides what
	// ExtensionModule used to, for an extension_absorbed rule whose symbols
	// move rather than simply disappear.
	AbsorbedInto   string   `yaml:"absorbed_into,omitempty"`
	SearchPatterns []string `yaml:"search_patterns,omitempty"`
	CompilerHints  []string `yaml:"compiler_hints,omitempty"`
	FilePatterns   []string `yaml:"file_patterns,omitempty"`
	Note           string   `yaml:"note"`
	Example        *Example `yaml:"example,omitempty"`

	// Requires holds the rule's preconditions. See agents/docs/version-floors.md
	// for how to derive the values; they are a lookup against the target
	// module's history, never a guess.
	Requires *Requires `yaml:"requires,omitempty"`
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

// parseRuleSet unmarshals a migration's embedded YAML without compiling
// patterns or touching package state. Validation tests use it to inspect
// rules as authored.
func parseRuleSet(migration string) (*RuleSet, error) {
	data, ok := migrations[migration]
	if !ok {
		return nil, fmt.Errorf("unknown migration %q; available: v3-to-v4, v2-to-v4", migration)
	}

	var rs RuleSet
	if err := yaml.Unmarshal(data, &rs); err != nil {
		return nil, fmt.Errorf("failed to parse migration rules: %w", err)
	}
	return &rs, nil
}

func loadRules(migration string) ([]CompiledRule, error) {
	rs, err := parseRuleSet(migration)
	if err != nil {
		return nil, err
	}

	sourceImportPrefix = rs.From
	targetImportPrefix = rs.To
	targetMinimumVersion = rs.MinimumTargetVersion

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
