# Coordinator Recipes Workflow

Coordinator recipes are built-in agent templates that turn the
[Orchestra pattern](../reference/result-contract.md#delegations-orchestration)
into one-command workflows. Each recipe is a coordinator prompt that emits a
`delegations[]` of **ephemeral** workers — created on demand from an inline spec,
run once, then auto-disposed — so you do not pre-register any agents. Gitmoot runs
the panelists or legs in the daemon, then reconvenes their results in a single
continuation back to the coordinator.

Start a recipe with `gitmoot orchestrate <recipe-id> "..." --repo owner/repo`,
which is sugar for a background `gitmoot agent run`:

```sh
gitmoot orchestrate review-panel "Review PR #123 in this repo." --repo owner/repo
gitmoot orchestrate decompose-and-verify "Implement the export feature described in the task." --repo owner/repo
```

Two recipes ship built in. Install or refresh either template the same way as any
built-in template:

```sh
gitmoot agent template update review-panel
gitmoot agent template update decompose-and-verify
gitmoot agent template show review-panel
```

## Review Panel

`review-panel` reviews a pull request or change by convening a panel of
independent reviewers, each looking through a different lens, then synthesizing
their findings into one verdict. It fans out three dep-free reviewers by default —
correctness and security; performance and maintainability; tests and edge cases —
so they review in parallel. Each panelist is an ephemeral worker with a
self-contained lens prompt, and the recipe mixes runtimes so the panel does not
share one model's blind spots (point a panelist at an installed review template
such as `thermo-nuclear-code-quality-review` only if you want).

```sh
gitmoot orchestrate review-panel "Review PR #123 in this repo." --repo owner/repo
```

Once every panelist is terminal, Gitmoot enqueues one continuation that
de-duplicates the findings, decides the verdict (`changes_requested` if any
reviewer raised a blocking issue, else `approved`), and reports which lenses drove
it.

## Decompose and Verify

`decompose-and-verify` takes one implementation task, splits it into independent
file-disjoint subtasks, and fans them out to ephemeral implementation workers that
build in parallel in their own branch worktrees. It then adds one `review`-action
verify step whose `deps` list every implementation leg, forming a small DAG, so
the gate runs only after all the legs finish.

```sh
gitmoot orchestrate decompose-and-verify "Implement the export feature described in the task." --repo owner/repo
```

After verify finishes, Gitmoot enqueues one continuation. The coordinator reads
the verify result first — it is the gate — and either reports the merged changes
when verify passed, or summarizes what failed and which leg owns the fix.

## Coordinator-owned review

By default an `implement` job that opens a pull request fans the PR out to
Gitmoot's native reviewers — the configured required reviewers, or the ones
passed for the task — so each reviewer runs as its own review job before the
merge gate. When a coordinator already plans review itself (for example a
`review-panel` leg, or a custom continuation that reconvenes its own reviewers),
that native fan-out duplicates work. Pass `--skip-native-review-fanout` on
`gitmoot orchestrate`, `gitmoot agent run`, or `gitmoot agent implement` to hand
review orchestration to the coordinator:

```sh
gitmoot agent implement lead --repo owner/repo --task task-001 --skip-native-review-fanout "Implement this task."
gitmoot orchestrate decompose-and-verify "Implement the export feature described in the task." --repo owner/repo --skip-native-review-fanout
```

With the flag set, the implement→PR step still records the PR baseline, runs the
merge gate, and records the `implemented` decision — it simply enqueues **no**
native review jobs. The skip is honored on both PR-open paths: the engine's
implement-advance and the daemon's GitHub PR-watcher, so a PR opened either way
stays free of native review fan-out. The flag defaults off; leaving it off keeps
the full native review fan-out, byte-identical to prior behavior.

## Ephemeral, leaf-only, bounded

In both recipes the delegations never set `agent`: `agent` and `ephemeral` are
mutually exclusive, and every panelist or leg here is ephemeral. Ephemeral workers
are **leaf-only** — they return findings, never their own delegations — so a
recipe's fan-out is exactly one level deep. The recipes run inside the same
delegation [termination bounds](../reference/result-contract.md#termination-bounds)
as any orchestra: a depth cap, a per-root job budget, a per-root wall-clock
budget, a per-coordinator width cap, and loop detection — all ending in one
graceful finalize continuation.

Inspect a run with the usual job and event commands:

```sh
gitmoot job list --repo owner/repo
gitmoot events --repo owner/repo
```

See the [Result Contract](../reference/result-contract.md) for the `ephemeral`
field reference and the
[registered agent vs. ephemeral worker](../concepts/agents-templates-jobs-locks.md#choosing-a-worker-registered-agent-vs-ephemeral-worker)
comparison.
