package main

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestLoadRules(t *testing.T) {
	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)
	require.NotEmpty(t, rules)

	// Every rule must have an id and a note.
	ids := make(map[string]struct{})
	for _, r := range rules {
		require.NotEmpty(t, r.ID, "rule missing id")
		require.NotEmpty(t, r.Kind, "rule %s missing kind", r.ID)
		require.NotEmpty(t, r.Package, "rule %s missing package", r.ID)
		require.NotEmpty(t, r.Note, "rule %s missing note", r.ID)

		// IDs must be unique.
		_, dup := ids[r.ID]
		require.False(t, dup, "duplicate rule id: %s", r.ID)
		ids[r.ID] = struct{}{}

		// All search patterns must be valid (they compiled without error).
		// This is implicitly verified by loadRules, but let's be explicit.
		for _, p := range r.SearchPatterns {
			require.NotEmpty(t, p, "rule %s has empty search pattern", r.ID)
		}
	}
}

func TestRuleKinds(t *testing.T) {
	validKinds := map[string]struct{}{
		"import_change":      {},
		"signature_change":   {},
		"rename":             {},
		"removed":            {},
		"behavioral":         {},
		"type_change":        {},
		"moved_to_extension": {},
		"build_change":       {},
	}

	for _, migration := range []string{"v3-to-v4", "v2-to-v4"} {
		t.Run(migration, func(t *testing.T) {
			rules, err := loadRules(migration)
			require.NoError(t, err)

			for _, r := range rules {
				_, ok := validKinds[r.Kind]
				require.True(t, ok, "rule %s has invalid kind %q", r.ID, r.Kind)
			}
		})
	}
}

func TestLoadV2ToV4Rules(t *testing.T) {
	rules, err := loadRules("v2-to-v4")
	require.NoError(t, err)
	require.NotEmpty(t, rules)

	ids := make(map[string]struct{})
	for _, r := range rules {
		require.NotEmpty(t, r.ID, "rule missing id")
		require.NotEmpty(t, r.Kind, "rule %s missing kind", r.ID)
		require.NotEmpty(t, r.Note, "rule %s missing note", r.ID)

		_, dup := ids[r.ID]
		require.False(t, dup, "duplicate rule id: %s", r.ID)
		ids[r.ID] = struct{}{}
	}

	// Verify the import prefix was set correctly.
	require.Equal(t, "github.com/lestrrat-go/jwx/v2", sourceImportPrefix)
}
