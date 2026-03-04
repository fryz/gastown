# Spider Protocol: Fraud Detection Queries

> **Version:** 0.1 (draft)
> **Related:** [HOP Protocol Spec](protocol-spec.md), [Constitution Questionnaire](chain-constitution-questionnaire.md), w-hop-003

## Overview

The Spider Protocol is the auditing layer of the HOP federation. Spiders
are automated agents that crawl the `wl-commons` database to detect
anomalies, enforce constitution rules, and maintain data integrity.

This document defines the fraud patterns, detection queries, alert
thresholds, and automated response actions.

---

## Target Schema

All queries run against the `hop/wl-commons` database on DoltHub. The
relevant tables are:

| Table | Key columns for fraud detection |
|---|---|
| `wanted` | `id`, `posted_by`, `claimed_by`, `status`, `evidence_url`, `created_at`, `updated_at` |
| `completions` | `id`, `wanted_id`, `completed_by`, `evidence`, `validated_by`, `completed_at`, `validated_at` |
| `stamps` | `id`, `author`, `subject`, `valence`, `confidence`, `severity`, `created_at` |
| `rigs` | `handle`, `trust_level`, `registered_at`, `last_seen`, `rig_type`, `parent_rig` |
| `badges` | `id`, `rig_handle`, `badge_type`, `awarded_at` |

---

## Pattern 1: Claim-and-Abandon

**Description:** A rig claims wanted items but never completes them,
blocking other rigs from working on them.

**Severity:** Medium

### Detection Query

```sql
-- Stale claims: items claimed more than N days ago with no completion
SELECT
    w.id AS wanted_id,
    w.title,
    w.claimed_by,
    w.updated_at AS claimed_at,
    DATEDIFF(NOW(), w.updated_at) AS days_stale
FROM wanted w
LEFT JOIN completions c ON c.wanted_id = w.id
WHERE w.status = 'claimed'
  AND c.id IS NULL
  AND w.updated_at < DATE_SUB(NOW(), INTERVAL 7 DAY)
ORDER BY days_stale DESC;
```

### Repeat Offender Query

```sql
-- Rigs with multiple abandoned claims
SELECT
    w.claimed_by AS rig_handle,
    COUNT(*) AS stale_claims,
    MIN(DATEDIFF(NOW(), w.updated_at)) AS min_days_stale,
    MAX(DATEDIFF(NOW(), w.updated_at)) AS max_days_stale
FROM wanted w
LEFT JOIN completions c ON c.wanted_id = w.id
WHERE w.status = 'claimed'
  AND c.id IS NULL
  AND w.updated_at < DATE_SUB(NOW(), INTERVAL 7 DAY)
GROUP BY w.claimed_by
HAVING COUNT(*) >= 2
ORDER BY stale_claims DESC;
```

### Thresholds

| Metric | Warning | Alert | Critical |
|---|---|---|---|
| Single item stale days | 7 days | 14 days | 30 days |
| Concurrent stale claims per rig | 2 | 3 | 5+ |
| Stale-to-completed ratio | > 0.3 | > 0.5 | > 0.8 |

### Automated Responses

1. **Warning (7 days):** Herald sends notification to the claiming rig
2. **Alert (14 days):** Unclaim the item (revert to `open` status)
3. **Critical (30 days or 5+ stale):** Temporary claim cooldown for the rig

---

## Pattern 2: Self-Validation Circumvention

**Description:** A rig attempts to validate its own completions, either
directly or through a sock puppet (an agent rig it controls).

**Severity:** High

### Direct Self-Validation Query

```sql
-- Completions where the completer and validator are the same rig
-- (The stamps table has a CHECK constraint, but completions.validated_by
-- does not — this catches any bypass)
SELECT
    c.id AS completion_id,
    c.wanted_id,
    c.completed_by,
    c.validated_by,
    c.completed_at
FROM completions c
WHERE c.validated_by IS NOT NULL
  AND c.completed_by = c.validated_by;
```

### Sock Puppet Validation Query

