# Stamp Validation Pipeline

**Wanted Item:** w-wl-002
**Status:** Design + prototype

## Overview

Stamps are attestations in the Wasteland that validate completed work. A stamp records that a reviewer (the "author") has examined a contributor's (the "subject") work and attests to its quality. The stamp validation pipeline automatically checks stamps for correctness, consistency, and fraud patterns before they are accepted into the commons.

## Stamps Schema

```sql
CREATE TABLE stamps (
    id              VARCHAR(64) PRIMARY KEY,
    author          VARCHAR(255) NOT NULL,   -- the reviewer/stamper
    subject         VARCHAR(255) NOT NULL,   -- the rig being reviewed
    valence         JSON NOT NULL,           -- positive/negative signal
    confidence      FLOAT DEFAULT 1,         -- 0.0 to 1.0
    severity        VARCHAR(16) DEFAULT 'leaf',  -- leaf, branch, root
    context_id      VARCHAR(64),             -- linked completion/wanted ID
    context_type    VARCHAR(32),             -- 'completion', 'wanted', etc.
    skill_tags      JSON,                    -- skills demonstrated
    message         TEXT,                    -- review comments
    prev_stamp_hash VARCHAR(64),             -- hash chain link
    block_hash      VARCHAR(64),             -- block-level hash
    hop_uri         VARCHAR(512),            -- federation URI
    created_at      TIMESTAMP,
    CHECK (NOT(author = subject))            -- no self-stamping
);
```

## Validation Rules

### Rule 1: Author Exists in Rigs Table

The stamp author must be a registered rig.

```sql
SELECT s.id, s.author
FROM stamps s
LEFT JOIN rigs r ON s.author = r.handle
WHERE r.handle IS NULL;
```

**Severity:** REJECT -- stamps from unregistered rigs are invalid.

### Rule 2: Subject Exists in Rigs Table

The stamp subject must be a registered rig.

```sql
SELECT s.id, s.subject
FROM stamps s
LEFT JOIN rigs r ON s.subject = r.handle
WHERE r.handle IS NULL;
```

**Severity:** REJECT -- stamps for unregistered rigs are invalid.

### Rule 3: No Self-Stamping

The database has a CHECK constraint, but we validate at the application level too.

```sql
SELECT id, author, subject
FROM stamps
WHERE author = subject;
```

**Severity:** REJECT -- self-attestation is never valid.

### Rule 4: Author Has Sufficient Trust Tier

Only rigs at trust_level >= 2 (vouched) should be able to stamp others.

```sql
SELECT s.id, s.author, r.trust_level
FROM stamps s
JOIN rigs r ON s.author = r.handle
WHERE r.trust_level < 2;
```

**Severity:** REJECT -- low-trust rigs cannot issue stamps.

### Rule 5: Completion Exists (when context_type = 'completion')

If a stamp references a completion, that completion must exist.

```sql
SELECT s.id, s.context_id
FROM stamps s
LEFT JOIN completions c ON s.context_id = c.id
WHERE s.context_type = 'completion' AND c.id IS NULL;
```

**Severity:** REJECT -- stamps referencing nonexistent completions are invalid.

### Rule 6: No Duplicate Stamps

One author should not stamp the same subject for the same context more than once.

```sql
SELECT author, subject, context_id, COUNT(*) as cnt
FROM stamps
WHERE context_id IS NOT NULL
GROUP BY author, subject, context_id
HAVING cnt > 1;
```

**Severity:** WARN -- duplicates should be flagged; the latest may be an update.

### Rule 7: Hash Chain Integrity

If `prev_stamp_hash` is set, it must reference an existing stamp's `block_hash`.

```sql
SELECT s.id, s.prev_stamp_hash
FROM stamps s
WHERE s.prev_stamp_hash IS NOT NULL
AND NOT EXISTS (
    SELECT 1 FROM stamps s2 WHERE s2.block_hash = s.prev_stamp_hash
);
```

**Severity:** WARN -- broken chain links indicate data corruption or missing stamps.

## Fraud Detection Patterns

These patterns are inspired by the Spider Protocol fraud detection work (w-hop-003).

### Pattern A: Stamp Ring Detection

Detect mutual stamping rings (A stamps B, B stamps A).

```sql
SELECT s1.author as rig_a, s1.subject as rig_b, COUNT(*) as mutual_stamps
FROM stamps s1
JOIN stamps s2 ON s1.author = s2.subject AND s1.subject = s2.author
GROUP BY rig_a, rig_b
HAVING mutual_stamps > 1;
```

### Pattern B: Rapid Stamping (Rubber-Stamping)

Detect rigs that stamp too quickly (< 5 minutes per stamp).

```sql
SELECT author, COUNT(*) as stamp_count,
    MIN(created_at) as first_stamp, MAX(created_at) as last_stamp,
    TIMESTAMPDIFF(MINUTE, MIN(created_at), MAX(created_at)) as window_minutes
FROM stamps
GROUP BY author
HAVING stamp_count > 5
AND TIMESTAMPDIFF(MINUTE, MIN(created_at), MAX(created_at)) < stamp_count * 5;
```

### Pattern C: Single-Source Dependency

Detect subjects who receive all stamps from a single author.

```sql
SELECT subject, COUNT(DISTINCT author) as unique_stampers, COUNT(*) as total_stamps
FROM stamps
GROUP BY subject
HAVING total_stamps > 2 AND unique_stampers = 1;
```

## Pipeline Implementation

The prototype is in `internal/doltserver/stamp_validation.go`. It provides a `ValidateStamps()` function that runs all validation rules and fraud detection patterns against the local wl-commons database.

### Pipeline Steps

1. **Fetch all stamps**: Count total stamps in the database.
2. **Run validation rules**: Execute Rules 1-7 against the stamp set.
3. **Run fraud detection**: Execute Patterns A-C.
4. **Produce report**: Output validated/rejected stamp list with reasons.

### Result Structure

```go
type StampValidationResult struct {
    TotalStamps    int
    ValidStamps    int
    RejectedStamps []StampRejection
    Warnings       []StampWarning
    FraudAlerts    []FraudAlert
    RunAt          time.Time
}
```

## Future Work

- **Automated PR integration**: Run validation as a pre-merge check for stamp PRs (Phase 2 integration with w-wl-001).
- **CLI command**: `gt wl validate-stamps [--dry-run] [--json]`
- **Configurable thresholds**: Allow trust tier requirements and fraud detection parameters to be configured per-commons.
- **Cross-commons validation**: Validate stamps that reference completions in other commons databases.
- **Spider Protocol integration**: Once w-hop-003 lands, integrate its graph-based fraud detection queries.
