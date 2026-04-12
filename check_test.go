package main

import (
	"bytes"
	"encoding/json"
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
