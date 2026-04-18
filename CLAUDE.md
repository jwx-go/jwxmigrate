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

## Authoring Rules and Fixtures

A new rule is two files:

1. **Rule entry** in `v3-to-v4.yaml` (or `v2-to-v4.yaml`) — one block matching
   `schema.yaml`: `id`, `kind`, `package`, `search_patterns`, `note`, etc.
2. **Fixture** at `testdata/rules/<v2|v3>/<rule-id>/fixture.txtar` — a single
   [txtar](https://pkg.go.dev/golang.org/x/tools/txtar) archive with sections:
   - `-- input/<path> --` — one or more Go (or build) files the rule should fire on.
   - `-- want_check.txt --` — expected golden output from the check pass.
   - `-- want_fix/<path> --` — expected file contents after `--fix` (omit for
     non-mechanical rules that have no auto-fix output).

Both `want_check.txt` and `want_fix/*` are auto-generated; seed an empty
fixture with just the `input/` section and run:

```
go test -update -run TestRulesFixtures/<migration>/<rule-id> ./...
```

Review the regenerated `fixture.txtar` diff before committing — `-update`
accepts whatever the tool produces as the new truth.

### Defaults and overrides

The fixture harness derives sensible defaults from the path:

- `testdata/rules/v2/<rule-id>/` → migration `v2-to-v4`, rule_id = `<rule-id>`.
- `testdata/rules/v3/<rule-id>/` → migration `v3-to-v4`, rule_id = `<rule-id>`.
- `testdata/edge/<name>/` → migration `v3-to-v4`, no rule_id filter.

Only drop a `config.yaml` alongside `fixture.txtar` when a fixture needs an
override: `skip_fix: true`, `mechanical_only: true`, or a non-default
migration (e.g. v2 edge fixtures).

### Legacy directory layout

Older fixtures used a directory tree (`input/`, `want_check.txt`, `want_fix/`)
instead of a single txtar archive. The loader still accepts that layout if
`fixture.txtar` is absent, but new fixtures should use txtar.

## Branch Policy

| Branch | Purpose |
|--------|---------|
| `v*` (e.g. `v4`) | Release tags only. NEVER commit directly to these branches. |
| `develop/v*` (e.g. `develop/v4`) | Active development. All feature branches merge here. |
| Feature branches | Branch from `develop/v*`, merge back via PR. |

- Tags are cut from `v*` branches.
- `v*` branches should never be directly worked on.
- Regular development happens on `develop/v*` and feature branches.
