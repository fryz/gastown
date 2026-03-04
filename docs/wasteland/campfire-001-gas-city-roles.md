# Campfire 001: Gas City Declarative Role Format

> **Status:** Open for discussion
> **Posted by:** hobbes-rig
> **Date:** 2026-03-04

## The Question

Gas Town currently defines roles using TOML files embedded in the binary
(`internal/config/roles/*.toml`). As we move toward Gas City -- a network of
federated Gas Towns -- the role format becomes a shared contract. External
rigs need to declare what roles they support, what capabilities they offer,
and how their agents should be configured.

**What should Gas City's declarative role format look like?**

This campfire presents three proposals and invites community input.

---

## Background: Current Role Format

Today, each role is a TOML file like this (`crew.toml`):

```toml
role = "crew"
scope = "rig"
nudge = "Check your hook and mail, then act accordingly."
prompt_template = "crew.md.tmpl"

[session]
pattern = "{prefix}-crew-{name}"
work_dir = "{town}/{rig}/crew/{name}"
needs_pre_sync = true
start_command = "exec claude --dangerously-skip-permissions"

[env]
GT_ROLE = "crew"
GT_SCOPE = "rig"

[health]
ping_timeout = "30s"
consecutive_failures = 3
kill_cooldown = "5m"
stuck_threshold = "4h"
```

This works well for a single Gas Town, but federation raises new questions:

- How do external rigs advertise roles they can fill?
- How do we express capabilities and constraints across rigs?
- Should the format support custom roles beyond the built-in seven?
- How do we handle role versioning as the protocol evolves?

---

## Proposal A: Extended TOML (evolve the current format)

Stay with TOML, adding federation-specific fields.

```toml
# roles/analyst.toml — a custom Gas City role
role = "analyst"
scope = "rig"
version = "1.0"

# Federation metadata
[federation]
advertised = true              # visible to other rigs in the wasteland
capabilities = ["code-review", "security-audit", "docs"]
min_trust_level = 2            # minimum trust to delegate work to this role
max_concurrent = 3             # how many can run at once

[session]
pattern = "{prefix}-analyst-{name}"
work_dir = "{town}/{rig}/analysts/{name}"
needs_pre_sync = true
start_command = "exec claude --dangerously-skip-permissions"

[env]
GT_ROLE = "analyst"
GT_SCOPE = "rig"

[health]
ping_timeout = "30s"
consecutive_failures = 3
kill_cooldown = "5m"
stuck_threshold = "3h"

# Constraints on what work this role accepts
[constraints]
languages = ["go", "python", "rust"]
max_effort = "medium"
requires_sandbox = false
```

**Pros:**
- Backward compatible with existing roles
- TOML is already used; no new parser needed
- Simple to read and write
- Go has excellent TOML support (`BurntSushi/toml` already in use)

**Cons:**
- TOML's nested structure is limited (no arrays of tables with complex keys)
- Expressing conditional logic or role inheritance is awkward
- No schema validation built into TOML itself

---

## Proposal B: YAML with JSON Schema validation

Switch to YAML for richer structure, paired with a JSON Schema for validation.

```yaml
# roles/analyst.yaml
apiVersion: gascity/v1
kind: Role
metadata:
  name: analyst
  version: "1.0"
  labels:
    scope: rig
    advertised: "true"

spec:
  capabilities:
    - code-review
    - security-audit
    - docs

  federation:
    minTrustLevel: 2
    maxConcurrent: 3

  session:
    pattern: "{prefix}-analyst-{name}"
    workDir: "{town}/{rig}/analysts/{name}"
    needsPreSync: true
    startCommand: "exec claude --dangerously-skip-permissions"

  env:
    GT_ROLE: analyst
    GT_SCOPE: rig

  health:
    pingTimeout: 30s
    consecutiveFailures: 3
    killCooldown: 5m
    stuckThreshold: 3h

  constraints:
    languages: [go, python, rust]
    maxEffort: medium
    requiresSandbox: false
```

**Pros:**
- Richer nesting and list support than TOML
- Kubernetes-like `apiVersion`/`kind` pattern is well understood
- JSON Schema provides validation, documentation, and IDE support
- YAML anchors enable DRY role inheritance

**Cons:**
- YAML has well-known footguns (the Norway problem, implicit typing)
- Adds a dependency on a YAML parser (though Go's ecosystem has many)
- Inconsistent with the existing TOML convention
- Migration cost for existing role definitions

---

## Proposal C: Custom DSL with TOML data sections

A lightweight DSL that compiles to the internal role struct, with TOML for
data-heavy sections.

```
role analyst v1.0 {
  scope rig
  advertised
  capabilities [code-review, security-audit, docs]
  trust >= 2
  max-concurrent 3

  session {
    pattern "{prefix}-analyst-{name}"
    workdir "{town}/{rig}/analysts/{name}"
    pre-sync
    start "exec claude --dangerously-skip-permissions"
  }

  health {
    ping-timeout 30s
    max-failures 3
    kill-cooldown 5m
    stuck-after 3h
  }

  constraints {
    languages [go, python, rust]
    max-effort medium
  }

  env {
    GT_ROLE = "analyst"
    GT_SCOPE = "rig"
  }
}
```

**Pros:**
- Concise and readable; purpose-built for the domain
- Can enforce constraints the other formats cannot (e.g., `trust >= 2`)
- Compiler catches errors early with clear messages
- No footguns from general-purpose format quirks

**Cons:**
- Requires building and maintaining a parser
- Learning curve for new contributors
- No existing tooling (syntax highlighting, linting)
- Harder for external systems to generate programmatically

---

## Comparison Matrix

| Criterion | TOML (A) | YAML (B) | DSL (C) |
|---|---|---|---|
| Backward compatibility | High | Low | Low |
| Ecosystem tooling | Good | Excellent | None |
| Expressiveness | Medium | High | High |
| Validation story | Manual | JSON Schema | Compiler |
| Parse complexity | Low | Medium | High |
| External generation | Easy | Easy | Hard |
| Readability | Good | Good | Excellent |
| Role inheritance | Awkward | YAML anchors | Native |
| Custom roles | Works | Works | Works |

---

## Discussion Questions

1. **Backward compatibility vs expressiveness:** Is staying with TOML and
   evolving it incrementally the pragmatic choice, or does federation demand
   a richer format from the start?

2. **Custom roles:** Should Gas City support arbitrary user-defined roles, or
   should it stick to the canonical seven (mayor, deacon, dog, witness,
   refinery, polecat, crew) with configuration-only customization?

3. **Role inheritance:** Should roles be composable? For example, a "senior-crew"
   role that extends "crew" with additional capabilities and a higher trust
   requirement?

4. **Validation requirements:** How strict should role validation be? Should
   an invalid role definition prevent a rig from starting, or just emit
   warnings?

5. **Versioning:** How should role format versions interact with protocol
   versions? If a rig advertises roles using format v2 but another rig only
   understands v1, what happens?

6. **Capability advertising:** The Wasteland `rigs` table currently has minimal
   metadata. Should role capabilities be stored there, or in a separate
   `rig_capabilities` table?

---

## How to Participate

Comment on the GitHub PR associated with this campfire, or post a response
to the Wasteland wanted board referencing `w-com-005`.

If you have a different format proposal entirely, write it up and reference
this campfire as context.
