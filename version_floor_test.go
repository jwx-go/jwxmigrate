package main

import (
	"testing"

	"github.com/stretchr/testify/require"
	"golang.org/x/mod/semver"
)

// knownModulePaths returns every module path the rule set is allowed to
// declare a requirement against: the migration target itself, plus any
// module named by a rule's extension_module or replacement field.
func knownModulePaths(t *testing.T, migration string) map[string]struct{} {
	t.Helper()

	rs, err := parseRuleSet(migration)
	require.NoError(t, err, "parse %s", migration)

	known := map[string]struct{}{rs.To: {}}
	for _, r := range rs.Rules {
		for _, p := range []string{r.ExtensionModule, r.Replacement} {
			if modulePathOf(p) != "" {
				known[modulePathOf(p)] = struct{}{}
			}
		}
	}
	return known
}

func TestVersionFloors(t *testing.T) {
	for _, migration := range []string{migrationV3ToV4, migrationV2ToV4} {
		t.Run(migration, func(t *testing.T) {
			rs, err := parseRuleSet(migration)
			require.NoError(t, err)

			t.Run("baseline is valid for the target module", func(t *testing.T) {
				require.NotEmpty(t, rs.MinimumTargetVersion,
					"minimum_target_version must be declared")
				require.True(t, semver.IsValid(rs.MinimumTargetVersion),
					"minimum_target_version %q is not valid semver", rs.MinimumTargetVersion)
				require.NoError(t, checkMajorAgrees(rs.To, rs.MinimumTargetVersion))
			})

			known := knownModulePaths(t, migration)

			for _, r := range rs.Rules {
				if r.Requires == nil {
					continue
				}
				t.Run(r.ID, func(t *testing.T) {
					for _, m := range r.Requires.Modules {
						require.True(t, semver.IsValid(m.Version),
							"version %q is not valid semver", m.Version)
						require.NoError(t, checkMajorAgrees(m.Path, m.Version),
							"module major and version disagree")

						_, ok := known[m.Path]
						require.True(t, ok,
							"module path %q is not the migration target and is named by no rule's extension_module or replacement", m.Path)

						// A floor at or below the baseline says nothing the
						// baseline does not already guarantee, and it hides
						// the fact that the baseline is what actually applies.
						if m.Path == rs.To {
							require.Equal(t, 1, semver.Compare(m.Version, rs.MinimumTargetVersion),
								"floor %s is not above minimum_target_version %s; drop it or raise the baseline",
								m.Version, rs.MinimumTargetVersion)
						}
					}
					if r.Requires.Go != "" {
						require.NoError(t, checkGoDirective(r.Requires.Go))
					}
				})
			}
		})
	}
}

func TestVersionFloorsMinimumTargetVersionCoversUndeclaredRules(t *testing.T) {
	// Every rule that declares no floor for the target module is promising
	// that minimum_target_version is enough for it. Nothing here can prove
	// that automatically; agents/docs/version-floors.md carries the sweep
	// that checks it against the target module's history. This test pins the
	// weaker invariant that the promise is at least well-formed.
	for _, migration := range []string{migrationV3ToV4, migrationV2ToV4} {
		rs, err := parseRuleSet(migration)
		require.NoError(t, err)
		for _, r := range rs.Rules {
			require.NotEmpty(t, r.ID, "every rule needs an id")
		}
		require.True(t, semver.IsValid(rs.MinimumTargetVersion), migration)
	}
}