```sql
-- Completions validated by an agent rig whose parent is the completer
SELECT
    c.id AS completion_id,
    c.wanted_id,
    c.completed_by,
    c.validated_by,
    r_validator.parent_rig AS validator_parent
FROM completions c
JOIN rigs r_validator ON r_validator.handle = c.validated_by
WHERE c.validated_by IS NOT NULL
  AND r_validator.parent_rig = c.completed_by;
```

### Reverse Sock Puppet Query

```sql
-- Completions where the completer is an agent whose parent validated
SELECT
    c.id AS completion_id,
    c.wanted_id,
    c.completed_by,
    c.validated_by,
    r_completer.parent_rig AS completer_parent
FROM completions c
JOIN rigs r_completer ON r_completer.handle = c.completed_by
WHERE c.validated_by IS NOT NULL
  AND r_completer.parent_rig = c.validated_by;
```

### Thresholds

| Metric | Action |
|---|---|
| Any direct self-validation | Immediate alert, invalidate completion |
| Any sock puppet validation | Alert, flag for manual review |
| Rig with 2+ sock puppet validations | Suspend validation privileges |

### Automated Responses

1. **Direct self-validation:** Invalidate the completion, issue negative root stamp
2. **Sock puppet (first offense):** Flag for manual review, warning stamp
3. **Sock puppet (repeat):** Suspend both parent and child rigs' validation rights

---

## Pattern 3: Rapid Claim-Complete Cycles

**Description:** A rig claims and completes items suspiciously fast,
suggesting pre-fabricated or low-quality work.

**Severity:** Medium

### Detection Query

```sql
-- Completions where claim-to-completion time is under 5 minutes
SELECT
    c.id AS completion_id,
    c.wanted_id,
    w.title,
    c.completed_by,
    w.effort_level,
    w.updated_at AS last_status_change,
    c.completed_at,
    TIMESTAMPDIFF(MINUTE, w.updated_at, c.completed_at) AS minutes_to_complete
FROM completions c
JOIN wanted w ON w.id = c.wanted_id
WHERE TIMESTAMPDIFF(MINUTE, w.updated_at, c.completed_at) < 5
  AND w.effort_level IN ('medium', 'large')
ORDER BY minutes_to_complete ASC;
```

### Velocity Anomaly Query

```sql
-- Rigs with abnormally high completion rates (> 5 per day)
SELECT
    c.completed_by,
    DATE(c.completed_at) AS completion_date,
    COUNT(*) AS completions_per_day
FROM completions c
WHERE c.completed_at >= DATE_SUB(NOW(), INTERVAL 30 DAY)
GROUP BY c.completed_by, DATE(c.completed_at)
HAVING COUNT(*) > 5
ORDER BY completions_per_day DESC;
```

### Thresholds

| Metric | Warning | Alert | Critical |
|---|---|---|---|
| Time to complete (medium effort) | < 30 min | < 10 min | < 5 min |
| Time to complete (large effort) | < 2 hours | < 30 min | < 10 min |
| Completions per day per rig | > 5 | > 10 | > 20 |

### Automated Responses

1. **Warning:** Flag completions for priority review
2. **Alert:** Require manual validation (skip auto-validate)
3. **Critical:** Suspend completion privileges, require manual review of all pending work

---

## Pattern 4: Evidence Quality

**Description:** Completions with missing, invalid, or duplicate evidence
URLs.

**Severity:** Medium

### Missing Evidence Query

```sql
-- Completions with no evidence
SELECT
    c.id AS completion_id,
    c.wanted_id,
    c.completed_by,
    c.evidence,
    c.completed_at
FROM completions c
WHERE c.evidence IS NULL
   OR TRIM(c.evidence) = '';
```

### Duplicate Evidence Query

