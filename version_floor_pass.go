package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
)

// moduleStateCache reads each module's go.mod at most once per run.
type moduleStateCache struct {
	byFile map[string]moduleState
	byRoot map[string]moduleState
}

func newModuleStateCache() *moduleStateCache {
	return &moduleStateCache{
		byFile: map[string]moduleState{},
		byRoot: map[string]moduleState{},
	}
}

// forFile returns the state of the module owning file, and whether the file
// is inside a module at all.
func (c *moduleStateCache) forFile(file string) (moduleState, bool) {
	dir := filepath.Dir(file)
	if st, ok := c.byFile[dir]; ok {
		return st, st.Root != ""
	}
	st, ok := moduleStateFor(file)
	if !ok {
		c.byFile[dir] = moduleState{}
		return moduleState{}, false
	}
	c.byFile[dir] = st
	c.byRoot[st.Root] = st
	return st, true
}

// applyVersionFloors post-processes raw findings against each project
// module's go.mod. It does two things:
//
//   - Drops findings from rules gated on a newer `go` directive than the
//     owning module declares. Their guidance cannot be acted on there.
//   - Appends one finding per unsatisfied module version floor, so a project
//     already on the target major but behind a rule's floor is told so.
//
// Rules are matched to findings by ID, so only rules that actually fired
// contribute floors.
//
// dir is the scanned root. Finding.File is relative to it, so resolving
// against dir rather than the process working directory is what makes the
// go.mod lookup find the consumer's module instead of jwxmigrate's own.
func applyVersionFloors(dir string, findings []Finding, rules []CompiledRule) []Finding {
	byID := make(map[string]*CompiledRule, len(rules))
	for i := range rules {
		byID[rules[i].ID] = &rules[i]
	}

	cache := newModuleStateCache()
	resolve := func(file string) string {
		if filepath.IsAbs(file) {
			return file
		}
		return filepath.Join(dir, file)
	}

	kept := make([]Finding, 0, len(findings))
	// fired tracks, per module root, the set of rule IDs that survived
	// gating in that module.
	fired := map[string]map[string]struct{}{}

	for _, f := range findings {
		r, ok := byID[f.RuleID]
		if !ok {
			kept = append(kept, f)
			continue
		}

		st, inModule := cache.forFile(resolve(f.File))

		if r.Requires != nil && r.Requires.Go != "" {
			if !inModule || !goDirectiveAtLeast(st.GoVersion, r.Requires.Go) {
				continue
			}
		}

		kept = append(kept, f)

		if inModule {
			if fired[st.Root] == nil {
				fired[st.Root] = map[string]struct{}{}
			}
			fired[st.Root][f.RuleID] = struct{}{}
		}
	}

	kept = append(kept, floorFindings(dir, cache, fired, byID)...)
	return kept
}

// floorFindings builds the diagnostics for unmet floors, one per
// (module root, rule, required module).
func floorFindings(dir string, cache *moduleStateCache, fired map[string]map[string]struct{}, byID map[string]*CompiledRule) []Finding {
	roots := make([]string, 0, len(fired))
	for root := range fired {
		roots = append(roots, root)
	}
	sort.Strings(roots)

	var out []Finding
	for _, root := range roots {
		st := cache.byRoot[root]

		ids := make([]string, 0, len(fired[root]))
		for id := range fired[root] {
			ids = append(ids, id)
		}
		sort.Strings(ids)

		firedRules := make([]CompiledRule, 0, len(ids))
		for _, id := range ids {
			if r, ok := byID[id]; ok {
				firedRules = append(firedRules, *r)
			}
		}

		// Report the go.mod the same way the scanner reports source
		// files: relative to the scanned root when it sits underneath it.
		goMod := filepath.Join(root, goModFilename)
		if rel, err := filepath.Rel(dir, goMod); err == nil && !strings.HasPrefix(rel, "..") {
			goMod = rel
		}
		for _, u := range unmetFloors(firedRules, st.Requires) {
			out = append(out, Finding{
				RuleID:          u.RuleID,
				File:            goMod,
				Line:            st.RequireLines[u.Path],
				Text:            fmt.Sprintf("%s %s", u.Path, u.Current),
				Mechanical:      true,
				Note:            floorNote(u),
				RequiresModule:  u.Path,
				RequiresVersion: u.Need,
				CurrentVersion:  u.Current,
				MatchedBy:       "version-floor",
			})
		}
	}
	return out
}

func floorNote(u unmetFloor) string {
	return fmt.Sprintf(
		"go.mod requires %s %s, but rule %s needs %s. The migration is not complete until this is raised; `jwxmigrate --fix` will do it.",
		u.Path, u.Current, u.RuleID, u.Need,
	)
}
