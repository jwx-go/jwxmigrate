# jwxmigrate

Migration checker for [github.com/lestrrat-go/jwx](https://github.com/lestrrat-go/jwx) v3 to v4.

Scans your Go project for remaining v3 API patterns and reports what needs to change, with references to specific migration rules and before/after examples.

Designed for both human developers and AI coding agents.

## Install

```bash
go install github.com/jwx-go/jwxmigrate@latest
```

## Usage

```bash
# Check current directory
jwxmigrate

# Check a specific directory
jwxmigrate /path/to/project

# JSON output (for agent consumption)
jwxmigrate --format json

# Only show auto-fixable items
jwxmigrate --mechanical

# Check a specific rule
jwxmigrate --rule import-v3-to-v4
```

### Exit codes

| Code | Meaning |
|------|---------|
| 0 | No v3 patterns found — migration complete |
| 1 | Remaining v3 patterns detected |
| 2 | Error (bad arguments, unreadable files, etc.) |

## Output

### Text (default)

```
[import-v3-to-v4] (auto) auth/middleware.go:5: import "github.com/lestrrat-go/jwx/v3/jwt"
  Update all import paths from v3 to v4.

[get-to-field] (manual) auth/middleware.go:42: token.Get(jwt.SubjectKey, &sub)
  Replace .Get(name, &dst) with .Field(name) or generic Get[T]()

Summary: 12 items remaining (4 mechanical, 8 require judgment)
```

### JSON

```json
{
  "total": 12,
  "mechanical": 4,
  "judgment": 8,
  "findings": [
    {
      "rule_id": "import-v3-to-v4",
      "file": "auth/middleware.go",
      "line": 5,
      "text": "import \"github.com/lestrrat-go/jwx/v3/jwt\"",
      "mechanical": true,
      "note": "Update all import paths from v3 to v4.",
      "example_before": "import \"github.com/lestrrat-go/jwx/v3/jwt\"",
      "example_after": "import \"github.com/lestrrat-go/jwx/v4/jwt\""
    }
  ]
}
```

## Migration rules

Rules are defined in `v3-to-v4.yaml` (schema: `schema.yaml`). Each rule has:

- **id**: stable identifier for referencing in diagnostics
- **mechanical**: whether an agent can auto-apply without judgment
- **search_patterns**: regexes to find affected code
- **compiler_hints**: Go compiler errors that map to this rule
- **note**: actionable migration guidance
- **example**: before/after code

For the full human-readable migration guide, see [MIGRATION.md](https://github.com/lestrrat-go/jwx/blob/develop-v4/MIGRATION.md) in the jwx repository.
