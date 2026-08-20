package main

import (
	"fmt"
	"regexp"
	"strings"

	"golang.org/x/mod/modfile"
	"golang.org/x/mod/semver"
)

// targetMinimumVersion is set by loadRules from the rule set's
// minimum_target_version. It is the version written to a consumer's go.mod
// for the migration target when no fired rule declares a higher floor.
var targetMinimumVersion = "v4.0.0"

// majorSuffixPattern matches a module path's major version element ("v2",
// "v4", ...). v0 and v1 modules carry no such element.
var majorSuffixPattern = regexp.MustCompile(`^v[1-9][0-9]*$`)

// goDirectivePattern matches a go.mod `go` directive value: "1.27" or
// "1.27.0". A rule declares the language version it needs, never a toolchain
// patch release.
var goDirectivePattern = regexp.MustCompile(`^1\.[0-9]+(\.[0-9]+)?$`)

// modulePathOf reduces an import path to the module path that owns it, by
// cutting at the last major-version element. Returns "" when the string is
// not an import path at all, which is the common case for `replacement`
// fields that hold prose or a bare symbol name.
//
// Modules without a major suffix (v0 and v1) are not recognised. Every module
// this tool names is v2 or later, and guessing where a suffix-less path ends
// is not possible without a network lookup.
func modulePathOf(s string) string {
	s = strings.TrimSpace(s)
	if s == "" || strings.ContainsAny(s, " \t(),\"`") {
		return ""
	}
	parts := strings.Split(s, "/")
	if len(parts) < 2 || !strings.Contains(parts[0], ".") {
		return ""
	}
	for i := len(parts) - 1; i >= 1; i-- {
		if majorSuffixPattern.MatchString(parts[i]) {
			return strings.Join(parts[:i+1], "/")
		}
	}
	return ""
}

// checkMajorAgrees reports whether a version's major matches the major
// encoded in its module path. A /v4 path takes a v4.x.y version; catching the
// mismatch here is what stops a jwx version from being pasted onto a
// companion requirement.
func checkMajorAgrees(modulePath, version string) error {
	if !semver.IsValid(version) {
		return fmt.Errorf("version %q is not valid semver", version)
	}
	want := semver.Major(version)

	parts := strings.Split(modulePath, "/")
	last := parts[len(parts)-1]
	if majorSuffixPattern.MatchString(last) {
		if last != want {
			return fmt.Errorf("module %s is major %s but version %s is major %s", modulePath, last, version, want)
		}
		return nil
	}

	if want != "v0" && want != "v1" {
		return fmt.Errorf("module %s carries no major suffix so it can only take v0 or v1 versions, got %s", modulePath, version)
	}
	return nil
}

// checkGoDirective reports whether s is shaped like a go.mod `go` directive.
func checkGoDirective(s string) error {
	if !goDirectivePattern.MatchString(s) {
		return fmt.Errorf("requires.go %q must be a go directive value such as \"1.27\"", s)
	}
	return nil
}

// goDirectiveAtLeast reports whether the project's `go` directive satisfies
// the floor. An unreadable or absent directive is treated as not satisfying
// it, so a Go-gated rule stays silent rather than firing on a project whose
// version could not be established.
func goDirectiveAtLeast(projectGo, floor string) bool {
	if projectGo == "" || floor == "" {
		return false
	}
	return semver.Compare(goSemver(projectGo), goSemver(floor)) >= 0
}

// goSemver turns a `go` directive value into something semver.Compare can
// order. "1.27" becomes "v1.27.0"; semver requires all three components.
func goSemver(s string) string {
	n := strings.Count(s, ".")
	for ; n < 2; n++ {
		s += ".0"
	}
	return "v" + s
}

// requiredVersionFor returns the version to write for modulePath, given the
// rules that fired. It is the highest of the rule set baseline (for the
// migration target only) and every fired rule's declared floor for that path.
//
// Rules that did not fire are excluded on purpose. Taking the maximum across
// the whole rule set would drag every consumer up to the newest version any
// rule mentions, which is exactly what minimal version selection exists to
// avoid.
func requiredVersionFor(modulePath string, fired []CompiledRule) string {
	best := ""
	if modulePath == targetImportPrefix {
		best = targetMinimumVersion
	}
	for _, r := range fired {
		if r.Requires == nil {
			continue
		}
		for _, m := range r.Requires.Modules {
			if m.Path != modulePath {
				continue
			}
			if best == "" || semver.Compare(m.Version, best) > 0 {
				best = m.Version
			}
		}
	}
	return best
}

// unmetFloors returns the requirements among the fired rules that the
// project's current go.mod does not already satisfy. A module the project
// does not require at all is not reported here: adding it is the job of the
// rewrite, not of this diagnostic.
func unmetFloors(fired []CompiledRule, current map[string]string) []unmetFloor {
	var out []unmetFloor
	for _, r := range fired {
		if r.Requires == nil {
			continue
		}
		for _, m := range r.Requires.Modules {
			have, ok := current[m.Path]
			if !ok {
				continue
			}
			if semver.Compare(have, m.Version) >= 0 {
				continue
			}
			out = append(out, unmetFloor{
				RuleID:  r.ID,
				Path:    m.Path,
				Need:    m.Version,
				Current: have,
			})
		}
	}
	return out
}

// unmetFloor is one rule's unsatisfied module requirement.
type unmetFloor struct {
	RuleID  string
	Path    string
	Need    string
	Current string
}

// currentRequires reads a parsed go.mod into the path→version shape the
// floor helpers expect.
func currentRequires(mf *modfile.File) map[string]string {
	out := make(map[string]string, len(mf.Require))
	for _, req := range mf.Require {
		out[req.Mod.Path] = req.Mod.Version
	}
	return out
}

// companionRequirement is a companion module a fired rule points at, plus the
// version that rule declares for it.
type companionRequirement struct {
	ruleID  string
	version string
}

// companionRequirements returns the companion modules named by fired
// moved_to_extension rules that also declare a floor for them.
//
// A rule that names a companion but declares no floor contributes nothing: we
// would have to invent a version, and `go mod tidy` picking one is better than
// this tool guessing.
func companionRequirements(fired []CompiledRule) map[string]companionRequirement {
	out := map[string]companionRequirement{}
	for _, r := range fired {
		if r.Kind != kindMovedToExtension || r.Requires == nil {
			continue
		}
		target := modulePathOf(r.ExtensionModule)
		if target == "" || target == targetImportPrefix {
			// Sub-packages of the migration target are not separate
			// modules; step 1 and 2 already cover that require.
			continue
		}
		for _, m := range r.Requires.Modules {
			if m.Path != target {
				continue
			}
			if cur, ok := out[target]; !ok || semver.Compare(m.Version, cur.version) > 0 {
				out[target] = companionRequirement{ruleID: r.ID, version: m.Version}
			}
		}
	}
	return out
}
