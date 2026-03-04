# Character Sheet Visualization Design

**Wanted item:** w-com-003
**Status:** Design document
**Date:** 2026-03-04

## 1. Overview

A character sheet is a contributor's at-a-glance reputation profile in the
Wasteland. It aggregates data from completions, stamps, and badges to show
what someone has done, how well they did it, and how much the community trusts
them.

Character sheets serve three purposes:

- **Trust signal** — Town mayors and other contributors can quickly assess
  whether to accept work from a given rig.
- **Skill discovery** — Contributors can find collaborators with specific
  expertise (Go, federation, testing, etc.).
- **Motivation** — Visible progression from T0 newcomer to T3 elder encourages
  sustained contribution.

## 2. Data Model

The character sheet draws from four tables in `wl-commons`:

### rigs

Primary identity table. Each contributor registers a rig.

| Field | Type | Usage |
|-------|------|-------|
| `handle` | varchar(255) PK | Unique identifier, displayed as `@handle` |
| `display_name` | varchar(255) | Human-readable name |
| `dolthub_org` | varchar(255) | DoltHub organization for linking |
| `trust_level` | int | Current trust tier (0-3) |
| `registered_at` | timestamp | Account age |
| `last_seen` | timestamp | Activity recency |
| `rig_type` | varchar(16) | `human` or `agent` |
| `parent_rig` | varchar(255) | For agent rigs, the owning human rig |

### completions

Records of finished wanted items.

| Field | Type | Usage |
|-------|------|-------|
| `id` | varchar(64) PK | Completion identifier |
| `wanted_id` | varchar(64) | Links to the wanted item |
| `completed_by` | varchar(255) | Rig handle who did the work |
| `evidence` | text | PR URL or other proof |
| `validated_by` | varchar(255) | Rig handle of validator |
| `stamp_id` | varchar(64) | Associated quality stamp |
| `completed_at` | timestamp | When work was finished |
| `validated_at` | timestamp | When work was validated |

### stamps

Quality and reputation signals attached to completions.

| Field | Type | Usage |
|-------|------|-------|
| `id` | varchar(64) PK | Stamp identifier |
| `author` | varchar(255) | Who issued the stamp |
| `subject` | varchar(255) | Who the stamp is about |
| `valence` | json | Score dimensions, e.g. `{"quality":0.85,"reliability":0.8}` |
| `confidence` | float | Author's confidence in the assessment |
| `skill_tags` | json | Skills demonstrated, e.g. `["Go","cleanup"]` |
| `message` | text | Human-readable assessment |
| `context_type` | varchar(32) | Usually `completion` |
| `created_at` | timestamp | When stamp was issued |

### badges

Special achievements awarded to contributors.

| Field | Type | Usage |
|-------|------|-------|
| `id` | varchar(64) PK | Badge identifier |
| `rig_handle` | varchar(255) | Recipient |
| `badge_type` | varchar(64) | Badge category |
| `awarded_at` | timestamp | When badge was granted |
| `evidence` | text | Why badge was awarded |

### wanted (supplementary)

Used to resolve `wanted_id` references into titles and project names for
display in recent completions.

| Field | Type | Usage |
|-------|------|-------|
| `id` | varchar(64) PK | Wanted item ID |
| `title` | text | Item title |
| `project` | varchar(64) | Project name (gastown, beads, hop, etc.) |
| `tags` | json | Category tags |
| `type` | varchar(32) | Work type (bug, feature, docs, design, etc.) |

## 3. Visual Design

### ASCII Mockup — CLI Output

