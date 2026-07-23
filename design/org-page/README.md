# Org dashboard page — approved design (RFC #1042 phase 4, #1097)

Owner-run Claude Design export (Jul 23), rendered + verified interactive by the
fleet. This is the **1:1 implementation source** for the dashboard Org page.
Committed here so it survives the design job dir.

## Files
- `Org.dc.html` — the full page (health strip, org-chart hero, drill-down panel,
  bottom escalations | event-feed split, and the design-only demo-state switcher).
- `OrgNode.dc.html` — the org-chart node component (presence dot, badges, HUMAN
  badge on the root, hollow ephemeral/never-seen).
- `support.js` — the design's interaction/support script.
- `sample-data.md` — the design's sample data. **Predates final naming** —
  implement against live data: the CoC role is `lead` (not `fable`), `jarvis` is a
  declared-empty seat (renders never-seen), `herdres` is in the tree. Nothing
  structural changes.
- `renders/` — verification screenshots: `org-page-render.png`,
  `org-page-render2.png` (main layout + node states), `org-drilldown.png`
  (drill-down panel), `org-empty.png` (empty state).

## Implementation contract (from #1097)
- **Read-only v1** — zero action buttons. Publicly visible during dev.
- **Store-backed ONLY** (owner decision 2a) — the public web service reads the
  SQLite store (`LoadOrg`, `org_role_presence`, `org_blocked_episodes`,
  `org_recycle_overdue_episodes`, `org_role_missed_wakes`, event/wake outcomes,
  `[org:escalate]` notes via the Parse helper). It must **not** hold a herdr
  socket. Freshness ≤ ~60s is by design.
- **Reuse `runOrgOverview`** for chart+status (don't duplicate). New read-only
  `/api/org/*` endpoints in core; page in the dashboard repo (two-repo playbook,
  pipelines-page precedent).
- Sidebar: new **Org** entry in FLEET, between Agents and Chat. Breadcrumb
  `fleet › org`.

Depends on #1095 (presence pane-binding fix — presence is wrong until it lands;
the page builds against the store schema regardless). Related #1096 (feed
digests).
