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
		"build-tag-goccy":                      true,
		"build-tag-asmbase64":                  true,
		"build-tag-es256k":                     true,
		"build-tag-secp256k1-pem":              true,
		"rename-decodersettings-to-settings":   true,
		"get-to-field":                         true,
		"register-custom-field-generic":        true,
		"register-custom-decoder-generic":      true,
		"remove-readfileoption":                true,
		"remove-withfs":                        true,
		"jwk-import-generic":                   true,
		"jwk-parsekey-generic":                 true,
		"jwk-register-key-importer":            true,
		"jwk-cacheoption-removed":              true,
		"jwk-resourceoption-removed":           true,
		"jwk-registeroption-removed":           true,
		"jwk-registerfetchoption-removed":      true,
		"jwk-withhttprcresourceoption-removed": true,
		"jwk-withconstantinterval-removed":     true,
		"jwk-withmininterval-removed":          true,
		"jwk-withmaxinterval-removed":          true,
		"jwk-set-iterator":                     true,
		"jwk-keyimporter-type-removed":         true,
		"jwk-registerprobefield-generic":       true,
		"jwk-export-generic":                   true,
		"jws-verifier2-to-verifier":            true,
		"jws-register-signer":                  true,
		"jws-register-verifier":                true,
		"jws-signerfactory-removed":            true,
		"jws-signerfactoryfn-removed":          true,
		"jws-signeradapter-removed":            true,
		"jws-verifierfactory-removed":          true,
		"jws-verifierfactoryfn-removed":        true,
		"jws-verifideradapter-removed":         true,
		"jws-withlegacysigners-removed":        true,
		"jws-signerfor-return-type":            true,
		"jws-verifierfor-return-type":          true,
		"jws-withkey-early-validation":         true,
		"jws-splitcompact-moved":               true,
		"jws-legacy-package-removed":           true,
		"jwe-remove-legacy-header-merging":     true,
		"jwa-es256k-extension":                 true,
		"jwa-secp256k1-extension":              true,
		"jwa-ed448-extension":                  true,
		"jwa-eddsaed448-extension":             true,
		"jwt-token-claims-iterator":            true,
		"jwt-withstrictbase64encoding-default": true,
		"asmbase64-extension":                  true,
		"dep-blackmagic-removed":               true,
		"dep-goccy-removed":                    true,
		"dep-segmentio-removed":                true,
		"dep-option-v2-to-v3":                  true,
	},
	// v2-to-v4: deferred to final PR. Populated via init() below to keep
	// this map legible.
	"v2-to-v4": {},
}

// v2ToV4Exemptions lists every v2-to-v4 rule ID that is temporarily exempt.
// Shrink this list as v2 fixtures are added.
var v2ToV4Exemptions = []string{
	"import-v2-to-v4",
	"build-go-version-v2",
	"build-tag-goccy-v2",
	"build-tag-asmbase64-v2",
	"build-tag-es256k-v2",
	"build-tag-secp256k1-pem-v2",
	"rename-decodersettings-to-settings-v2",
	"get-to-field-v2",
	"accessor-return-type-v2",
	"register-custom-field-generic-v2",
	"readfile-to-parsefs-v2",
	"remove-readfileoption-v2",
	"remove-withfs-v2",
	"jwa-es256k-extension-v2",
	"jwa-secp256k1-extension-v2",
	"jwa-ed448-extension-v2",
	"jwk-fromraw-to-import-v2",
	"jwk-key-raw-to-export-v2",
	"jwk-import-generic-v2",
	"jwk-parsekey-generic-v2",
	"jwk-register-key-importer-v2",
	"jwk-cache-removed-v2",
	"jwk-autorefresh-removed-v2",
	"jwk-cacheoption-removed-v2",
	"jwk-resourceoption-removed-v2",
	"jwk-registeroption-removed-v2",
	"jwk-set-iterator-v2",
	"jwk-keyimporter-type-removed-v2",
	"jwk-certificatechain-removed-v2",
	"jwk-x25519-removed-v2",
	"jwk-setglobalfetcher-removed-v2",
	"jws-signer2-to-signer-v2",
	"jws-verifier2-to-verifier-v2",
	"jws-withverify-removed-v2",
	"jws-verifyauto-removed-v2",
	"jws-withpayloadsigner-removed-v2",
	"jws-signerfactory-removed-v2",
	"jws-signerfactoryfn-removed-v2",
	"jws-signeradapter-removed-v2",
	"jws-verifierfactory-removed-v2",
	"jws-verifierfactoryfn-removed-v2",
	"jws-verifideradapter-removed-v2",
	"jws-withlegacysigners-removed-v2",
	"jws-isxxxerror-removed-v2",
	"jws-withkey-early-validation-v2",
	"jwe-decryptencryptoption-renamed-v2",
	"jwe-message-decrypt-removed-v2",
	"jwe-json-removed-v2",
	"jwe-withpostparser-removed-v2",
	"jwe-remove-legacy-header-merging-v2",
	"jwe-isxxxerror-removed-v2",
	"jwt-withverify-false-to-parseinsecure-v2",
	"jwt-withdecrypt-removed-v2",
	"jwt-withjweheaders-removed-v2",
	"jwt-withheaders-removed-v2",
	"jwt-withjwsheaders-removed-v2",
	"jwt-usedefault-removed-v2",
	"jwt-inferalgorithmfromkey-removed-v2",
	"jwt-withkeysetprovider-removed-v2",
	"jwt-errinvalidjwt-renamed-v2",
	"jwt-errmissingrequiredclaim-removed-v2",
	"jwt-token-claims-iterator-v2",
	"jwt-time-validation-behavioral-v2",
	"jwt-field-presence-behavioral-v2",
	"jwk-cache-extension-v2",
	"asmbase64-extension-v2",
	"dep-pkg-errors-removed-v2",
	"dep-blackmagic-removed-v2",
	"dep-goccy-removed-v2",
	"dep-segmentio-removed-v2",
	"dep-option-v1-to-v3",
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
		if e.IsDir() {
			m[e.Name()] = true
		}
	}
	return m
}