```sql
-- Multiple completions pointing to the same evidence URL
SELECT
    c.evidence,
    COUNT(*) AS usage_count,
    GROUP_CONCAT(c.id) AS completion_ids,
    GROUP_CONCAT(c.wanted_id) AS wanted_ids
FROM completions c
WHERE c.evidence IS NOT NULL
  AND TRIM(c.evidence) != ''
GROUP BY c.evidence
HAVING COUNT(*) > 1
ORDER BY usage_count DESC;
```

### Invalid Evidence URL Query

```sql
-- Evidence URLs that do not look like valid GitHub PR/commit URLs
SELECT
    c.id AS completion_id,
    c.wanted_id,
    c.completed_by,
    c.evidence
FROM completions c
WHERE c.evidence IS NOT NULL
  AND c.evidence NOT LIKE 'https://github.com/%/pull/%'
  AND c.evidence NOT LIKE 'https://github.com/%/commit/%'
  AND c.evidence NOT LIKE 'https://github.com/%/issues/%'
  AND c.evidence NOT LIKE 'https://%.com/%'
ORDER BY c.completed_at DESC;
```

### Thresholds

| Metric | Action |
|---|---|
| Missing evidence | Block validation until evidence provided |
| Duplicate evidence across different wanted items | Flag for review |
| Non-URL evidence | Warning (may be legitimate for non-code items) |

### Automated Responses

1. **Missing evidence:** Completion stays in `in_review`, herald notifies completer
2. **Duplicate evidence:** Flag all related completions for manual review
3. **Invalid URL:** Warning stamp, request clarification

---

## Pattern 5: Mutual Validation Rings

**Description:** A group of rigs exclusively validates each other's work,
forming a closed trust circle that may inflate reputation.

**Severity:** High

### Detection Query

```sql
-- Pairs of rigs that validate each other (A validates B AND B validates A)
SELECT
    c1.validated_by AS rig_a,
    c1.completed_by AS rig_b,
    COUNT(*) AS a_validates_b,
    (
        SELECT COUNT(*)
        FROM completions c2
        WHERE c2.completed_by = c1.validated_by
          AND c2.validated_by = c1.completed_by
    ) AS b_validates_a
FROM completions c1
WHERE c1.validated_by IS NOT NULL
GROUP BY c1.validated_by, c1.completed_by
HAVING a_validates_b >= 2
   AND b_validates_a >= 2
ORDER BY (a_validates_b + b_validates_a) DESC;
```

### Validation Concentration Query

```sql
-- Rigs where > 80% of their validations come from a single validator
SELECT
    c.completed_by,
    c.validated_by,
    COUNT(*) AS validations_from_this_rig,
    (
        SELECT COUNT(*)
        FROM completions c2
        WHERE c2.completed_by = c.completed_by
          AND c2.validated_by IS NOT NULL
    ) AS total_validations,
    ROUND(
        COUNT(*) * 100.0 / NULLIF((
            SELECT COUNT(*)
            FROM completions c3
            WHERE c3.completed_by = c.completed_by
              AND c3.validated_by IS NOT NULL
        ), 0), 1
    ) AS pct_from_single_validator
FROM completions c
WHERE c.validated_by IS NOT NULL
GROUP BY c.completed_by, c.validated_by
HAVING pct_from_single_validator > 80
   AND total_validations >= 3
ORDER BY pct_from_single_validator DESC;
```

### Thresholds

| Metric | Warning | Alert | Critical |
|---|---|---|---|
| Mutual validation count (per pair) | 3 | 5 | 10+ |
| Validation concentration from single source | > 60% | > 80% | > 95% |
| Ring size (rigs in closed validation circle) | 2 | 3 | 4+ |

### Automated Responses

1. **Warning:** Log observation, no action
2. **Alert:** Require outside validator for next N completions
3. **Critical:** Reset trust levels for ring members, require re-validation of recent completions

---

## Pattern 6: Sock Puppet Networks

**Description:** A single entity registers multiple rigs to game the
system -- inflating reputation, self-validating, or hoarding claims.

**Severity:** Critical

### Agent Tree Query