```
+-------------------------------------------------------+
|  Alice's Workshop                       Trust: T2      |
|  @alice-rig  DoltHub: alice  Type: human               |
|  Member since: 2026-01-15   Last active: 2026-03-02   |
|                                                        |
|  Completed: 12   Validated: 8   Stamps: 10   Badges: 3|
|                                                        |
|  Skills                                                |
|  ========== Go              ======.... Documentation   |
|  ========.. TypeScript      ===....... Design          |
|  =======... federation      ==........ testing         |
|                                                        |
|  Reputation Scores (avg across stamps)                 |
|  quality: 0.81   reliability: 0.76   speed: 0.85      |
|                                                        |
|  Recent Completions                                    |
|  [ok] Fix auth timeout bug          gastown  Mar 2026  |
|  [ok] Add federation sync docs      hop      Feb 2026  |
|  [ok] Remove daemon infra           beads    Feb 2026  |
|  [--] Survey orchestration fmwks    gas-city Mar 2026  |
|                                                        |
|  Badges                                                |
|  [first-blood]  First completion validated             |
|  [full-cycle]   Completed post-claim-validate cycle    |
|  [multi-town]   Contributed to 3+ projects             |
+-------------------------------------------------------+
```

### Design Notes

- **`[ok]`** = validated completion, **`[--]`** = pending validation.
- **Skill bars** use `=` for filled and `.` for empty, 10 characters wide.
  Skill level is derived from stamp counts and average quality scores for
  that skill tag.
- **Reputation scores** are weighted averages from stamp valence fields,
  weighted by the author's own trust level and confidence score.
- **Badge display** shows `badge_type` and a short description derived from
  `evidence`.
- The CLI uses no emoji or unicode box-drawing to ensure compatibility across
  terminals. The web view can use richer formatting.

### Web View Enhancements

On a web-rendered view (DoltHub or a dedicated page), the character sheet
can include:

- Color-coded trust tier badge (gray T0, blue T1, green T2, gold T3).
- Clickable completion links to PR evidence URLs.
- Hover tooltips on skill bars showing exact scores.
- Contribution timeline heatmap (similar to GitHub contribution graph).

## 4. Access Methods

### CLI

```bash
gt wl profile <handle>
```

Queries `wl-commons` via DoltHub SQL API and renders the ASCII character
sheet to the terminal. Flags:

- `--json` — Output raw JSON instead of formatted text.
- `--completions N` — Number of recent completions to show (default 5).
- `--project <name>` — Filter to completions in a specific project.

### Web

A character sheet can be viewed at:

```
https://www.dolthub.com/repositories/hop/wl-commons/query/main?q=<profile-query>
```

Or, if a dedicated page is built, at a path like:

```
https://www.dolthub.com/hop/wl-commons/profiles/<handle>
```

### API

Direct SQL queries against DoltHub (see Section 5).

## 5. Implementation Notes — SQL Queries

### Core profile query

```sql
SELECT handle, display_name, dolthub_org, trust_level,
       rig_type, parent_rig, registered_at, last_seen
FROM rigs
WHERE handle = ?
```

### Completion summary

```sql
SELECT c.id, c.wanted_id, w.title, w.project, c.evidence,
       c.completed_at, c.validated_at, c.validated_by
FROM completions c
LEFT JOIN wanted w ON c.wanted_id = w.id
WHERE c.completed_by = ?
ORDER BY c.completed_at DESC
LIMIT ?
```

### Aggregate stats

```sql
SELECT
  COUNT(*) AS total_completions,
  SUM(CASE WHEN validated_at IS NOT NULL THEN 1 ELSE 0 END) AS validated
FROM completions
WHERE completed_by = ?
```

### Stamps received

```sql
SELECT COUNT(*) AS stamp_count,
       AVG(confidence) AS avg_confidence
FROM stamps
WHERE subject = ?
```

### Skill breakdown

```sql
SELECT skill_tag,
       COUNT(*) AS uses,
       AVG(JSON_EXTRACT(valence, '$.quality')) AS avg_quality
FROM stamps,
     JSON_TABLE(skill_tags, '$[*]' COLUMNS(skill_tag VARCHAR(64) PATH '$')) AS jt
WHERE subject = ?
GROUP BY skill_tag
ORDER BY uses DESC
```

