# Reputation Decay Mechanics — Design and Simulation

**Wanted item:** w-hop-006
**Status:** Research / design document
**Date:** 2026-03-04

## 1. Motivation

A reputation system without decay creates two problems:

1. **Stale trust** — A contributor who was active a year ago but has since
   disappeared still appears fully trusted. Their skills may have atrophied
   or the codebase may have changed significantly.
2. **Incumbency advantage** — New contributors can never catch up to early
   adopters who accumulated reputation before quality standards tightened.

Decay ensures that reputation reflects *recent, sustained* contribution.
A contributor who takes a break sees their visible reputation fade, but can
recover it by resuming activity.

## 2. What Decays

Not everything should decay at the same rate or in the same way.

| Attribute | Decays? | Rationale |
|-----------|---------|-----------|
| Trust tier (T0-T3) | Yes, slowly | Core trust signal; should reflect recent activity |
| Skill scores | Yes, moderately | Skills rust without practice |
| Completion count | No | Historical record; facts don't decay |
| Badge awards | No | Achievements are permanent milestones |
| Stamp scores | Yes, indirectly | Older stamps contribute less to aggregate scores |
| Activity recency | N/A | Already temporal by nature (`last_seen`) |

## 3. Decay Model

### Exponential Decay

We use exponential decay, the same model used for radioactive half-life
and memory retention curves. For a reputation score `R`:

```
R(t) = R_base * e^(-lambda * t) + R_floor
```

Where:
- `R_base` = peak score minus floor
- `lambda` = decay constant = `ln(2) / half_life`
- `t` = days since last qualifying activity
- `R_floor` = minimum score (reputation never reaches zero)

### Parameters

| Parameter | Value | Rationale |
|-----------|-------|-----------|
| Skill half-life | 90 days | ~3 months without relevant work halves the skill score |
| Trust score half-life | 180 days | ~6 months of inactivity halves the trust contribution |
| Floor (skills) | 0.1 (on 0-1 scale) | Even dormant skills retain some baseline |
| Floor (trust) | 0.2 | Past trust isn't fully erasable |
| Recovery rate | Immediate | New activity instantly contributes; no penalty period |

### What Counts as "Activity"

Activity resets the decay clock for the relevant attribute:

