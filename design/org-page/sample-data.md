# Real sample data for the Org page (use these strings in the mock — no lorem ipsum)

## The role tree (this fleet's actual planned org)
- owner (jerry) — root, human. scope: *
  - fable — coordinator-of-coordinators. scope: gitmoot/*. merge_rule: self. pane: Gitmoot
    - g2 — coordinator. scope: gitmoot/gitmoot. merge_rule: owner. pane: Gitmoot2 — WORKING
    - g3 — coordinator. scope: gitmoot/gitmoot. merge_rule: owner. pane: Gitmoot 3 — IDLE
    - g4 — coordinator. scope: gitmoot/gitmoot. merge_rule: owner. pane: Gitmoot4 — BLOCKED 14m
      - sol-impl (ephemeral implementer seats) — scope: gitmoot/gitmoot. never-seen (hollow dot)
    - vetrina — project lane. scope: jerryfane/vetrina. pane: vetrina — IDLE, overdue ⟳ 3d
    - trend-scout — project lane. scope: jerryfane/trend-scout. pane: pipeline — IDLE
    - joltra — project lane. scope: jerryfane/joltra. pane: joltra — WORKING

## Health strip numbers (realistic)
12 roles · 3 working · 1 blocked · 1 overdue · 2 escalations open · wake success 94% (24h)

## Escalations lane (real texts, shortened)
1. g4 → fable · 14m ago · wf g4/org-phase3-prep
   "Overdue-event shape: CLI-side at refusal path, or daemon-side evaluator? Reusing the
   blocked-episodes table would get my episodes reaped..."
2. vetrina → fable · 2h ago · wf vetrina/67-outreach-conversion
   "First live batch waits on Dodo merchant verification — re-confirm the send gate chain?"

## Event feed (real event shapes)
03:02  blocked_since      g4 → wake fable        ✓ delivered (nudge #2, since 02:48)
02:55  recycle_overdue    vetrina → wake fable   ✗ stalled (missed wakes: 2)
02:41  dispatch REFUSED   role 'g5' unknown      agent ask → gitmoot/gitmoot
02:37  dispatch REFUSED   vetrina out-of-scope   target gitmoot/gitmoot not in jerryfane/vetrina
02:15  recycle            g3 recycled by fable   handoff: "wave closed; fresh session for #1082"

## Drill-down panel example (role: g4)
- parent: fable · merge_rule: owner · pane: Gitmoot4 (bound by label)
- scope: gitmoot/gitmoot
- presence: blocked since 02:48 (14m) · last seen 03:02 · missed wakes: 0
- recycle: last recycled 2d ago · recycle_after: 7d (5d remaining)
- recent activity (attributed): 3 jobs today (2 succeeded, 1 running) · 14 journal notes
- open escalations: 1 (to fable, 14m)

## Freshness
All data is read from the store the daemon writes; up to ~60s stale. Header shows
"updated 32s ago" — design a subtle staleness indicator, not a spinner.