### Reputation scores (weighted averages)

```sql
SELECT
  AVG(JSON_EXTRACT(s.valence, '$.quality') * s.confidence * r.trust_level)
    / AVG(s.confidence * r.trust_level) AS weighted_quality,
  AVG(JSON_EXTRACT(s.valence, '$.reliability') * s.confidence * r.trust_level)
    / AVG(s.confidence * r.trust_level) AS weighted_reliability
FROM stamps s
JOIN rigs r ON s.author = r.handle
WHERE s.subject = ?
```

### Badges

```sql
SELECT badge_type, awarded_at, evidence
FROM badges
WHERE rig_handle = ?
ORDER BY awarded_at DESC
```

## 6. Trust Tier Progression

| Tier | Name | Requirements | Privileges |
|------|------|-------------|------------|
| T0 | Newcomer | Register a rig | Can browse wanted items, claim low-priority work |
| T1 | Contributor | 1+ validated completion with positive stamps | Can claim medium-priority items, stamp other T0 work |
| T2 | Trusted | 5+ validated completions, avg quality >= 0.7, stamps from 2+ distinct authors | Can claim high-priority items, validate completions, stamp T0-T1 work |
| T3 | Elder | 15+ validated completions, avg quality >= 0.8, contributed to 3+ projects, nominated by a T3 | Can post wanted items, modify trust levels, act as town validator |

### Tier Display on Character Sheet

The trust tier is prominently displayed in the header. The CLI shows it as
`Trust: T2` while the web view uses a colored badge. The tier name
(Newcomer/Contributor/Trusted/Elder) appears on hover or with `--verbose`.

### Progression Tracking

The character sheet can optionally show progress toward the next tier:

```
  Next tier: T3 Elder
  Completions: 12/15    Avg quality: 0.81/0.80    Projects: 3/3
  Remaining: nomination from a T3 elder
```

## 7. Skill Derivation

Skills are inferred from the `skill_tags` JSON array on stamps. Each stamp
issued for a completion includes tags describing the skills demonstrated.

### Derivation Algorithm

1. **Collect** all stamps where `subject = <handle>`.
2. **Extract** `skill_tags` from each stamp and flatten into a list.
3. **Group** by tag and compute:
   - `count` — how many stamps reference this skill.
   - `avg_quality` — average of `valence.quality` for stamps with this tag.
   - `recency` — time decay factor based on `created_at`.
4. **Score** each skill: `score = count * avg_quality * recency_factor`.
5. **Normalize** scores to a 0-10 scale for the bar display.
6. **Rank** and display the top 6 skills on the character sheet.

### Skill Tag Conventions

Stamps should use consistent lowercase tags. Common categories:

- **Languages:** `go`, `typescript`, `python`, `sql`
- **Domains:** `federation`, `testing`, `cleanup`, `lifecycle`, `design`
- **Projects:** `gastown`, `beads`, `hop`, `polecat`

Tags are free-form, so the system should normalize common variants
(e.g., `Go` and `go` map to the same skill) and ignore tags with fewer
than 2 occurrences for display purposes.

### Stamp Quality Weighting

Not all stamps are equal. The skill score weights stamps by:

- **Author trust level** — A T3 elder's stamp carries more weight than T0.
- **Confidence** — The stamp author's self-reported confidence (0.0 to 1.0).
- **Severity** — `root` stamps (aggregate assessments) weigh more than `leaf`
  stamps (single-completion assessments).

## 8. Future Considerations

- **Comparative views** — Side-by-side character sheets for team composition.
- **Skill search** — Query rigs by skill to find contributors for specific work.
- **Reputation decay** — Skills and trust could decay without recent activity.
- **Agent character sheets** — Agent rigs (with `parent_rig`) could show
  both their own stats and their parent human's oversight record.
- **Exportable badges** — SVG or image badges for use in GitHub profiles or
  external sites.
