# jwxmigrate

## Overview

This module (`github.com/jwx-go/jwxmigrate/v4`) is a migration checker for `github.com/lestrrat-go/jwx` version upgrades (v2→v4, v3→v4).

It scans Go projects for remaining old-version API patterns and reports what needs to change, with references to specific migration rules and before/after examples. Supports both human-readable text and machine-readable JSON output.

## Architecture

jwxmigrate is a standalone CLI tool (`package main`). It loads migration rules from embedded YAML files, compiles regex-based search patterns, and scans `.go` files for matches. It also supports AST-based scanning for patterns that regexes cannot reliably detect, and mechanical auto-fixing of simple patterns.

### Components

| File | Purpose |
|------|---------|
| `main.go` | CLI entry point, flag parsing, orchestration |
| `rules.go` | Rule loading and compilation from YAML |
| `check.go` | Scan files and collect findings |
| `fix.go` | Apply mechanical fixes in-place |
| `ast_scanner.go` | AST-based pattern detection |
| `ast_matcher.go` | AST pattern matching helpers |
| `ast_derive.go` | Derive additional patterns from AST analysis |
| `v3-to-v4.yaml` | v3→v4 migration rules |
| `v2-to-v4.yaml` | v2→v4 migration rules |
| `schema.yaml` | YAML schema for rule definitions |

## Build / Test

```
go test ./...
```

## Branch Policy

| Branch | Purpose |
|--------|---------|
| `v*` (e.g. `v4`) | Release tags only. NEVER commit directly to these branches. |
| `develop/v*` (e.g. `develop/v4`) | Active development. All feature branches merge here. |
| Feature branches | Branch from `develop/v*`, merge back via PR. |

- Tags are cut from `v*` branches.
- `v*` branches should never be directly worked on.
- Regular development happens on `develop/v*` and feature branches.
