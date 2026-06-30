# Agent heartbeat schedules

Heartbeats let the gitmoot daemon schedule **recurring agent work** itself —
cron-like background jobs — without relying on an external cron. They reuse the
normal job queue and background-agent path (no separate runner): a due heartbeat
enqueues an ordinary job that the existing worker tick runs.

Heartbeats are **off by default**. With no heartbeat sections in your config, the
daemon's scan returns immediately and nothing changes.

## Configuration

Add one `[agents.<agent>.heartbeats.<name>]` section per schedule, scoped to a
named agent:

```toml
[agents.repo-maintainer.heartbeats.daily-status]
enabled = true
repo = "jerryfane/gitmoot"
interval = "24h"
jitter = "15m"
action = "ask"
prompt = "Review open issues, PRs, and recent jobs. Return a concise status report with blockers and suggested next actions."
max_concurrent = 1
```

| Field            | Required | Default | Notes                                                        |
| ---------------- | -------- | ------- | ------------------------------------------------------------ |
| `enabled`        | no       | `false` | A disabled heartbeat never runs.                             |
| `repo`           | yes      | —       | `owner/name` the job runs against. Must be a registered, enabled daemon repo with a checkout (see note below). |
| `interval`       | yes      | —       | Go duration (e.g. `24h`, `1h30m`). Validated at load.        |
| `jitter`         | no       | `0s`    | Random `[0, jitter]` added to each `next_due` to de-thunder. |
| `action`         | no       | `ask`   | **MVP: only `ask` (read-only) is supported.**                |
| `prompt`         | yes      | —       | Instructions passed to the agent.                            |
| `max_concurrent` | no       | `1`     | Overlap cap; a new run is skipped while this many are active.|

Invalid intervals/jitter or an unsupported action produce a clear validation
error at config load.

## Behavior

- The daemon checks heartbeat schedules during its existing poll loop (both the
  registered-repo and single-repo daemons).
- When a heartbeat is due, gitmoot enqueues one normal background job. Heartbeat
  jobs are visible in the usual job/status surfaces (sender `heartbeat`,
  fingerprint `heartbeat:<agent>/<name>`).
- gitmoot records heartbeat state locally: last run time, next due time, last job
  id, and last status.
- **No duplicate jobs:** a new run is skipped while a prior heartbeat job is still
  active (the `max_concurrent` cap), and the persisted `next_due` means a daemon
  restart does not re-fire an active heartbeat.
- **Capacity-aware:** a heartbeat is skipped for this tick when the agent is
  already at its `max_background`; it retries once capacity frees up.
- **Missed ticks coalesce:** after a long outage the schedule replays only once
  (`next_due` is re-anchored to now), not a backlog of every missed interval.
- **Repo must be managed:** `repo` has to be a repo the daemon actually manages —
  registered (added to the daemon), enabled, and with a local checkout. A
  heartbeat pointing at an unmanaged/disabled repo is skipped each tick with
  `last_status = repo_unmanaged` (it does not enqueue a job no worker would claim),
  and starts running on its own once the repo becomes managed.

## Safety notes

- The default action is the conservative, read-only `ask`. Heartbeats do **not**
  auto-implement code. (review/implement heartbeats are a deferred follow-up.)
- Heartbeats respect existing agent/runtime capacity limits (`max_background`),
  runtime locks, and repo policy — they enqueue through the same path as any other
  job.
- Keep `interval` sane: every due tick consumes a background slot and runtime
  budget.

## Not yet supported (deferred)

- `action = "review" | "implement"` heartbeats.
- Agent-**type** scoping (only named agents today).
- A write-side CLI to create/edit heartbeats (edit the config file directly).
- Per-job runtime override (see #531).
