package main

import (
	"regexp"
	"strings"
)

// identAfterDot matches an identifier that follows a regex-escaped dot (`\.`).
// Captures the identifier only.
var identAfterDot = regexp.MustCompile(`\\\.([A-Za-z_][A-Za-z0-9_]*)`)

// standaloneIdent matches a leading identifier (possibly after `^` or `\b`)
// followed by a regex boundary construct (`\(`, `\b`, `\)`, end, `(`).
// Used as a fallback when the pattern has no `\.`-qualified identifier.
var standaloneIdent = regexp.MustCompile(
	`(?:^|\\b|\^)([A-Za-z_][A-Za-z0-9_]*)(?:\\\(|\\b|\\\)|$|\()`,
)

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

	var matchers []ASTMatcher

	// Import-path shape: search patterns with a `/` in them name a package
	// path rather than an identifier. Emit an import matcher for each so
	// removed sub-packages (e.g. jwk/x25519) are caught structurally.
	for _, sp := range r.SearchPatterns {
		path := importPathFromPattern(sp)
		if path == "" {
			continue
		}
		matchers = append(matchers, ASTMatcher{
			Kind:       MatchImportSpec,
			ImportPath: path,
		})
	}

	// Wildcard-name shape: patterns like `jws\.Is\w+Error\(` target a family
	// of identifiers. Emit a NamePattern matcher so any matching name fires.
	for _, sp := range r.SearchPatterns {
		pkg, re := namePatternFromSearch(sp)
		if re == nil {
			continue
		}
		if pkg == "" && r.Package != "" && r.Package != "all" {
			pkg = r.Package
		}
		kind := MatchSelectorExpr
		if isCall {
			kind = MatchCallExpr
		}
		matchers = append(matchers, ASTMatcher{
			Kind:        kind,
			PkgName:     pkg,
			NamePattern: re,
		})
	}

	// For rules with multiple distinct names in search patterns (e.g. jwk-cache-removed
	// matches NewCache, Cache, CachedSet, etc.), derive a matcher for each.
	names := extractNamesFromPatterns(r)
	if len(names) == 0 && len(matchers) == 0 {
		// Fall back to v3 field.
		if r.FromVersion() != "" {
			names = []string{r.FromVersion()}
		} else {
			return nil
		}
	}

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

// importPathFromPattern returns the import path targeted by a search pattern,
// or "" if the pattern is not an import-path shape. Import-path patterns are
// those containing `/` but no regex metacharacters that would make them
// unsuitable as a substring match (e.g. `lestrrat-go/jwx/v3`, `jwk/x25519`).
func importPathFromPattern(pat string) string {
	if !strings.Contains(pat, "/") {
		return ""
	}
	// Reject patterns that look like identifier matchers (shouldn't happen
	// with `/` present, but be defensive).
	if strings.Contains(pat, `\(`) || strings.Contains(pat, `\b`) {
		return ""
	}
	return pat
}

// namePatternFromSearch compiles a NamePattern regex from a search pattern
// of the form `pkg\.Identifier\(` where Identifier contains regex wildcards
// (`\w+`, `\d+`, `.*`, etc.). Returns (pkgName, compiledRegex) or ("", nil)
// if the pattern does not have wildcard-name shape.
func namePatternFromSearch(pat string) (string, *regexp.Regexp) {
	if !strings.Contains(pat, `\w`) && !strings.Contains(pat, `\d`) {
		return "", nil
	}
	if strings.Contains(pat, "/") {
		return "", nil
	}

	// Expect form: pkg\.<regex>\( or pkg\.<regex>\b  (pkg is optional).
	// Strip the trailing `\(` or `\b` delimiter.
	body := pat
	for _, suf := range []string{`\(`, `\b`, `$`} {
		body = strings.TrimSuffix(body, suf)
	}

	// Split on `\.` — the part before is the package, the part after is
	// the name-regex body.
	idx := strings.Index(body, `\.`)
	var pkg, nameRe string
	if idx >= 0 {
		pkg = body[:idx]
		nameRe = body[idx+2:]
	} else {
		nameRe = body
	}
	if nameRe == "" {
		return "", nil
	}

	// Validate the package prefix looks like a Go identifier.
	if pkg != "" {
		for _, c := range pkg {
			if !isIdentChar(c) {
				return "", nil
			}
		}
	}

	// Require the name regex to start with an identifier character class
	// (letter, `_`, or `[...]`), otherwise it's unlikely to be a real name.
	if !strings.HasPrefix(nameRe, "[") {
		r := rune(nameRe[0])
		if !(r >= 'A' && r <= 'Z') && !(r >= 'a' && r <= 'z') && r != '_' {
			return "", nil
		}
	}

	re, err := regexp.Compile("^" + nameRe + "$")
	if err != nil {
		return "", nil
	}
	return pkg, re
}

func isIdentChar(c rune) bool {
	return (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_'
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

// extractNameFromPattern extracts the identifier a regex pattern is targeting.
// Handles common forms like:
//   - `pkg\.Name\(` → "Name"
//   - `pkg\.Name\b` → "Name"
//   - `\.Name\([A-Za-z0-9_]+\)` → "Name"
//   - `Name\(` → "Name"
//   - `Name\b` → "Name"
//
// Returns "" if the pattern:
//   - contains a `/` (import-path shape, not an identifier)
//   - has a wildcard attached to the name (e.g. `Is\w+Error` — the name
//     extends beyond the extractable prefix, so no single identifier fits)
//   - doesn't contain any extractable identifier at all
//
// Priority: an identifier following `\.` wins (e.g. `jws\.Sign` returns
// "Sign", not "jws"). Otherwise a standalone identifier at a regex boundary
// is returned.
func extractNameFromPattern(pat string) string {
	// Import paths contain `/`; we don't extract identifiers from them.
	if strings.Contains(pat, "/") {
		return ""
	}

	// Prefer identifier after `\.` — that's the called/referenced symbol in
	// almost all rules.
	if loc := identAfterDot.FindStringSubmatchIndex(pat); loc != nil {
		name := pat[loc[2]:loc[3]]
		if isNameBoundary(pat[loc[3]:]) {
			return name
		}
		return ""
	}

	// Fall back to a standalone identifier at a regex boundary.
	if loc := standaloneIdent.FindStringSubmatchIndex(pat); loc != nil {
		return pat[loc[2]:loc[3]]
	}

	return ""
}

// isNameBoundary reports whether the given regex tail starts with something
// that ends a Go identifier (so the preceding match really is the full name).
// Anything that could extend the identifier (`\w`, `\d`, `*`, `+`, `?`, `{`)
// means the pattern's target is NOT a single fixed identifier.
func isNameBoundary(tail string) bool {
	if tail == "" {
		return true
	}
	// Regex constructs that would extend the preceding character class.
	switch {
	case strings.HasPrefix(tail, `\w`), strings.HasPrefix(tail, `\d`),
		strings.HasPrefix(tail, `\S`), strings.HasPrefix(tail, `[`),
		strings.HasPrefix(tail, `*`), strings.HasPrefix(tail, `+`),
		strings.HasPrefix(tail, `?`), strings.HasPrefix(tail, `{`):
		return false
	}
	// Otherwise the identifier is terminated (by `\(`, `\b`, literal `(`,
	// whitespace, punctuation, end of pattern, etc.).
	return true
}
