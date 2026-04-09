package main

import (
	"bytes"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCheckV3Sample(t *testing.T) {
	rules, err := loadRules()
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{})
	require.NoError(t, err)

	// The v3_sample.go file should trigger several rules.
	require.NotEmpty(t, result.Findings, "expected findings from v3_sample.go")
	require.Greater(t, result.Total, 0)

	// Collect rule IDs found.
	foundRules := make(map[string]struct{})
	for _, f := range result.Findings {
		foundRules[f.RuleID] = struct{}{}
		// All findings should come from v3_sample.go (v4_clean.go has no v3 imports).
		require.Equal(t, "v3_sample.go", f.File, "unexpected file in findings")
	}

	// These rules should definitely be triggered by v3_sample.go.
	expected := []string{
		"import-v3-to-v4",
		"get-to-field",
		"readfile-to-parsefs",
		"jwk-import-generic",
		"jwk-parsekey-generic",
		"register-custom-field-generic",
		"jws-signer2-to-signer",
		"jwk-cache-removed",
		"remove-decodersettings",
		"remove-withusenumber",
	}
	for _, id := range expected {
		_, ok := foundRules[id]
		require.True(t, ok, "expected rule %s to trigger, but it did not. found: %v", id, foundRules)
	}
}

func TestCheckV4Clean(t *testing.T) {
	// Create a temp dir with only the clean file — but v4_clean.go
	// doesn't import v3, so scanning testdata/ should produce no
	// findings from v4_clean.go.
	rules, err := loadRules()
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{})
	require.NoError(t, err)

	for _, f := range result.Findings {
		require.NotEqual(t, "v4_clean.go", f.File,
			"v4_clean.go should not produce any findings, but got rule %s on line %d", f.RuleID, f.Line)
	}
}

func TestCheckMechanicalFilter(t *testing.T) {
	rules, err := loadRules()
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{MechanicalOnly: true})
	require.NoError(t, err)

	for _, f := range result.Findings {
		require.True(t, f.Mechanical,
			"with --mechanical, finding %s should be mechanical", f.RuleID)
	}
}

func TestCheckRuleFilter(t *testing.T) {
	rules, err := loadRules()
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{RuleID: "import-v3-to-v4"})
	require.NoError(t, err)

	for _, f := range result.Findings {
		require.Equal(t, "import-v3-to-v4", f.RuleID,
			"with --rule filter, only import-v3-to-v4 should appear")
	}
	require.NotEmpty(t, result.Findings, "expected at least one import-v3-to-v4 finding")
}

func TestFormatJSON(t *testing.T) {
	rules, err := loadRules()
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{})
	require.NoError(t, err)

	var buf bytes.Buffer
	require.NoError(t, FormatJSON(&buf, result))

	// Verify it's valid JSON that decodes to CheckResult.
	var decoded CheckResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Equal(t, result.Total, decoded.Total)
	require.Equal(t, len(result.Findings), len(decoded.Findings))
}

func TestFormatText(t *testing.T) {
	rules, err := loadRules()
	require.NoError(t, err)

	result, err := Check("testdata", rules, CheckOptions{})
	require.NoError(t, err)

	var buf bytes.Buffer
	FormatText(&buf, result)

	output := buf.String()
	require.Contains(t, output, "Summary:")
	require.Contains(t, output, "import-v3-to-v4")
}
