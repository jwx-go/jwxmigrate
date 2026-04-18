package main

import (
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/stretchr/testify/require"
)

// ruleCoverageExemptions is the shrinking list of rules that do not yet have
// a fixture under testdata/rules/<migration>/<rule-id>/. Each PR that adds
// fixtures must remove the corresponding entries here; the final PR deletes
// the exemption list entirely.
//
// Do NOT add entries to this list. If you see a new rule failing the coverage
// test, author a fixture under testdata/rules/<migration>/<rule-id>/ instead.
var ruleCoverageExemptions = map[string]map[string]bool{
	// v3-to-v4: pilot PR ships fixtures for one rule per kind. Everything
	// else is exempt and will be added in follow-up PRs.
	"v3-to-v4": {
		// jwk option/Cache* rules: realistic call-site coverage lives in
		// testdata/edge/jwk-cache-options which exercises NewCache plus
		// every With*/Option* symbol in a single scenario. Per-rule
		// fixtures would be trivial name-match stubs that test nothing.
		"jwk-cacheoption-removed":              true,
		"jwk-resourceoption-removed":           true,
		"jwk-registeroption-removed":           true,
		"jwk-registerfetchoption-removed":      true,
		"jwk-withhttprcresourceoption-removed": true,
		"jwk-withconstantinterval-removed":     true,
		"jwk-withmininterval-removed":          true,
		"jwk-withmaxinterval-removed":          true,
		// jws legacy signer/verifier factory+adapter subsystem: fully
		// exercised in context by testdata/edge/jws-legacy-signers. All
		// nine rules fire on the realistic registration chain there.
		"jws-register-signer":           true,
		"jws-register-verifier":         true,
		"jws-signerfactory-removed":     true,
		"jws-signerfactoryfn-removed":   true,
		"jws-signeradapter-removed":     true,
		"jws-verifierfactory-removed":   true,
		"jws-verifierfactoryfn-removed": true,
		"jws-verifideradapter-removed":  true,
		"jws-withlegacysigners-removed": true,
		// jwt-withstrictbase64encoding-default is a pure documentation
		// rule with no search_patterns: it advises users that the parse
		// path now enforces strict base64 decoding by default. It can
		// never fire on any code and therefore has no fixture.
		"jwt-withstrictbase64encoding-default": true,
		// asmbase64-extension has only the search pattern `asmbase64`
		// and no file_patterns, so deriveRemovedOrMoved emits a
		// MatchSelectorExpr that can only fire on `jwx.asmbase64` —
		// which is not a real symbol. The rule is effectively
		// unfireable on any realistic code today; users are instead
		// caught by build-tag-asmbase64 when they reference the old
		// build tag in a Makefile / shell script. Until the rule's
		// triggers are reworked (add file_patterns, or merge into
		// build-tag-asmbase64), there is no code that could exercise
		// it from a fixture.
		"asmbase64-extension": true,
	},
	// v2-to-v4: deferred to final PR. Populated via init() below to keep
	// this map legible.
	"v2-to-v4": {},
}

// v2ToV4Exemptions lists v2-to-v4 rule IDs that intentionally lack a
// per-rule fixture dir. Grouped by reason; shrink only by making the
// rules fireable or by replacing an edge fixture with per-rule coverage.
var v2ToV4Exemptions = []string{
	// jwk option-type rules: realistic call-site coverage lives in
	// testdata/edge/jwk-cache-options-v2 which exercises a v2
	// NewCache plus each Option type in a single scenario. Per-rule
	// fixtures would be trivial name-match stubs that test nothing.
	"jwk-cacheoption-removed-v2",
	"jwk-resourceoption-removed-v2",
	"jwk-registeroption-removed-v2",
	// jws legacy signer/verifier factory+adapter subsystem: exercised
	// in context by testdata/edge/jws-legacy-signers-v2. The seven
	// rules below fire there on the realistic registration chain.
	"jws-signerfactory-removed-v2",
	"jws-signerfactoryfn-removed-v2",
	"jws-signeradapter-removed-v2",
	"jws-verifierfactory-removed-v2",
	"jws-verifierfactoryfn-removed-v2",
	"jws-verifideradapter-removed-v2",
	"jws-withlegacysigners-removed-v2",
	// Unfireable-by-design: no search_patterns, or patterns that
	// can't match real Go code. Kept exempt until the rules are
	// reworked to have actionable triggers.
	"jwt-time-validation-behavioral-v2", // behavioral, no patterns
	"jwt-field-presence-behavioral-v2",  // behavioral, no patterns
	"asmbase64-extension-v2",            // bare "asmbase64" pattern never fires on Go
}

func init() {
	m := make(map[string]bool, len(v2ToV4Exemptions))
	for _, id := range v2ToV4Exemptions {
		m[id] = true
	}
	ruleCoverageExemptions["v2-to-v4"] = m
}

// TestEveryRuleHasFixture enumerates every loaded rule and asserts that a
// fixture directory exists for it, minus exemptions. Adding a rule without a
// fixture causes this test to fail.
func TestEveryRuleHasFixture(t *testing.T) {
	for _, migration := range []string{"v3-to-v4", "v2-to-v4"} {
		t.Run(migration, func(t *testing.T) {
			rules, err := loadRules(migration)
			require.NoError(t, err)

			// Slug used for the directory name: strip "-to-v4" → "v3" / "v2".
			slug := migration[:2]
			fixtureRoot := filepath.Join("testdata", "rules", slug)

			present := listFixtureDirs(t, fixtureRoot)

			var missing []string
			for _, r := range rules {
				if ruleCoverageExemptions[migration][r.ID] {
					continue
				}
				if !present[r.ID] {
					missing = append(missing, r.ID)
				}
			}
			sort.Strings(missing)
			require.Empty(t, missing,
				"%d rule(s) have no fixture under %s — author a fixture or (as a last resort) add to ruleCoverageExemptions in rules_coverage_test.go:\n  %v",
				len(missing), fixtureRoot, missing)

			// Reverse check: every fixture directory must correspond to a
			// real rule. Prevents dangling dirs when rules are removed.
			ruleIDs := make(map[string]bool, len(rules))
			for _, r := range rules {
				ruleIDs[r.ID] = true
			}
			var orphans []string
			for id := range present {
				if !ruleIDs[id] {
					orphans = append(orphans, id)
				}
			}
			sort.Strings(orphans)
			require.Empty(t, orphans,
				"%d fixture dir(s) under %s have no matching rule:\n  %v",
				len(orphans), fixtureRoot, orphans)
		})
	}
}

func listFixtureDirs(t *testing.T, root string) map[string]bool {
	t.Helper()
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return map[string]bool{}
	}
	require.NoError(t, err)
	m := make(map[string]bool, len(entries))
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		// A fixture dir counts as present if it contains either the
		// legacy input/ subdir or a fixture.txtar archive.
		fixtureDir := filepath.Join(root, e.Name())
		if _, err := os.Stat(filepath.Join(fixtureDir, "fixture.txtar")); err == nil {
			m[e.Name()] = true
			continue
		}
		if info, err := os.Stat(filepath.Join(fixtureDir, "input")); err == nil && info.IsDir() {
			m[e.Name()] = true
		}
	}
	return m
}
