package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFixRemaining_NestedRemovedCallsReported covers the previous silent
// failure where mechanical `removed` rules matched inside composite literal
// elements were reported in Check but invisibly dropped from FixFile's
// Remaining list (because the old logic blanket-skipped mechanical findings).
// After the per-location tracking fix, every call site that Check matches
// but Fix couldn't rewrite must show up in Remaining.
func TestFixRemaining_NestedRemovedCallsReported(t *testing.T) {
	src := `package example

import (
	"time"

	"github.com/lestrrat-go/jwx/v3/jwk"
)

func opts() []jwk.RegisterOption {
	return []jwk.RegisterOption{
		jwk.WithConstantInterval(15 * time.Minute),
		jwk.WithMinInterval(5 * time.Minute),
		jwk.WithMaxInterval(60 * time.Minute),
	}
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := FixFile(path, rules)
	require.NoError(t, err)
	require.NotNil(t, result)

	// Each WithXInterval call is a mechanical:true removed rule whose
	// delete-statement fixer cannot touch it (no statement parent — the
	// call is an element of a composite literal). Every one of those
	// findings must end up in Remaining.
	need := map[string]bool{
		"jwk-withconstantinterval-removed": false,
		"jwk-withmininterval-removed":      false,
		"jwk-withmaxinterval-removed":      false,
	}
	for _, f := range result.Remaining {
		if _, ok := need[f.RuleID]; ok {
			need[f.RuleID] = true
		}
	}
	for id, got := range need {
		require.True(t, got, "rule %s should be in Remaining but was silently dropped", id)
	}
}

// TestFixRemaining_FixedCallsAbsentFromRemaining verifies the other half of
// the fix: a mechanical rule whose fixer DID emit an edit must not show up
// in Remaining. Uses import-v3-to-v4 which is the canonical mechanical rule
// with a working fixer.
func TestFixRemaining_FixedCallsAbsentFromRemaining(t *testing.T) {
	src := `package example

import (
	"github.com/lestrrat-go/jwx/v3/jwk"
)

var _ = jwk.RSAPrivateKey(nil)
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := FixFile(path, rules)
	require.NoError(t, err)
	require.NotNil(t, result)

	for _, f := range result.Remaining {
		require.NotEqual(t, "import-v3-to-v4", f.RuleID,
			"import-v3-to-v4 should be applied, not listed as remaining: %v", f)
	}
}

// TestFixRemaining_PartialFixReportsUnfixedInstances confirms per-location
// (not per-rule) tracking: when the same rule matches multiple call sites
// and only some can be rewritten, the unfixed instances must appear in
// Remaining while the fixed ones must not.
//
// Uses readfile-to-parsefs: it rewrites string-literal paths but skips
// dynamic args (returning []Edit{} to suppress the name-only fallback).
func TestFixRemaining_PartialFixReportsUnfixedInstances(t *testing.T) {
	src := `package example

import (
	"github.com/lestrrat-go/jwx/v3/jwt"
)

func load(dynamic string) {
	_, _ = jwt.ReadFile("static.jwt")
	_, _ = jwt.ReadFile(dynamic)
}
`
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	require.NoError(t, os.WriteFile(path, []byte(src), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	result, err := FixFile(path, rules)
	require.NoError(t, err)
	require.NotNil(t, result)

	var readfileRemaining []Finding
	for _, f := range result.Remaining {
		if f.RuleID == readfileToParseFSRuleID {
			readfileRemaining = append(readfileRemaining, f)
		}
	}
	require.Len(t, readfileRemaining, 1,
		"the dynamic-arg ReadFile call must remain; the literal-arg call must not")

	out, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Contains(t, string(out), `jwt.ParseFS(os.DirFS("."), "static.jwt")`,
		"the literal-arg call should have been rewritten")
	require.Contains(t, string(out), `jwt.ReadFile(dynamic)`,
		"the dynamic-arg call should be preserved verbatim")
}
