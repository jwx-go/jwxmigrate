package main

import (
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestCheckBuildFilesRecursive verifies that file_patterns rules scan the
// full directory tree, including dotfile directories like .github/workflows
// where build-tag usage overwhelmingly lives. See review item
// JWXMIGRATE-20260415151950-012.
func TestCheckBuildFilesRecursive(t *testing.T) {
	dir := t.TempDir()

	mkfile := func(rel, body string) {
		full := filepath.Join(dir, rel)
		require.NoError(t, os.MkdirAll(filepath.Dir(full), 0o755))
		require.NoError(t, os.WriteFile(full, []byte(body), 0o644))
	}

	// Realistic layout: build tag lives in a GitHub workflow and a script
	// under subdirectories. Top-level files have no references.
	mkfile(".github/workflows/ci.yml", "jobs:\n  test:\n    run: go test -tags=jwx_goccy ./...\n")
	mkfile("scripts/ci/test.sh", "#!/bin/sh\ngo build -tags=jwx_asmbase64 ./...\n")
	mkfile("build/docker/Dockerfile", "FROM golang\nRUN go build -tags=jwx_es256k ./...\n")
	mkfile("vendor/thirdparty.yml", "tags: jwx_goccy\n") // must NOT be scanned
	mkfile("Makefile", "test:\n\tgo test ./...\n")       // no match, stays quiet

	mkRule := func(id, pat string) CompiledRule {
		return CompiledRule{
			Rule: Rule{
				ID:           id,
				Mechanical:   true,
				FilePatterns: []string{"*.yml", "*.yaml", "*.sh", "Makefile", "Dockerfile"},
			},
			Patterns: []*regexp.Regexp{regexp.MustCompile(pat)},
		}
	}
	rules := []CompiledRule{
		mkRule("build-tag-goccy", `jwx_goccy`),
		mkRule("build-tag-asmbase64", `jwx_asmbase64`),
		mkRule("build-tag-es256k", `jwx_es256k`),
	}

	findings := checkBuildFiles(dir, rules, CheckOptions{})

	byRule := map[string][]Finding{}
	for _, f := range findings {
		byRule[f.RuleID] = append(byRule[f.RuleID], f)
	}

	require.Len(t, byRule["build-tag-goccy"], 1, "workflow hit under .github/workflows must be reported")
	require.Equal(t, filepath.Join(".github", "workflows", "ci.yml"), byRule["build-tag-goccy"][0].File)

	require.Len(t, byRule["build-tag-asmbase64"], 1, "script hit under scripts/ci must be reported")
	require.Equal(t, filepath.Join("scripts", "ci", "test.sh"), byRule["build-tag-asmbase64"][0].File)

	require.Len(t, byRule["build-tag-es256k"], 1, "Dockerfile hit under build/docker must be reported")
	require.Equal(t, filepath.Join("build", "docker", "Dockerfile"), byRule["build-tag-es256k"][0].File)

	// vendor/ must still be skipped.
	for _, f := range findings {
		require.NotContains(t, f.File, "vendor", "vendor/ must not be scanned")
	}
}
