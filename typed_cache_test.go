package main

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"
)

// TestBuildTypedFileCache_PopulatesV3ImportingFiles pins the batch
// cache contract: after buildTypedFileCache runs over a file list
// spanning a v3-importing module, every v3-importing .go file shows
// up in the returned map with TypesInfo attached, and files that
// don't import v3 are omitted.
func TestBuildTypedFileCache_PopulatesV3ImportingFiles(t *testing.T) {
	// loadRules mutates sourceImportPrefix — without this, a prior test's
	// v2-to-v4 run could leave the prescan looking for v2 imports while
	// our fixture uses v3.
	_, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	root := withStubJWKModule(t)

	callerDir := filepath.Join(root, "caller")
	require.NoError(t, os.MkdirAll(callerDir, 0o755))

	v3File := filepath.Join(callerDir, "main.go")
	require.NoError(t, os.WriteFile(v3File, []byte(`package caller

import "github.com/lestrrat-go/jwx/v3/jwk"

func Example(k jwk.Key) (any, error) {
	var raw any
	err := jwk.Export(k, &raw)
	return raw, err
}
`), 0o644))

	noV3File := filepath.Join(callerDir, "other.go")
	require.NoError(t, os.WriteFile(noV3File, []byte(`package caller

func Other() string { return "hi" }
`), 0o644))

	cache := buildTypedFileCache([]string{v3File, noV3File}, nil)

	absV3, err := filepath.Abs(v3File)
	require.NoError(t, err)
	absNoV3, err := filepath.Abs(noV3File)
	require.NoError(t, err)

	pf := cache[absV3]
	require.NotNil(t, pf, "v3-importing file must be in cache")
	require.NotNil(t, pf.TypesInfo, "cached entry must carry type info")
	require.NotEmpty(t, pf.V3Imports)

	_, present := cache[absNoV3]
	require.False(t, present, "non-v3 files must be omitted from cache")
}

// TestFixFileWithOptions_UsesTypedCacheEntry pins that a pre-populated
// typedCache short-circuits parseGoFileTyped: the fixer finds the cached
// entry by absolute path and rewrites using it directly. We verify this
// by feeding a cache entry whose TypesInfo lets a type-aware fix fire —
// specifically jwk-export-generic, which silently no-ops without types.
func TestFixFileWithOptions_UsesTypedCacheEntry(t *testing.T) {
	root := withStubJWKModule(t)

	callerDir := filepath.Join(root, "caller")
	require.NoError(t, os.MkdirAll(callerDir, 0o755))
	mainPath := filepath.Join(callerDir, "main.go")
	require.NoError(t, os.WriteFile(mainPath, []byte(`package caller

import "github.com/lestrrat-go/jwx/v3/jwk"

func Example(k jwk.Key) (any, error) {
	var raw any
	err := jwk.Export(k, &raw)
	return raw, err
}
`), 0o644))

	rules, err := loadRules("v3-to-v4")
	require.NoError(t, err)

	cache := buildTypedFileCache([]string{mainPath}, nil)
	absMain, err := filepath.Abs(mainPath)
	require.NoError(t, err)
	require.NotNil(t, cache[absMain])

	res, err := FixFileWithOptions(mainPath, rules, FixOptions{typedCache: cache})
	require.NoError(t, err)
	require.NotNil(t, res)
	require.Contains(t, res.Applied, "jwk-export-generic",
		"typed cache entry must enable jwk-export-generic to fire")

	got, err := os.ReadFile(mainPath)
	require.NoError(t, err)
	require.Contains(t, string(got), "jwk.Export[any](k)")
}
