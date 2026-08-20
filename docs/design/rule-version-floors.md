# Per-Rule Module Version Floors

Status: proposed.

## Problem

`fix_gomod.go` pins every `go.mod` rewrite to one hand-maintained constant:

```go
const latestV4Version = "v4.0.0"
```

Its comment says the value "only needs to be a valid version that go can
resolve, not necessarily the absolute latest", because `fixFiles` runs
`go mod tidy` afterward. That reasoning does not hold. `go mod tidy` uses
minimal version selection and will not raise a `require` above what something
else demands, so whatever this constant names is what the user ends up on.

That matters because a rule's guidance can reference API that did not exist at
the pinned version.

### The live instance

Rule `jwk-parse-retains-unsupported-keys` (`v3-to-v4.yaml:421`) tells the user
to call `jwk.WithStrictKeySetParsing(true)`, or to filter with
`jwk.IsUnsupportedKey`. Both symbols arrived in jwx commit `68683a11` and first
shipped in **v4.2.0**; neither exists in the v4.0.0 tree.

So `jwxmigrate --fix` writes `github.com/lestrrat-go/jwx/v4 v4.0.0` into the
user's `go.mod`, and then the rule it just reported tells them to write code
that does not compile against it.

### How widespread it is

Every `pkg.Symbol` token named anywhere in `v3-to-v4.yaml` was extracted (169
of them) and checked against the v4.0.0 tree and against `develop/v4`. Exactly
three are post-v4.0.0, and all three belong to the single rule above.

The same sweep over `v2-to-v4.yaml` (96 symbols) found none, so that ruleset is
clean and its baseline can be declared at `v4.0.0` without further work.

The problem is therefore narrow today. It grows by one every time a rule is
written about recently added API. The planned ML-DSA rule is the next instance,
at whatever release first ships native ML-DSA.

### The second axis

22 rules carry `kind: moved_to_extension` and name six external modules:
`asmbase64`, `ed448`, `es256k`, `jwkfetch`, `jwxfilter`, and
`jwxfilter/openidfilter`. `importRewriteRules` (`fix_gomod.go:167-175`) filters
to `kind: import_change` with `package: all`, so companion modules are never
written to `go.mod` at all and the user resolves them by hand.

All six start at `v4.0.0` and sit within four patch releases of it, so nothing
is broken now. But a rule referencing companion API added later would fail the
same way, and the schema has nowhere to record the requirement.

### Why nothing catches the drift

`jwxmigrate`'s `go.mod` requires only testify, `golang.org/x/mod`,
`golang.org/x/tools`, and yaml.v3. It has no dependency edge to jwx at all —
every `lestrrat-go/jwx` occurrence in the source is fixture text. Dependabot,
`go get -u`, and anything else that watches `go.mod` will never touch this
constant. It is correct only for as long as somebody remembers, which is why it
is still `v4.0.0` three releases on.

## Goals

1. A rule declares the minimum version of the module its guidance targets.
2. `--fix` writes a version that satisfies the rules that actually fired.
3. `check` reports when the project's current version is below a fired rule's
   floor.
4. The hand-maintained constant stops being the source of truth, and the
   remaining declared value is guarded by a test that needs no network.
5. The same mechanism expresses a Go toolchain floor, which the ML-DSA rule
   needs.

### Non-goals

- Resolving versions over the network at runtime. The tool stays offline.
- Upgrading a user's dependencies beyond the declared floor. Minimal version
  selection is the user's to control.
- Raising the project's `go` directive. That is a decision the tool should
  report, never make.

## Design

### Schema

`schema_version` goes from `"2"` to `"3"`. One optional field is added per rule:

```yaml
  - id: jwk-parse-retains-unsupported-keys
    kind: behavioral
    package: jwk
    mechanical: false
    requires:
      modules:
        - path: github.com/lestrrat-go/jwx/v4
          version: v4.2.0
```

and, for a rule gated on the toolchain:

```yaml
    requires:
      go: "1.27"
```

`go` and `modules` are independent and either may be omitted. A rule with no
`requires` block behaves exactly as today.

The ruleset header gains a baseline, replacing the Go constant:

```yaml
schema_version: "3"
from: github.com/lestrrat-go/jwx/v3
to: github.com/lestrrat-go/jwx/v4
minimum_target_version: v4.0.0
```

This is the version written to `go.mod` when no fired rule asks for more. It
stays a declared value, but it now lives in the file a rule author is already
editing, next to the rules that constrain it.

### Types

```go
// Requires expresses the preconditions a rule's guidance depends on.
// A zero value means the rule applies unconditionally.
type Requires struct {
    Go      string              `yaml:"go,omitempty"      json:"go,omitempty"`
    Modules []ModuleRequirement `yaml:"modules,omitempty" json:"modules,omitempty"`
}

type ModuleRequirement struct {
    Path    string `yaml:"path"    json:"path"`
    Version string `yaml:"version" json:"version"`
}
```

