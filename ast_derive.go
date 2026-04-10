package main

import "strings"

// deriveASTMatchers builds AST matchers from existing rule fields.
// This avoids requiring YAML schema changes — matchers are inferred from
// the rule's kind, package, v3 name, and search_patterns.
func deriveASTMatchers(r *Rule) []ASTMatcher {
	switch r.Kind {
	case "import_change":
		return deriveImportChange(r)
	case "signature_change":
		return deriveSignatureChange(r)
	case "rename":
		return deriveRename(r)
	case "removed", "moved_to_extension":
		return deriveRemovedOrMoved(r)
	case "type_change":
		return deriveTypeChange(r)
	case "behavioral", "build_change":
		// No AST matchers — behavioral uses regex fallback,
		// build_change targets non-Go files only.
		return nil
	default:
		return nil
	}
}

func deriveImportChange(r *Rule) []ASTMatcher {
	path := r.FromVersion()
	if path == "" {
		// Fall back to first search pattern as the import path substring.
		if len(r.SearchPatterns) > 0 {
			path = r.SearchPatterns[0]
		}
	}
	if path == "" {
		return nil
	}
	return []ASTMatcher{{
		Kind:       MatchImportSpec,
		ImportPath: path,
	}}
}

func deriveSignatureChange(r *Rule) []ASTMatcher {
	name := r.FromVersion()
	if name == "" {
		return nil
	}

	if r.Package != "" && r.Package != "all" {
		// Package-qualified call: e.g. jwk.Import(...)
		return []ASTMatcher{{
			Kind:    MatchCallExpr,
			PkgName: r.Package,
			Name:    name,
		}}
	}

	// package == "all" — method call on any receiver: e.g. .Get(...)
	return []ASTMatcher{{
		Kind: MatchMethodCall,
		Name: name,
	}}
}

func deriveRename(r *Rule) []ASTMatcher {
	name := r.FromVersion()
	if name == "" {
		return nil
	}

	var matchers []ASTMatcher

	if r.Package != "" && r.Package != "all" {
		// Qualified reference: e.g. jws.Signer2
		matchers = append(matchers, ASTMatcher{
			Kind:    MatchSelectorExpr,
			PkgName: r.Package,
			Name:    name,
		})
	}

	// Also match bare identifier (for dot imports or within-package references).
	matchers = append(matchers, ASTMatcher{
		Kind: MatchIdent,
		Name: name,
	})

	return matchers
}

func deriveRemovedOrMoved(r *Rule) []ASTMatcher {
	// Determine whether this is a function call or a type/value reference
	// by checking if any search pattern contains `\(` (indicating a call).
	isCall := false
	for _, sp := range r.SearchPatterns {
		if strings.Contains(sp, `\(`) {
			isCall = true
			break
		}
	}

	// For rules with multiple distinct names in search patterns (e.g. jwk-cache-removed
	// matches NewCache, Cache, CachedSet, etc.), derive a matcher for each.
	names := extractNamesFromPatterns(r)
	if len(names) == 0 {
		// Fall back to v3 field.
		if r.FromVersion() != "" {
			names = []string{r.FromVersion()}
		} else {
			return nil
		}
	}

	var matchers []ASTMatcher
	for _, name := range names {
		if r.Package != "" && r.Package != "all" {
			if isCall {
				matchers = append(matchers, ASTMatcher{
					Kind:    MatchCallExpr,
					PkgName: r.Package,
					Name:    name,
				})
			}
			// Always add a selector matcher for type/value references.
			matchers = append(matchers, ASTMatcher{
				Kind:    MatchSelectorExpr,
				PkgName: r.Package,
				Name:    name,
			})
		} else {
			// package == "all"
			if isCall {
				matchers = append(matchers, ASTMatcher{
					Kind: MatchMethodCall,
					Name: name,
				})
			}
			matchers = append(matchers, ASTMatcher{
				Kind: MatchIdent,
				Name: name,
			})
		}
	}

	return matchers
}

func deriveTypeChange(r *Rule) []ASTMatcher {
	// Extract names from search patterns for type references.
	names := extractNamesFromPatterns(r)
	if len(names) == 0 && r.FromVersion() != "" {
		names = []string{r.FromVersion()}
	}

	var matchers []ASTMatcher
	for _, name := range names {
		if r.Package != "" && r.Package != "all" {
			matchers = append(matchers, ASTMatcher{
				Kind:    MatchSelectorExpr,
				PkgName: r.Package,
				Name:    name,
			})
		} else {
			matchers = append(matchers, ASTMatcher{
				Kind: MatchIdent,
				Name: name,
			})
		}
	}
	return matchers
}

// extractNamesFromPatterns pulls identifier names from search pattern regexes.
// Patterns typically look like `jwk\.Import\(` or `Signer2\b` — we extract
// the identifier that follows `\.` or stands alone before `\b` or `\(`.
func extractNamesFromPatterns(r *Rule) []string {
	seen := make(map[string]struct{})
	var names []string

	for _, sp := range r.SearchPatterns {
		name := extractNameFromPattern(sp)
		if name == "" {
			continue
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}

	return names
}

// extractNameFromPattern extracts an identifier from a single regex pattern.
// Handles common forms:
//   - `pkg\.Name\(` → "Name"
//   - `pkg\.Name\b` → "Name"
//   - `Name\(` → "Name"
//   - `Name\b` → "Name"
//   - `\.Name\(` → "Name"
func extractNameFromPattern(pat string) string {
	// Strip common regex suffixes.
	s := pat
	for _, suffix := range []string{`\(`, `\b`, `$`} {
		s = strings.TrimSuffix(s, suffix)
	}

	// If pattern contains `\.`, take the part after the last `\.`
	if idx := strings.LastIndex(s, `\.`); idx >= 0 {
		s = s[idx+2:]
	}

	// Strip any remaining regex characters.
	s = strings.TrimRight(s, `*+?|()[]{}^$.\`)

	// Validate it looks like a Go identifier.
	if s == "" {
		return ""
	}
	for _, c := range s {
		if !isIdentChar(c) {
			return ""
		}
	}
	return s
}

func isIdentChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
}