```sql
-- Map parent-child rig relationships and activity
SELECT
    r_parent.handle AS parent_rig,
    r_parent.trust_level AS parent_trust,
    r_child.handle AS child_rig,
    r_child.rig_type AS child_type,
    (
        SELECT COUNT(*) FROM completions c WHERE c.completed_by = r_child.handle
    ) AS child_completions,
    (
        SELECT COUNT(*)
        FROM completions c
        WHERE c.completed_by = r_child.handle
          AND c.validated_by = r_parent.handle
    ) AS parent_validated_child
FROM rigs r_child
JOIN rigs r_parent ON r_parent.handle = r_child.parent_rig
WHERE r_child.parent_rig IS NOT NULL
ORDER BY parent_rig, child_rig;
```

### Registration Burst Query

```sql
-- Multiple rigs registered within a short time window from same org
SELECT
    dolthub_org,
    COUNT(*) AS rigs_registered,
    MIN(registered_at) AS first_registration,
    MAX(registered_at) AS last_registration,
    TIMESTAMPDIFF(HOUR, MIN(registered_at), MAX(registered_at)) AS hours_span
FROM rigs
GROUP BY dolthub_org
HAVING COUNT(*) > 3
   AND TIMESTAMPDIFF(HOUR, MIN(registered_at), MAX(registered_at)) < 24
ORDER BY rigs_registered DESC;
```

### Coordinated Activity Query

```sql
-- Rigs from the same org that claim items in rapid succession
SELECT
    r.dolthub_org,
    w.id AS wanted_id,
    w.claimed_by,
    w.updated_at AS claim_time
FROM wanted w
JOIN rigs r ON r.handle = w.claimed_by
WHERE w.status IN ('claimed', 'in_review', 'done')
  AND r.dolthub_org IN (
      SELECT dolthub_org FROM rigs GROUP BY dolthub_org HAVING COUNT(*) > 2
  )
ORDER BY r.dolthub_org, w.updated_at;
```

### Thresholds

| Metric | Warning | Alert | Critical |
|---|---|---|---|
| Rigs per org | 3 | 5 | 10+ |
| Agent rigs per parent | 2 | 3 | 5+ |
| Parent validating own agents (%) | > 50% | > 75% | > 90% |

### Automated Responses

1. **Warning:** Flag for manual review
2. **Alert:** Suspend new rig registration for the org
3. **Critical:** Suspend all rigs in the network, escalate to chain maintainer

---

## Pattern 7: Stamp Manipulation

**Description:** Rigs issuing stamps to manipulate trust scores --
excessive positive stamps, negative stamp campaigns, or confidence
inflation.

**Severity:** High

### Stamp Flood Query

```sql
-- Rigs issuing abnormally many stamps in a short period
SELECT
    author,
    DATE(created_at) AS stamp_date,
    COUNT(*) AS stamps_issued,
    COUNT(DISTINCT subject) AS unique_subjects
FROM stamps
WHERE created_at >= DATE_SUB(NOW(), INTERVAL 7 DAY)
GROUP BY author, DATE(created_at)
HAVING COUNT(*) > 10
ORDER BY stamps_issued DESC;
```

### Negative Stamp Campaign Query

```sql
-- Rigs that issue predominantly negative stamps toward a single subject
SELECT
    s.author,
    s.subject,
    COUNT(*) AS total_stamps,
    SUM(CASE WHEN JSON_EXTRACT(s.valence, '$.positive') = false THEN 1 ELSE 0 END) AS negative_stamps,
    ROUND(
        SUM(CASE WHEN JSON_EXTRACT(s.valence, '$.positive') = false THEN 1 ELSE 0 END) * 100.0 / COUNT(*), 1
    ) AS pct_negative
FROM stamps s
GROUP BY s.author, s.subject
HAVING COUNT(*) >= 3
   AND pct_negative > 75
ORDER BY negative_stamps DESC;
```

### Confidence Inflation Query

