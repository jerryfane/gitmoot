# Agents, Presets, Jobs, And Locks

Gitmoot uses runtime-neutral agent records. A named agent has a runtime, runtime
reference, repo access, role, capabilities, and optional preset.

## Agents

Agents can be started by Gitmoot or subscribed from an existing runtime session.

```sh
gitmoot agent start planner --runtime codex --repo owner/repo --preset gitmoot-plan-and-goal
gitmoot agent subscribe reviewer --runtime codex --session <session-id> --repo owner/repo --capability review
```

## Presets

Presets are reusable prompt/profile bundles. Gitmoot snapshots preset content
into each job so the job has reproducible instructions.

```sh
gitmoot preset update gitmoot-plan-and-goal
gitmoot preset update thermo-nuclear-code-quality-review
gitmoot preset add frontend-reviewer --file agents/frontend-reviewer.md
```

## Jobs

Jobs are units of routed work. They can come from PR comments, local
`agent ask`, task runs, retries, or merge actions.

## Locks

Gitmoot uses separate locks for separate resources:

- Repo checkout locks keep daemon ticks from using the same local checkout at
  the same time.
- Runtime session locks serialize Codex or Claude delivery for the same
  `runtime:<runtime>:<runtime_ref>` across daemon jobs, `job run`, and
  synchronous `agent ask`.
- Branch locks prevent multiple agents from racing on the same implementation
  branch.

Review and ask jobs usually inspect and report. Implementation jobs should only
mutate branches when Gitmoot assigned the job and the branch lock is held.

The daemon defaults to `--workers 1`. Raise workers only when jobs use
independent runtime sessions or managed agent types with `max_background`
greater than one.
