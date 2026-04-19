package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// Unit tests for the FormatText / FormatJSON output layer. Behavioral checks
// against real fixtures live in TestRulesFixtures and TestEdgeCases.

func TestFormatJSON(t *testing.T) {
	result := &CheckResult{
		Total:      2,
		Mechanical: 1,
		Judgment:   1,
		Findings: []Finding{
			{RuleID: "a", File: "a.go", Line: 1, Mechanical: true, Note: "note a"},
			{RuleID: "b", File: "b.go", Line: 2, Mechanical: false, Note: "note b"},
		},
	}
	var buf bytes.Buffer
	require.NoError(t, FormatJSON(&buf, result))

	var decoded CheckResult
	require.NoError(t, json.Unmarshal(buf.Bytes(), &decoded))
	require.Equal(t, result.Total, decoded.Total)
	require.Equal(t, len(result.Findings), len(decoded.Findings))
	require.Equal(t, "a", decoded.Findings[0].RuleID)
}

func TestFormatText(t *testing.T) {
	result := &CheckResult{
		Total:      1,
		Mechanical: 1,
		Findings: []Finding{
			{RuleID: "import-v3-to-v4", File: "a.go", Line: 3, Mechanical: true, Note: "update import"},
		},
	}
	var buf bytes.Buffer
	FormatText(&buf, result)

	output := buf.String()
	require.Contains(t, output, "Summary:")
	require.Contains(t, output, "import-v3-to-v4")
	require.Contains(t, output, "a.go:3")
}

// TestFormatText_IncludesSourceLine pins that when a finding has a
// SourceLine attached, FormatText renders it under the header so the
// user can see exactly which source line triggered the rule without
// opening the file.
func TestFormatText_IncludesSourceLine(t *testing.T) {
	result := &CheckResult{
		Total:      1,
		Mechanical: 1,
		Findings: []Finding{
			{
				RuleID:     "import-v3-to-v4",
				File:       "a.go",
				Line:       3,
				Mechanical: true,
				Note:       "update import",
				SourceLine: `import "github.com/lestrrat-go/jwx/v3/jwk"`,
			},
		},
	}
	var buf bytes.Buffer
	FormatText(&buf, result)

	require.Contains(t, buf.String(), `import "github.com/lestrrat-go/jwx/v3/jwk"`,
		"FormatText must echo the source line that triggered the finding")
}

// TestCheck_PopulatesSourceLine pins end-to-end that running Check on
// an actual Go file fills in each finding's SourceLine with the exact
// on-disk line content — not the node fragment stored in Text. This
// keeps the diagnostic useful even when the triggering construct spans
// a single line with surrounding context the user wants to see.
func TestCheck_PopulatesSourceLine(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	dir := t.TempDir()
	src := "package x\n\nimport \"github.com/lestrrat-go/jwx/v3/jwk\"\n\nvar _ = jwk.Key(nil)\n"
	require.NoError(t, os.WriteFile(filepath.Join(dir, "a.go"), []byte(src), 0o644))

	result, err := Check(dir, rules, CheckOptions{})
	require.NoError(t, err)
	require.NotEmpty(t, result.Findings)

	var importFinding *Finding
	for i := range result.Findings {
		if result.Findings[i].RuleID == "import-v3-to-v4" {
			importFinding = &result.Findings[i]
			break
		}
	}
	require.NotNil(t, importFinding, "expected import-v3-to-v4 finding")
	require.Equal(t, `import "github.com/lestrrat-go/jwx/v3/jwk"`, importFinding.SourceLine)
}
