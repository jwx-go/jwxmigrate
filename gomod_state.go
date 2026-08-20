package main

import (
	"os"
	"path/filepath"

	"golang.org/x/mod/modfile"
)

// moduleState is what a consumer's go.mod says about the two things version
// floors are compared against: the language version it builds at, and the
// module versions it currently requires.
type moduleState struct {
	// Root is the directory holding the go.mod this was read from.
	Root string
	// GoVersion is the `go` directive value, e.g. "1.26.0". Empty when the
	// directive is absent or the file could not be parsed.
	GoVersion string
	// Requires maps module path to the version currently required. Only
	// direct and indirect requires present in the file appear here.
	Requires map[string]string
	// RequireLines maps module path to the 1-indexed line of its require,
	// so a diagnostic can point at the line the reader has to edit.
	RequireLines map[string]int
}

// readModuleState parses the go.mod at modRoot. An unreadable or unparseable
// file yields a zero-valued state rather than an error: every caller treats
// "unknown" the same as "no floor information available", and refusing to
// scan a project because its go.mod is malformed would be worse than
// scanning it without version diagnostics.
func readModuleState(modRoot string) moduleState {
	st := moduleState{
		Root:         modRoot,
		Requires:     map[string]string{},
		RequireLines: map[string]int{},
	}

	path := filepath.Join(modRoot, goModFilename)
	src, err := os.ReadFile(path)
	if err != nil {
		return st
	}
	mf, err := modfile.Parse(path, src, nil)
	if err != nil {
		return st
	}

	if mf.Go != nil {
		st.GoVersion = mf.Go.Version
	}
	for _, req := range mf.Require {
		st.Requires[req.Mod.Path] = req.Mod.Version
		if req.Syntax != nil {
			st.RequireLines[req.Mod.Path] = req.Syntax.Start.Line
		}
	}
	return st
}

// moduleStateFor walks up from a file to the nearest enclosing go.mod and
// reads it. Returns false when the file is not inside a module.
func moduleStateFor(file string) (moduleState, bool) {
	abs, err := filepath.Abs(file)
	if err != nil {
		return moduleState{}, false
	}
	for d := filepath.Dir(abs); ; {
		if _, err := os.Stat(filepath.Join(d, goModFilename)); err == nil {
			return readModuleState(d), true
		}
		parent := filepath.Dir(d)
		if parent == d {
			return moduleState{}, false
		}
		d = parent
	}
}
