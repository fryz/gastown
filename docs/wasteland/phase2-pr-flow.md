# Phase 2: PR-Based Contribution Flow

**Wanted Item:** w-wl-001
**Status:** Design + prototype

## Overview

Phase 1 (wild-west mode) allows any rig to write directly to its local `wl-commons` database fork. This works for bootstrapping but has no review gate: any rig can claim items, submit fake completions, or corrupt shared state.

Phase 2 introduces a **PR-based contribution flow** where mutations (claims, completions, stamps) are proposed as DoltHub pull requests instead of direct writes. This enables review, trust-based auto-merge, and an audit trail of all changes.

## Current Phase 1 Flow

```
Contributor                    Local Dolt Server
    |                               |
    |-- gt wl claim w-xxx --------->|
    |   (direct UPDATE to main)     |
    |                               |
    |-- gt wl done w-xxx ---------->|
    |   (direct INSERT+UPDATE)      |
    |                               |
    |-- dolt push origin main ----->|  DoltHub fork
```

**Problems:**
- No review gate -- anyone can claim/complete anything
- No audit trail beyond Dolt commit history
- No trust differentiation between rigs
- Corruption risk from malformed writes

## Proposed Phase 2 Flow

```
Contributor                    Local Dolt      DoltHub Fork      DoltHub Upstream
    |                            |                 |                    |
    |-- gt wl claim --pr-mode -->|                 |                    |
    |   1. Create branch         |                 |                    |
    |   2. Write claim on branch |                 |                    |
    |   3. Commit on branch      |                 |                    |
    |   4. Push branch           |---------------->|                    |
    |   5. Create DoltHub PR     |                 |------------------->|
    |                            |                 |                    |
    |                            |       [Review / Auto-merge]          |
    |                            |                 |                    |
    |   6. PR merged             |                 |<-------------------|
    |   7. gt wl sync            |<----------------|                    |
```

### Step-by-step

1. **Branch creation**: `gt wl claim --pr-mode w-xxx` creates a Dolt branch named `wl/<rig-handle>/claim-<wanted-id>` on the local fork.

2. **Mutation on branch**: The claim UPDATE runs against the branch (not main), using `CALL DOLT_CHECKOUT('wl/...')` before the mutation.

3. **Commit on branch**: `CALL DOLT_COMMIT(...)` on the branch.

4. **Push branch to fork**: `dolt push origin wl/<rig-handle>/claim-<wanted-id>`.

5. **Create DoltHub PR**: POST to DoltHub's merge-request API to propose merging the branch from the fork into upstream main.

6. **Review or auto-merge**: Based on the rig's trust tier:
   - **Tier 3+ (trusted)**: Auto-merge after basic validation checks pass.
   - **Tier 1-2 (new/untrusted)**: Requires manual review by a trusted rig or the commons maintainer.

7. **Sync**: `gt wl sync` pulls merged changes from upstream into the local fork's main.

## DoltHub PR API

DoltHub exposes a merge-request API. Based on the existing `ForkDoltHubRepo` pattern in `wasteland.go`:

```go
// CreateDoltHubPR creates a pull request on DoltHub.
// fromBranch: the branch on the fork containing the changes
// toBranch: the target branch on upstream (typically "main")
func CreateDoltHubPR(upstreamOrg, upstreamDB, forkOrg, fromBranch, toBranch, title, description, token string) (string, error) {
    body := map[string]interface{}{
        "title":             title,
        "description":       description,
        "fromBranchOwnerName": forkOrg,
        "fromBranchRepoName":  upstreamDB,
        "fromBranchName":      fromBranch,
        "toBranchOwnerName":   upstreamOrg,
        "toBranchRepoName":    upstreamDB,
        "toBranchName":        toBranch,
    }
    // POST to dolthubAPIBase + "/" + upstreamOrg + "/" + upstreamDB + "/pulls"
    // Returns PR ID on success
}
```

### API endpoint

```
POST https://www.dolthub.com/api/v1alpha1/{owner}/{repo}/pulls
Authorization: token <DOLTHUB_TOKEN>
Content-Type: application/json

{
  "title": "wl claim: w-com-001 by hobbes-rig",
  "description": "Claiming wanted item w-com-001",
  "fromBranchOwnerName": "hobbes",
  "fromBranchRepoName": "wl-commons",
  "fromBranchName": "wl/hobbes-rig/claim-w-com-001",
  "toBranchOwnerName": "hop",
  "toBranchRepoName": "wl-commons",
  "toBranchName": "main"
}
```

**Note:** The exact DoltHub API shape may need adjustment based on their current documentation. The fork API (`/database/fork`) is known to work. The PR API needs testing.

## New CLI Flags

### `gt wl claim --pr-mode`

```
gt wl claim <wanted-id> [--pr-mode]
```

- Without `--pr-mode`: Phase 1 behavior (direct write to main).
- With `--pr-mode`: Phase 2 behavior (create branch, commit, push, create DoltHub PR).

### `gt wl done --pr-mode`

```
gt wl done <wanted-id> --evidence <url> [--pr-mode]
```

Same pattern: proposes completion via PR instead of direct write.

### Future: `gt wl config pr-mode true`

Once Phase 2 is stable, `--pr-mode` becomes the default. A config option allows rigs to opt in early.

## Trust Tier Integration

The `rigs.trust_level` column controls PR handling:

| Trust Level | Meaning | PR Behavior |
|-------------|---------|-------------|
| 0 | Unverified | PR requires manual review |
| 1 | Registered | PR requires manual review |
| 2 | Vouched | PR requires one approval |
| 3 | Trusted | Auto-merge after validation checks |
| 4 | Core | Auto-merge, can review others |

### Validation Checks (pre-merge)

Before any PR is merged (auto or manual), these checks run:

1. **Schema compliance**: Mutation only touches expected columns
2. **State machine**: Status transitions are valid (open->claimed, claimed->in_review)
3. **Ownership**: Rig can only claim for itself, not impersonate others
4. **No overwrites**: Cannot re-claim an already-claimed item
5. **Single mutation**: PR changes exactly one row (no batch tampering)

## Branch Naming Convention

```
wl/<rig-handle>/<operation>-<wanted-id>
```

Examples:
- `wl/hobbes-rig/claim-w-com-001`
- `wl/hobbes-rig/done-w-com-001`
- `wl/hobbes-rig/stamp-c-abc123`

## Implementation Files

| File | Changes |
|------|---------|
| `internal/cmd/wl_claim.go` | Add `--pr-mode` flag, branch+commit+push+PR flow |
| `internal/cmd/wl_done.go` | Add `--pr-mode` flag, same branch flow |
| `internal/wasteland/wasteland.go` | Add `CreateDoltHubPR()`, `DoltBranchWrite()` helpers |
| `internal/doltserver/wl_commons.go` | Add branch-aware mutation variants |

## Migration Path

1. **Phase 2a** (current): `--pr-mode` as opt-in flag. Direct writes still default.
2. **Phase 2b**: `--pr-mode` becomes default. Direct writes available via `--direct`.
3. **Phase 3**: Direct writes removed. All mutations go through PRs.

## Risks and Mitigations

| Risk | Mitigation |
|------|-----------|
| DoltHub PR API may have undocumented behavior | Implemented with clear error messages; fallback to direct write |
| Branch push may fail if fork is out of sync | `gt wl sync` before PR operations; retry with rebase |
| Auto-merge for trusted rigs could be exploited | Validation checks run regardless of trust tier |
| Network latency for PR creation | Async PR creation with status polling |