```sql
-- Stamps with unusually high confidence from rigs that should not be issuing
-- root-level stamps
SELECT
    s.id,
    s.author,
    s.subject,
    s.severity,
    s.confidence,
    r.trust_level AS author_trust
FROM stamps s
JOIN rigs r ON r.handle = s.author
WHERE s.severity = 'root'
  AND r.trust_level < 3
ORDER BY s.created_at DESC;
```

### Thresholds

| Metric | Warning | Alert | Critical |
|---|---|---|---|
| Stamps per day per author | > 10 | > 20 | > 50 |
| Negative stamps toward single subject | 3 | 5 | 10+ |
| Root stamps from trust < 3 | Any | -- | -- |

### Automated Responses

1. **Stamp flood:** Rate-limit stamping for the author
2. **Negative campaign:** Flag for review, freeze involved stamps
3. **Root stamp from low-trust rig:** Invalidate stamp, issue warning

---

## Pattern 8: Post-and-Complete (Self-Serving Items)

**Description:** A rig posts wanted items and then claims and completes
them itself, creating the appearance of productivity without external
demand.

**Severity:** Medium

### Detection Query

```sql
-- Items where the poster also completed the item
SELECT
    w.id AS wanted_id,
    w.title,
    w.posted_by,
    c.completed_by,
    w.created_at AS posted_at,
    c.completed_at
FROM wanted w
JOIN completions c ON c.wanted_id = w.id
WHERE w.posted_by = c.completed_by
ORDER BY w.created_at DESC;
```

### Post-and-Complete Ratio Query

```sql
-- Rigs where a high percentage of their completions are self-posted items
SELECT
    c.completed_by,
    COUNT(*) AS total_completions,
    SUM(CASE WHEN w.posted_by = c.completed_by THEN 1 ELSE 0 END) AS self_posted,
    ROUND(
        SUM(CASE WHEN w.posted_by = c.completed_by THEN 1 ELSE 0 END) * 100.0 / COUNT(*), 1
    ) AS self_post_pct
FROM completions c
JOIN wanted w ON w.id = c.wanted_id
GROUP BY c.completed_by
HAVING COUNT(*) >= 3
ORDER BY self_post_pct DESC;
```

### Thresholds

| Metric | Warning | Alert | Critical |
|---|---|---|---|
| Self-post-and-complete ratio | > 50% | > 75% | > 90% |
| Self-posted completions (absolute) | 3 | 5 | 10+ |

### Automated Responses

1. **Warning:** Log observation (self-posting is sometimes legitimate)
2. **Alert:** Require external validation for self-posted completions
3. **Critical:** Discount self-posted completions from trust calculation

---

## Spider Execution Schedule

| Spider | Frequency | Priority |
|---|---|---|
| Stale claims (Pattern 1) | Daily | Medium |
| Self-validation (Pattern 2) | On every new validation | High |
| Rapid cycles (Pattern 3) | Daily | Medium |
| Evidence quality (Pattern 4) | On every new completion | Medium |
| Validation rings (Pattern 5) | Weekly | High |
| Sock puppets (Pattern 6) | Weekly | Critical |
| Stamp manipulation (Pattern 7) | Daily | High |
| Post-and-complete (Pattern 8) | Weekly | Medium |

---

## Alert Routing

| Severity | Destination | Response Time |
|---|---|---|
| Warning | Spider log, herald digest | Next scheduled review |
| Alert | Herald notification to chain maintainer | Within 24 hours |
| Critical | Herald notification + automatic enforcement action | Immediate |

---

## Implementation Notes

1. **Phase 1 (current):** Spiders run as manual SQL queries against the
   DoltHub API. A maintainer reviews results and takes action manually.

2. **Phase 2 (planned):** Spiders run as scheduled jobs (cron or herald-
   triggered) that write results to a `spider_findings` table and
   automatically enforce responses below the "alert" threshold.

3. **Phase 3 (planned):** Spider results feed into trust score computation.
   The `rigs.trust_level` field is automatically adjusted based on
   accumulated spider findings.

All spider queries are designed to run against the DoltHub SQL API and
are compatible with MySQL/Dolt SQL syntax.
