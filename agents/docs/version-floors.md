# Determining a Rule's Version Floor

Read before adding or editing a rule whose guidance names an API, or before
declaring `requires.modules` in `v3-to-v4.yaml` / `v2-to-v4.yaml`.

A floor is the **lowest published version of the target module that contains
every symbol the rule tells the user to write**. It is a lookup against that
module's git history, never a guess and never "the latest release".

## When a floor is required

| Situation | Action |
|-----------|--------|
| Rule names only symbols present at the ruleset `minimum_target_version` | Omit `requires`. |
| Rule's `v4`, `replacement`, `note`, or `example` names a symbol added later | Declare `requires.modules`. |
| Rule points at a companion via `extension_module` | Declare `requires.modules` for that companion path. |
| Rule's guidance only compiles on a newer toolchain | Declare `requires.go`. |

Symbols in the `v3:` / `v2:` fields describe the OLD API. NEVER derive a floor
from them.

## Procedure

Needs a local clone of the target module. For jwx that is the jwx checkout; for
a companion it is `<jwx>/.companions/repo/<name>`. All commands are read-only.

### Step 1 — list the symbols the rule tells the user to write

Read the rule's `v4`, `replacement`, `note`, and `example.after` fields. Collect
every `pkg.Symbol` token. Those four fields are what a reader acts on.

### Step 2 — find the commit that introduced each symbol

```bash
git -C <module-clone> log --oneline --reverse -S "<Symbol>" -- '<pkg>/*.go' | head -1
```

`-S` reports commits that changed the number of occurrences, so the first entry
in reverse order is the one that added it. Restrict the pathspec to the owning
package; the same identifier often appears in tests and docs first.

### Step 3 — find the first release containing that commit

```bash
git -C <module-clone> tag --contains <sha> --sort=v:refname | head -1
```

Empty output means the symbol is **unreleased**. NEVER declare a floor on an
unreleased version. Cut the release first, or hold the rule.

### Step 4 — confirm the symbol is absent at the baseline

```bash
git -C <module-clone> grep -qE "(func|type|const|var) +<Symbol>\b" <baseline-tag> -- '<pkg>/*.go'
echo $?
```

Same pattern as the audit script below, so both agree on what counts as a
declaration. `func` alone would miss a type, const, or var.

| Exit | Meaning | Action |
|------|---------|--------|
| 1 | Absent at the baseline | Declare the Step 3 tag as the floor. |
| 0 | Already present at the baseline | Omit `requires` for this symbol; it needs no floor. |

Exit 0 usually means Step 2 found a commit that moved or renamed the symbol
rather than introduced it. Re-run Step 2 with a wider pathspec before concluding
anything.

### Step 5 — the floor is the highest result across the rule's symbols

Declare it:

```yaml
    requires:
      modules:
        - path: github.com/lestrrat-go/jwx/v4
          version: v4.2.0
```

The version's major must match the path's major suffix. A `/v4` path takes a
`v4.x.y` version. The validation test enforces this.

## Auditing a whole ruleset

Use this to check every rule at once, e.g. after a target-module release. It
prints symbols the rules mention that do NOT exist at the baseline but DO exist
at the tip, which are exactly the rules needing a floor.

```bash
#!/bin/bash
set -uo pipefail
JWX=<path-to-jwx-clone>
RULES=<path-to>/v3-to-v4.yaml
BASE=v4.0.0            # ruleset minimum_target_version
TIP=origin/develop/v4

grep -oE '\b(jwk|jws|jwt|jwe|jwa|jwx|jwkbb|jwsbb)\.[A-Z][A-Za-z0-9_]*' "$RULES" \
  | sort -u | while read -r tok; do
    pkg=${tok%%.*}; sym=${tok#*.}
    case "$pkg" in
      jwkbb) dir="jwk/jwkbb" ;;
      jwsbb) dir="jws/jwsbb" ;;
      jwx)   dir="." ;;
      *)     dir="$pkg" ;;
    esac
    old=no; new=no
    git -C "$JWX" grep -qE "(func|type|const|var) +$sym\b|$sym +=" "$BASE" -- "$dir/*.go" 2>/dev/null && old=yes
    git -C "$JWX" grep -qE "(func|type|const|var) +$sym\b|$sym +=" "$TIP"  -- "$dir/*.go" 2>/dev/null && new=yes
    if [ "$old" = no ] && [ "$new" = yes ]; then echo "$tok needs a floor"; fi
  done
```

The final `if` is deliberate. Written as an `&&` chain the loop exits non-zero
whenever the last symbol checked does not need a floor, which is the normal
case and would fail any caller running under `set -e`.

Run it against both `v3-to-v4.yaml` and `v2-to-v4.yaml`. Adjust the package
alternation when a rule set starts naming a new package.

Dated result, 2026-08-20: `v3-to-v4.yaml` scanned 169 symbols and returned
three, all from `jwk-parse-retains-unsupported-keys` (floor `v4.2.0`).
`v2-to-v4.yaml` scanned 96 and returned none.

## Toolchain floors

`requires.go` takes the `go` directive value, e.g. `"1.27"`, not a patch
version. Derive it from the Go release that first shipped the standard-library
package the guidance depends on. NEVER derive it from the toolchain installed
on the machine writing the rule.

## Why this is written down

The floors are the only thing standing between a rule and advice that does not
compile. `jwxmigrate` has no `go.mod` dependency on jwx or on any companion, so
no dependency tooling will ever notice that a floor is missing or stale. Only
this procedure and the validation tests in `version_floor_test.go` will.

## How the declared values are used

| Field | Effect |
|-------|--------|
| `minimum_target_version` (rule set) | Version written for the target module when no fired rule asks for more. |
| `requires.modules` | Raises that version; reported as a finding when the project is below it; written by `--fix`. |
| `requires.go` | Suppresses the rule entirely on projects whose `go` directive is lower. |

Only rules that actually fired contribute. A consumer is never raised to a
version some unrelated rule happens to mention.