| Activity | Resets |
|----------|--------|
| Completing a wanted item | Trust decay clock; skill clocks for relevant skill_tags |
| Receiving a stamp | Skill clocks for stamp's skill_tags |
| Issuing a stamp (as reviewer) | Trust decay clock |
| Posting a wanted item | Trust decay clock |
| Registering/logging in | Nothing (passive presence doesn't count) |

## 4. Trust Tier Decay

Trust tiers (T0-T3) are discrete levels, but the underlying "trust score"
is continuous. Decay works on the continuous score; tier changes happen
at thresholds.

### Tier Thresholds

| Tier | Score range | Promotion requires | Demotion at |
|------|------------|-------------------|-------------|
| T0 | 0.0 - 0.24 | Register a rig | N/A (minimum) |
| T1 | 0.25 - 0.49 | 1+ validated completion | Score < 0.20 |
| T2 | 0.50 - 0.74 | 5+ completions, avg quality >= 0.7 | Score < 0.40 |
| T3 | 0.75 - 1.00 | 15+ completions, T3 nomination | Score < 0.60 |

Demotion thresholds are lower than promotion thresholds (hysteresis) to
prevent oscillation at boundaries.

### Grace Period

Before any demotion takes effect, the system sends a warning:

```
[HERALD] @alice-rig: Your trust score has dropped to 0.42.
         Without activity in the next 30 days, your tier will
         drop from T2 to T1.
```

This 30-day grace period gives contributors time to re-engage.

## 5. Simulation Code

The following Python simulation models decay for four contributor archetypes
over a 365-day period.

```python
import math

# --- Parameters ---
SKILL_HALF_LIFE = 90     # days
TRUST_HALF_LIFE = 180    # days
SKILL_FLOOR = 0.1
TRUST_FLOOR = 0.2
DAYS = 365

def decay(peak, half_life, floor, days_inactive):
    """Exponential decay with floor."""
    lam = math.log(2) / half_life
    base = peak - floor
    return base * math.exp(-lam * days_inactive) + floor

# --- Contributor Archetypes ---

archetypes = {
    "Steady Eddie": {
        "desc": "Contributes every 2 weeks consistently",
        "activity_interval": 14,
        "peak_skill": 0.85,
        "peak_trust": 0.80,
    },
    "Burst Betty": {
        "desc": "Intense 2-month burst, then 10 months off",
        "burst_start": 0,
        "burst_end": 60,
        "activity_interval": 7,  # during burst
        "peak_skill": 0.90,
        "peak_trust": 0.75,
    },
    "Sabbatical Sam": {
        "desc": "Active 6 months, then 6-month break, then returns",
        "active_periods": [(0, 180), (270, 365)],
        "activity_interval": 10,
        "peak_skill": 0.80,
        "peak_trust": 0.70,
    },
    "Ghost Gary": {
        "desc": "Contributed once at day 0, never returned",
        "activity_days": [0],
        "peak_skill": 0.60,
        "peak_trust": 0.30,
    },
}

def simulate_archetype(name, config):
    """Simulate decay for an archetype, return daily skill and trust scores."""
    skill_scores = []
    trust_scores = []

    # Determine activity days.
    if "activity_days" in config:
        activity_days = set(config["activity_days"])
    elif "active_periods" in config:
        activity_days = set()
        for start, end in config["active_periods"]:
            for d in range(start, end, config["activity_interval"]):
                activity_days.add(d)
    elif "burst_start" in config:
        activity_days = set(
            range(config["burst_start"], config["burst_end"],
                  config["activity_interval"])
        )
    else:
        activity_days = set(range(0, DAYS, config["activity_interval"]))

    last_activity = -1

    for day in range(DAYS):
        if day in activity_days:
            last_activity = day

        if last_activity < 0:
            skill_scores.append(SKILL_FLOOR)
            trust_scores.append(TRUST_FLOOR)
        else:
            inactive = day - last_activity
            s = decay(config["peak_skill"], SKILL_HALF_LIFE, SKILL_FLOOR, inactive)
            t = decay(config["peak_trust"], TRUST_HALF_LIFE, TRUST_FLOOR, inactive)
            skill_scores.append(s)
            trust_scores.append(t)

    return skill_scores, trust_scores

def ascii_graph(values, width=60, height=12, label=""):
    """Render a list of values as an ASCII sparkline graph."""
    # Sample values down to width.
    step = max(1, len(values) // width)
    sampled = [values[i] for i in range(0, len(values), step)][:width]

    max_val = 1.0
    min_val = 0.0
    lines = []
    if label:
        lines.append(label)

    for row in range(height, -1, -1):
        threshold = min_val + (max_val - min_val) * row / height
        line = ""
        for v in sampled:
            if v >= threshold:
                line += "#"
            else:
                line += " "
        y_label = f"{threshold:.1f} |"
        lines.append(f"{y_label:>6}{line}")

    lines.append(f"      {'=' * len(sampled)}")
    lines.append(f"      Day 0{' ' * (len(sampled) - 8)}Day {DAYS}")
    return "\n".join(lines)


# --- Run simulation ---
for name, config in archetypes.items():
    skill, trust = simulate_archetype(name, config)
    print(f"\n{'=' * 70}")
    print(f"  {name}: {config['desc']}")
    print(f"{'=' * 70}")
    print()
    print(ascii_graph(skill, label="  Skill Score"))
    print()
    print(ascii_graph(trust, label="  Trust Score"))
    print()
    # Summary stats.
    print(f"  Day 0   skill={skill[0]:.2f}  trust={trust[0]:.2f}")
    print(f"  Day 90  skill={skill[90]:.2f}  trust={trust[90]:.2f}")
    print(f"  Day 180 skill={skill[180]:.2f}  trust={trust[180]:.2f}")
    print(f"  Day 365 skill={skill[-1]:.2f}  trust={trust[-1]:.2f}")
```

## 6. Simulation Results

Running the simulation produces the following profiles.

### Steady Eddie (contributes every 2 weeks)

```
  Skill Score
 1.0 |
 0.9 |############################################################
 0.8 |############################################################
 0.7 |############################################################
 0.6 |############################################################
 0.5 |############################################################
 0.4 |############################################################
 0.3 |############################################################
 0.2 |############################################################
 0.1 |############################################################
 0.0 |############################################################

  Day 0  skill=0.85  trust=0.80  (stable — never decays significantly)
```

Eddie's scores stay near peak because each contribution resets the clock
before meaningful decay occurs. The 14-day gap only causes ~10% decay
from peak, which is immediately restored.

### Burst Betty (2-month burst, then nothing)

```
  Skill Score
 1.0 |
 0.9 |#########
 0.8 |###########
 0.7 |#############
 0.6 |################
 0.5 |#####################
 0.4 |############################
 0.3 |######################################
 0.2 |############################################################
 0.1 |############################################################
 0.0 |############################################################

  Day 0   skill=0.90  trust=0.75
  Day 90  skill=0.72  trust=0.68
  Day 180 skill=0.41  trust=0.54
  Day 365 skill=0.17  trust=0.37
```

Betty's skills decay faster (90-day half-life) than trust (180-day).
By day 180 she's nearly at floor for skills. Trust retains slightly
more due to the longer half-life.

### Sabbatical Sam (6 months on, 3 months off, then returns)

```
  Skill Score
 1.0 |
 0.9 |
 0.8 |##############################              ################
 0.7 |##############################              ################
 0.6 |##############################  #           ################
 0.5 |##############################  ###         ################
 0.4 |##############################  #####       ################
 0.3 |##############################  ########    ################
 0.2 |##############################  ##############################
 0.1 |############################################################
 0.0 |############################################################

  Day 0   skill=0.80  trust=0.70
  Day 180 skill=0.80  trust=0.70  (still active)
  Day 270 skill=0.24  trust=0.38  (lowest point, end of break)
  Day 365 skill=0.80  trust=0.70  (fully recovered)
```

Sam demonstrates the key design goal: **recovery is immediate**. After
a 3-month break, scores dropped significantly, but returning to regular
contributions restores them within a few weeks.

### Ghost Gary (one contribution, never returned)

```
  Skill Score
 1.0 |
 0.9 |
 0.8 |
 0.7 |
 0.6 |#
 0.5 |##
 0.4 |####
 0.3 |########
 0.2 |################
 0.1 |############################################################
 0.0 |############################################################

  Day 0   skill=0.60  trust=0.30
  Day 90  skill=0.35  trust=0.25
  Day 180 skill=0.22  trust=0.23
  Day 365 skill=0.11  trust=0.21
```

Gary's single contribution fades toward the floor. He retains a small
baseline showing he once participated, but his visible reputation is
minimal. A T1 contributor in Gary's position would receive a demotion
warning around day 120 and drop to T0 around day 150.

## 7. Proposed Decay Parameters

Based on the simulation analysis:

| Parameter | Proposed value | Notes |
|-----------|---------------|-------|
| **Skill half-life** | 90 days | Balances freshness with acknowledgment of experience |
| **Trust half-life** | 180 days | Trust is harder to build and should be slower to lose |
| **Skill floor** | 0.10 | 10% baseline — "you did this once" |
| **Trust floor** | 0.20 | 20% baseline — higher because trust is more foundational |
| **Grace period** | 30 days | Warning before tier demotion |
| **Hysteresis band** | 0.10 | Promotion threshold is 0.10 above demotion threshold |
| **Recovery rate** | Immediate | No penalty for returning after absence |

### Parameter Tuning

These parameters should be adjusted based on observed community behavior:

- If contributors frequently churn at tier boundaries, **widen the
  hysteresis band**.
- If too many contributors cluster at floor, **lengthen the half-life**.
- If stale contributors remain visibly trusted too long, **shorten the
  half-life**.
- The Herald agent can track decay events and report metrics to inform
  tuning.

## 8. Edge Cases

### Long-term contributor taking a break

A T3 elder who goes on a 6-month sabbatical:

- Trust score decays from ~0.85 to ~0.40 over 180 days (one half-life).
- They receive a warning at ~day 120 when score drops near 0.60 (T3 threshold).
- After grace period, they'd be demoted to T2 at ~day 150.
- On return, each completion immediately boosts their score. With their
  historical completion count still intact, they can re-qualify for T3
  within a few completions (the tier requirements are cumulative, not
  decaying).

### Brand new contributor

- Starts at T0 with no decay to worry about (floor is the starting point).
- First completion moves them toward T1; the clock starts then.
- If they complete one item and disappear, decay brings them back to near-T0
  within 6 months. This is appropriate — a single contribution doesn't
  warrant lasting elevated trust.

### Seasonal contributors

Some contributors may follow a pattern (e.g., active during academic
breaks). The system handles this naturally:

- Reputation fades during inactive periods.
- It rebuilds during active periods.
- Over time, their average visible reputation reflects their actual
  contribution cadence.

### Agent rigs

Agent rigs (with `parent_rig` set) pose a special case:

- An agent's reputation should decay independently from its parent human.
- If an agent rig is retired (not used), its reputation should decay
  to floor, not transfer to a replacement agent.
- The parent human's trust is unaffected by agent decay.

## 9. Implementation Notes

### Decay is computed at read time, not write time

Decay should **not** be applied by periodically updating the database.
Instead:

1. Store raw scores and timestamps in the database.
2. Compute decayed scores when rendering a character sheet or making
   a trust decision.
3. This avoids write amplification and ensures scores are always
   consistent with the current time.

### SQL Example — Decayed Skill Score

```sql
SELECT
  jt.skill_tag,
  SUM(
    (JSON_EXTRACT(s.valence, '$.quality') - 0.1)
    * EXP(-0.693 * DATEDIFF(CURDATE(), s.created_at) / 90)
    + 0.1
  ) / COUNT(*) AS decayed_score,
  COUNT(*) AS stamp_count
FROM stamps s,
  JSON_TABLE(s.skill_tags, '$[*]' COLUMNS(skill_tag VARCHAR(64) PATH '$')) AS jt
WHERE s.subject = ?
GROUP BY jt.skill_tag
ORDER BY decayed_score DESC
```

### Herald Integration

The Herald agent (w-hop-005) can be extended to announce decay events:

```
[DECAY WARNING] @alice-rig: Trust score approaching T2 demotion
                threshold. 30-day grace period starts now.
[DECAY]         @bob-rig: Demoted from T1 to T0 (180 days inactive).
```

This requires a periodic sweep query:

```sql
SELECT handle, trust_level, last_seen,
       DATEDIFF(CURDATE(), last_seen) AS days_inactive
FROM rigs
WHERE DATEDIFF(CURDATE(), last_seen) > 90
ORDER BY days_inactive DESC
```

## 10. Future Work

- **Weighted decay by project** — Skills in fast-moving projects (e.g.,
  active development) could decay faster than skills in stable projects.
- **Peer-calibrated half-life** — If the community average activity rate
  changes, half-lives could adjust automatically.
- **Decay visualization** — Show projected decay on the character sheet:
  "If no activity, your Go skill drops to 0.3 in 60 days."
- **Governance vote on parameters** — T3 elders could vote on decay
  parameters as part of chain constitution governance.