`Rule` gains `Requires *Requires`. `RuleSet` gains
`MinimumTargetVersion string`.

### Reading the project's versions

`ast_scanner.go` already discovers module roots (`findModuleRoots`) and walks
each one (`prescanModule`). `fix_gomod.go` already parses `go.mod` with
`golang.org/x/mod/modfile`. `modulePrescan` gains two fields filled from that
parse:

```go
type modulePrescan struct {
    Root      string
    Patterns  []string
    V3Files   []string
    GoVersion string            // the `go` directive
    Requires  map[string]string // module path -> version
}
```

Nothing new is walked or parsed; the existing per-module pass carries two more
facts out.

### Gating on the Go version

A rule with `requires.go` set applies only when the project's `go` directive is
at or above it.

The gate reads the `go` directive, not the installed toolchain. The directive
governs which standard-library API the project is allowed to use, and it gives
the same answer on every machine, which matters for a tool whose output is
committed.

When the directive is below the floor, the rule is suppressed rather than
reported as unmet. A user on `go 1.26` cannot act on a `go 1.27` cleanup, so
surfacing it is noise in the middle of a v3-to-v4 migration. If that turns out
to be wanted, it belongs behind a flag.

### Resolving the version for `--fix`

For each module root, the version written for a given module path is the
highest of the ruleset baseline and the declared floors of the rules that fired
in that module.

The floors of rules that did not fire are excluded deliberately. Pinning the
maximum across the whole ruleset would drag every user to the newest version
any rule mentions, which defeats minimal version selection.

This is the main implementation cost. `FixBuildFile` currently receives
`rules []CompiledRule` and has no idea which of them matched. It needs the
check findings, grouped by module root, which the prescan already provides the
grouping for.

### The new diagnostic

When a fired rule's floor exceeds what the project currently requires, `check`
emits a finding. `Finding` gains three optional fields, so the existing JSON
contract is unchanged for current consumers:

```go
RequiresModule  string `json:"requires_module,omitempty"`
RequiresVersion string `json:"requires_version,omitempty"`
CurrentVersion  string `json:"current_version,omitempty"`
```

Text output reads:

```
go.mod requires github.com/lestrrat-go/jwx/v4 v4.1.0, but rule
jwk-parse-retains-unsupported-keys needs v4.2.0
```

This is the part that makes the floors useful without `--fix`. A project
already on v4 but behind a rule's floor gets told so, which today it never is.

## Validation

Three tests, none of which touch the network:

1. **Version well-formedness.** Every declared version parses as semver and
   agrees with its module path's major suffix. A `/v4` path with a `v3.x.y`
   version fails. This catches the class of mistake where a jwx version is
   pasted onto a companion requirement.
2. **Known module paths.** Every `requires.modules[].path` is either the
   ruleset's `to` prefix or a path named by some rule's `extension_module` or
   `replacement`. This catches typos in a field nothing else reads.
3. **Baseline consistency.** `minimum_target_version` is a valid version for
   the ruleset's `to` module, and no rule declares a floor for that module
   *below* it.

Test 3 is the answer to "codify the bump". The moment someone adds a rule whose
floor exceeds the baseline, they are told, in the same test run, in the same
repository. No release-checklist step and no scheduled job is needed, because
the constraint is local to the file being edited.

An optional fourth check, gated behind an environment variable so it never runs
in ordinary CI, can confirm each declared version is a real published tag.

### Fixtures

Fixtures already include `go.mod` in their `input/` trees, so both the gating
and the new diagnostic are testable through the existing txtar harness with no
changes to it. A fixture sets the `go` directive and the jwx `require` line and
asserts the golden output.

## Rollout

1. Land the schema, types, and validation tests with no rule changes. Behavior
   is identical, because absent `requires` means today's behavior and the
   baseline is set to `v4.0.0`, matching the constant it replaces.
2. Add `requires.modules` to `jwk-parse-retains-unsupported-keys` with
   `v4.2.0`, and a fixture covering the new diagnostic.
3. Thread findings into `FixBuildFile` and switch `--fix` to resolved floors.
4. Delete `latestV4Version`.
5. Only then add the Go-gated ML-DSA rule, which needs step 3 in place.

Steps 1 and 2 are worth doing on their own: they fix a real defect that ships
today, independently of the ML-DSA work that prompted the investigation.

## Open questions

1. **Exit code.** Current codes are 0 for complete, 1 for patterns remaining, 2
   for error. Should an unmet version floor be a 1, or informational at 0?
   It is not a remaining v3 pattern, but it does mean the migration is not
   finished.
2. **Companion pinning.** Should `--fix` start writing companion `require`
   lines for `moved_to_extension` rules, now that a floor can be declared for
   them? That is a behavior change beyond this design, but the floors make it
   possible for the first time.
3. **Companion floor discovery.** Nothing today records which companion release
   first shipped the API a `moved_to_extension` rule points at. Filling those
   in is manual archaeology per rule, so the first pass may declare floors only
   where a rule is known to need one.
