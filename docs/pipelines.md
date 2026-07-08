# Pipelines

Pipelines (#681) let the gitmoot daemon run a **declared DAG of shell stages** —
a small, durable multi-step flow — on demand or on an interval schedule. Each
stage is an ordinary queued job run through the **shell runtime**: the normal
worker tick claims and runs it, and a scan-based **advancer** folds each stage's
`gitmoot_result` decision and enqueues the stages whose dependencies have all
succeeded. Pipelines reuse the same job queue, result contract, and scheduling
idiom as heartbeats — there is no separate runner.

Pipelines are **off by default**: with no pipelines defined, the daemon's
pipeline scan returns an empty list before touching any state, and behavior is
unchanged.

A pipeline is not an orchestra. Each stage is a **leaf**: it runs a shell command
and returns a decision. A stage that emits `delegations[]` does **not** spawn
children — the advancer ignores them and the engine strips them for a pipeline
stage job, so a pipeline can never fan out into a delegation tree. Reach for an
orchestra (`gitmoot orchestrate` / a coordinator that returns `delegations[]`)
when you want dynamic decomposition; reach for a pipeline when you have a fixed,
repeatable sequence of shell steps with explicit dependencies.

## The spec

A pipeline is declared as a YAML file and registered with `gitmoot pipeline add`.
The raw bytes are stored **verbatim** in the local store alongside a content hash;
each run snapshots the hash it was created from, so a run always executes the spec
it was created against — editing the file later (even whitespace) changes the hash
and does not affect an in-flight run.

```yaml
name: nightly-sync          # required, name-safe token (letters, digits, - _)
repo: owner/repo            # optional to register; REQUIRED to actually run
schedule:                   # optional; interval schedule (no cron in v1)
  interval: 24h             #   required when a schedule block is present (positive Go duration)
  jitter: 15m               #   optional random [0, jitter] added to each next_due (>= 0)
success_decisions:          # optional top-level default (see below)
  - approved
  - implemented
stages:                     # the DAG, keyed by unique id and wired by needs
  - id: source
    cmd: "curl -sf https://example.com/data > data.json"
  - id: score
    cmd: "python score.py data.json"
    needs: [source]         # runs only after every listed stage SUCCEEDS
  - id: deploy
    cmd: "rclone copy out/ r2:bucket"
    needs: [score]
    timeout: 30m            # optional per-stage job timeout (positive Go duration)
    retry: 2                # optional; re-attempt a FAILED stage up to N times (>= 0)
    success_decisions:      # optional per-stage override of the pipeline default
      - approved
```

### Fields

| Field                       | Scope        | Required | Notes |
| --------------------------- | ------------ | -------- | ----- |
| `name`                      | pipeline     | yes      | Stable identifier and DB primary key; a name-safe token (letters, digits, `-`, `_`). |
| `repo`                      | pipeline     | no\*     | `owner/name` the stages run against. Optional to **register**, but **required to run** — stage jobs need a managed repo for the worker to claim them. |
| `schedule.interval`         | pipeline     | cond.    | Required when a `schedule:` block is present. A positive Go duration (`24h`, `1h30m`). |
| `schedule.jitter`           | pipeline     | no       | Random `[0, jitter]` added to each `next_due` to de-thunder (`>= 0`). |
| `success_decisions`         | pipeline     | no       | Decisions that mark a stage succeeded. Default `["approved","implemented"]`. Any value must be one of `approved`, `implemented`, `changes_requested` — `blocked`/`failed` are park states and are rejected. |
| `stages[].id`               | stage        | yes      | Unique, name-safe stage id. Appears verbatim in the stage job's fingerprint and deterministic id. |
| `stages[].cmd`              | stage        | cond.    | Shell command run verbatim via `sh -c` (see the stage contract below). **Exactly one** of `cmd` or `agent` per stage. |
| `stages[].agent`            | stage        | cond.    | Name of a managed gitmoot agent to run this stage as a read-only leaf (#757), instead of a shell `cmd`. Must be a name-safe token; `pipeline add` warns (does not block) if the agent does not exist yet, but it must exist before the stage runs. Mutually exclusive with `cmd`. See [Agent stages](#agent-stages). |
| `stages[].prompt`           | stage        | cond.    | Instruction handed to an agent stage's agent. **Required** for an agent stage; rejected for a shell stage. Prepended with the upstream `needs` stages' result summaries at enqueue. |
| `stages[].action`           | stage        | no       | Read-only verb for an agent stage: `ask` (default) or `review`. `implement` is rejected — an agent stage is a read-only leaf. Rejected for a shell stage. |
| `stages[].needs`            | stage        | no       | Ids of sibling stages that must **succeed** before this stage is enqueued. Must reference known stages, never the stage itself, and form no cycle. |
| `stages[].timeout`          | stage        | no       | Per-stage job timeout (positive Go duration). |
| `stages[].retry`            | stage        | no       | How many times a **failed** stage may be re-attempted (`>= 0`, default `0`). |
| `stages[].success_decisions`| stage        | no       | Per-stage override of the pipeline default. |

`gitmoot pipeline add` validates the whole spec **at add time** — unknown keys, a
non-name-safe name/id, a duplicate stage id, a stage that is not **exactly one** of
`cmd` or `agent` (and, for an agent stage, a missing `prompt`, a non-existent agent,
or a non-read-only `action`), an unknown/self/cyclic `needs`, an invalid
timeout/interval/jitter, a negative retry, or a `success_decisions` value outside the
allowed set — so a structural mistake surfaces as a clear error at registration
rather than a stuck run later.

### Agent stages

A stage may run a **named managed gitmoot agent** as a read-only leaf instead of a
shell command (#757). Set `agent` + `prompt` (and optionally `action`) in place of
`cmd`:

```yaml
stages:
  - id: extract
    cmd: "python fetch_replies.py > replies.json"
  - id: triage
    agent: reply-triager      # managed agent — create it before the pipeline runs
    action: ask               # ask (default) | review — read-only ONLY
    prompt: "Triage the fetched replies; block if anything needs a human."
    needs: [extract]
```

- **Runs on the agent's own runtime.** Unlike a shell stage (which runs through the
  hidden per-pipeline shell runner via a per-job runtime override), an agent stage
  binds the stage job to the named agent and runs it on **its own** registered runtime
  (claude / codex) and session — no runtime override.
- **Read-only leaf.** `action` is `ask` or `review` only; `implement` is rejected. The
  stage is a leaf: its `delegations[]` are stripped (Sender is the pipeline sender),
  so an agent stage can never fan out.
- **Agent existence is warned at add time.** `pipeline add` checks every referenced
  agent and prints a warning for any that does not exist yet, but does **not** block —
  a spec may legitimately be added before its agents are provisioned (bundled or
  shareable pipelines, scripted setup). A genuinely-missing agent still fails loudly
  when the stage runs, so create it before the pipeline runs (`gitmoot agent …`).
- **Upstream needs-context injection.** At enqueue the stage prompt is **prepended**
  with a bounded `Upstream stage results` block — one labeled entry per `needs` stage
  carrying that stage's fold state and result summary — so a downstream agent stage
  acts on upstream output as real dataflow. Each summary is **fenced** (a backtick
  fence sized longer than any run inside it) so an upstream summary can never spoof the
  block structure or inject instructions into the downstream agent. The block is
  size-bounded and each summary is truncated with a `[truncated]` marker when oversized;
  a root agent stage (no `needs`) receives the bare prompt.
- **Read-only worktree isolation.** A repo-bound ask/review agent stage is born with
  its own detached, committed-tip **read-only worktree** (#739), so it keys
  `worktree:<path>` rather than the shared `repo:<repo>` live checkout — same-repo
  agent stages then run **concurrently** and never mutate the live checkout. The
  worktree is disposed when the stage job settles (and reclaimed on daemon restart).
  Allocation is fail-open: if it cannot be created the stage still runs, serialized on
  the shared checkout, with a loud skip event. A pure-reasoning agent stage with no
  `repo` needs no worktree.
- **Stateless per run.** Because each agent stage runs in a freshly-created worktree
  directory, a runtime that scopes sessions by working directory (e.g. Claude Code)
  starts a **fresh session each run** — a pipeline stage carries no session context
  across runs. This is intended: a pipeline run is deterministic and independent, so
  supply everything a stage needs via its `prompt` and the injected upstream context,
  not via accumulated agent memory.

Agent stages fold by `decision` and advance/park exactly like shell stages.

### The stage contract

A stage command runs under the shell runtime as `sh -c '<cmd>' gitmoot '<prompt>'`
(so `$0` is `gitmoot` and `$1` is the stage's job prompt). The stage signals its
outcome by printing a `gitmoot_result` JSON blob to **stdout**; the advancer folds
the stage by that result's **`decision`**, never by the job's exit state:

```sh
# A stage that succeeds:
printf '%s' '{"gitmoot_result":{"decision":"approved","summary":"synced"}}'

# A stage that parks the run awaiting a human, listing what it needs:
printf '%s' '{"gitmoot_result":{"decision":"blocked","summary":"secret missing","needs":["R2 token"]}}'
```

The decision drives advancement:

- **`decision` in the stage's `success_decisions`** → the stage **succeeded**;
  stages whose `needs` have all now succeeded are enqueued.
- **`decision: blocked`** → the stage is **blocked**; its `needs` are persisted at
  the stage and run level and the run **parks blocked**. Downstream stages are
  never enqueued.
- **`decision: failed`, any decision outside the stage's `success_decisions`
  (`changes_requested` by default), a cancelled job, or no `gitmoot_result` at all**
  → the stage **failed**. If the stage has retry budget left it is re-attempted;
  otherwise the run **parks failed**.

`changes_requested` is a stage **failure by default** — even though the underlying
job succeeded — because a stage folds on the decision, not the job state, and a
review that asked for changes is not "this step landed." List it in a stage's (or
the pipeline's) `success_decisions` to treat it as success instead.

## CLI

```sh
# Register (validate + store). Omit --enable to add it disabled (no scheduling).
gitmoot pipeline add nightly-sync.yaml --enable

gitmoot pipeline list [--json]
gitmoot pipeline show <name> [--json]        # registry view for a pipeline name
gitmoot pipeline show <run-id> [--json]      # run funnel for a "prun-…" run id

gitmoot pipeline run <name>                  # start a manual run; prints the run id
gitmoot pipeline resume <run-id> [--from <stage>]
gitmoot pipeline cancel <run-id>

gitmoot pipeline enable <name>
gitmoot pipeline disable <name>
gitmoot pipeline remove <name>
```

`pipeline run` prints just the run id (script-stable), so `RUN=$(gitmoot pipeline
run nightly-sync)` works. A manual run ignores the `enabled` flag — a disabled
pipeline can still be run by hand — but still requires a `repo` and refuses to
start while the pipeline already has an active run (one active run per pipeline).

`pipeline show <run-id>` renders the run as a **text funnel**:

```
run: prun-nightly-sync-18bfa02e9afb86ed
pipeline: nightly-sync
trigger: manual
state: blocked
started: 2026-07-06T06:41:39Z
finished: 2026-07-06T06:42:10Z
halt_stage: score
halt_reason: secret missing
needs: R2 token

source OK -> score BLOCKED (needs: R2 token) -> deploy SKIPPED
```

Funnel labels are `OK` for a succeeded stage, `BLOCKED (needs: …)` for a parked
stage, and the uppercased state otherwise (`PENDING`, `QUEUED`, `RUNNING`,
`FAILED`, `SKIPPED`, `CANCELLED`). When a run **failed**, the view also prints the
exact command to file a bug for the halted stage's job — gitmoot never files it for
you:

```
stage failed; report it with:
  gitmoot report bug --job <stage-job-id>
```

`--json` on `list` and `show` emits a stable machine shape (pipelines as an array;
a run as `{id, pipeline, trigger, state, halt_stage, halt_reason, needs, spec_hash,
started_at, finished_at, funnel, stages[]}`).

### Resume

`pipeline resume <run-id>` re-runs a **parked** (blocked or failed) run from its
halted stage; `--from <stage>` overrides the resume point. Resume resets the halted
stage (or `--from` stage) and every stage that transitively **depends** on it back
to pending — bumping each reset stage's attempt so the next scan enqueues a fresh
stage job — clears the run's park fields, and returns the run to `running`. It
**never re-runs a stage that already succeeded**, even one downstream of the resume
point, and it refuses a run that is not parked. Because a run executes its spec
snapshot, resume is refused if the pipeline's spec changed since the run was
created.

The intended story: a stage blocks on something the operator must provide (a
missing secret, an unapproved change), the operator provisions it out of band, then
`pipeline resume` re-runs the halted stage and everything downstream — while the
already-landed upstream stages are left untouched. Approval gates that call resume
automatically are a follow-up (#682); v1 is the manual verb.

### Cancel and remove

`pipeline cancel <run-id>` abandons a running or parked run: it cancels each
in-flight stage job through the shared `job cancel` path (which also best-effort
releases the job's locks) and marks the run and its non-terminal stages
`cancelled`. An already-settled stage keeps its recorded outcome, so a cancelled
run still shows why it halted. It refuses an already-terminal (succeeded/cancelled)
run.

`pipeline remove <name>` deletes the pipeline and disposes its hidden shell runner
agent (below).

## Scheduling

A pipeline with a `schedule.interval` and `enable`d auto-runs on that interval,
using the same durable-`next_due` idiom as heartbeats. The daemon's pipeline scan
runs in **both** the registered-repo and single-repo daemon loops and does two
passes per tick:

1. **Schedule pass** — for each enabled pipeline whose interval is due and that has
   no active run, create a fresh scheduled run and advance `next_due` **anchored to
   now**.
2. **Advance pass** — advance every in-flight run once (fold settled stage jobs,
   enqueue newly-ready stages, park or finish).

A parked or terminal run is never advanced, so a blocked/failed run consumes **zero
compute** while it waits. Behavior worth knowing:

- **Interval + jitter only** — there is no cron parser in v1; because `next_due` is
  durable, a cron front-end is a documented drop-in for a later release.
- **One active run per pipeline** — a scheduled tick that finds a run in flight is
  skipped without advancing `next_due`, so the next run fires as soon as the current
  one settles. A parked run does **not** count as active.
- **Missed ticks coalesce** — a long-idle scheduler that missed many intervals fires
  exactly **one** run and schedules the next one interval out; it never replays a
  backlog.
- **Restart-safe** — the advancer recovers purely from the persisted run/stage rows,
  so a daemon restart mid-run picks the run back up and completes it.
- **Repo required to run** — a scheduled pipeline with no `repo` is skipped and its
  `next_due` advanced (so a misconfigured schedule does not hot-loop and
  self-recovers once a repo is set), mirroring the heartbeat repo-unmanaged idiom.

## The hidden runner agent

`pipeline add` auto-creates one hidden shell agent per pipeline, named
`pipeline-<name>-runner`, that owns the pipeline's stage jobs (the worker loop
resolves a job's agent record). The stage command travels **per job** (in the stage
job's runtime-override ref), not on the agent's runtime ref, so one runner serves
every stage. These runner agents are an implementation detail and are **filtered out
of `gitmoot agent list`**; `pipeline remove` disposes them.

## Observability

- `gitmoot pipeline list` shows each pipeline's enabled state, interval, repo, and
  last run status.
- `gitmoot pipeline show <name>` shows the registry view (spec hash, schedule,
  last/next run bookkeeping, and the stage DAG).
- `gitmoot pipeline show <run-id>` shows the run funnel.
- Stage jobs are ordinary jobs (sender `pipeline`), so they also appear in the usual
  job/status surfaces.

### Web dashboard

The web dashboard (`gitmoot dashboard --web`) has a dedicated, read-only
**Pipelines** view: a list of every declared pipeline with its schedule state,
next-due countdown, and recent-run outcomes, and a per-run detail that renders the
stage DAG in spec (topological) order with the same halt/needs information the
`pipeline show <run-id>` funnel prints. It surfaces the resume / bug-report
commands for a parked run as copyable text but never mutates anything. See
[Dashboard Views → Pipelines](https://gitmoot.io/docs/dashboard/views#pipelines).

## Safety

`pipeline add` is an **operator-trust action**: a stage's `cmd` runs verbatim via
`sh -c` with the daemon's permissions. Only register specs you would run yourself —
the same trust you extend to a heartbeat prompt or a CI step. The spec is stored
verbatim (raw bytes), so treat a spec containing private hostnames, paths, or repo
names the same as any other private-repo data. See `SAFETY.md → Pipeline stages run
with daemon permissions`.

## Not yet supported (deferred)

These are intentionally out of scope for v1 and tracked as follow-ups:

- A cron schedule front-end (interval + jitter only today).
- Approval gates / secret stores / an approval UI for a blocked stage (#682) — v1
  ships the manual `pipeline resume` seam.
- Auto-filing a bug for a failed stage (`show` prints the command; you run it).
- LLM stages, upstream stage output flowing into a downstream stage's prompt, and
  per-stage env/workdir.
- Matrix / dynamic stages, more than one concurrent run per pipeline, pipelines
  defined from the dashboard, a web funnel view, and a foreground `--watch`.
</content>
