# Gitmoot Local Agent Ask Dispatch Goal

Implement the plan task by task. Each task must be developed, reviewed, opened
as its own pull request, merged, and verified before moving on, unless tasks are
explicitly safe to run in parallel.

This goal adds a real local dispatch path so Codex or Claude plugin usage can
call a registered Gitmoot agent instead of becoming a parallel skill-only
planner path. The MVP command is `gitmoot agent ask <agent> "message"`. It must
create a local Gitmoot job, run the registered runtime session through the
existing mailbox/runtime adapter path, store job/events/result locally, and
print the result. Do not implement `gitmoot agent review` or
`gitmoot agent implement` in this goal; create a GitHub issue to track them as
follow-up work.

## Core Rules

- Work one task at a time in the listed order by default.
- If tasks are independent, have disjoint file ownership, and do not depend on
  each other's results, they may be done in parallel on separate branches.
- Do not start dependent work until the prerequisite task has passed checks,
  passed `codex exec review --uncommitted`, been pushed, opened as a PR, merged,
  and verified on the target branch.
- Do not commit generated data, reports, caches, build artifacts, secrets,
  credentials, session archives, cloned helper repos, local plugin build output,
  or large outputs unless the plan explicitly says they are intended tracked
  fixtures/artifacts.
- Preserve existing behavior unless the current task explicitly changes it.
- Keep changes clean, scoped, and organized. Avoid broad rewrites.
- Avoid code duplication. When repeated logic appears, extract small reusable
  helpers that match existing repo patterns.
- If implementation depends on external APIs, docs, CLIs, data formats,
  generated scripts, installers, service launchers, subprocess calls, env vars,
  config formats, or third-party libraries, verify the real contract with local
  commands and/or official sources before editing.
- Prefer official/primary sources for technical contracts.
- Reuse Gitmoot's existing mailbox, runtime adapter, preset snapshot, job event,
  and result parsing code. Do not create a second prompt runner or duplicate the
  daemon's runtime invocation behavior.
- Local ask dispatch must not post GitHub PR comments.
- `gitmoot agent review` and `gitmoot agent implement` are out of scope except
  for the tracking issue in Task 1.

## Before Starting

1. Inspect current repo state with:
   - `git status --short`
   - current branch
   - current remote
2. If the target branch is unclear, the remote looks wrong, or the worktree has
   unrelated existing changes that make task commits ambiguous, stop and ask
   before continuing.
3. Confirm the target base branch from the current repo. If unspecified, use the
   current branch as the base.
4. Inspect relevant existing patterns before editing:
   - agent CLI behavior in `internal/cli/agent.go`
   - job CLI behavior in `internal/cli/job.go`
   - daemon worker runtime dispatch helpers in `internal/cli/daemon.go`
   - mailbox and result parsing in `internal/workflow/mailbox.go`
   - runtime adapters in `internal/runtime`
   - prompt rendering in `internal/prompts`
   - skill/plugin docs in `skills/gitmoot`
5. Verify PR tooling is available before the first PR:
   - `gh auth status`
   - repo remote resolves to the expected GitHub repository
6. Verify relevant external/runtime contracts before editing:
   - local `codex exec --help`
   - local `codex exec resume --help`
   - local `claude --help` if Claude Code behavior is touched
   - OpenAI Codex plugin/skill docs:
     `https://openai.com/academy/codex-plugins-and-skills/`
   - Claude Code CLI docs:
     `https://code.claude.com/docs/en/cli-usage`

## Per-Task Branch Workflow

1. Confirm the current task's scope.
2. Create a task branch from the latest target base branch.
3. Implement only that task.
4. Add or update focused tests/checks appropriate to the task.
5. Run focused tests for touched modules.
6. Run broader checks when the task touches shared behavior, CLI/API surfaces,
   config paths, subprocess calls, runtime delivery, docs, plugins, presets, or
   user-facing workflows.
7. For CLI, generated-package, external-tool, runtime-adapter, or plugin
   validation changes, include an operational smoke test or direct contract
   check. Syntax checks alone are not enough.
8. Identify every repository where files changed. In each changed repo, run:
   `codex exec review --uncommitted`
9. Preserve the exact raw review output per repo.

## Review-Fix Loop

1. If review finds issues, do not only patch the literal line.
2. Identify the underlying invariant/class of bug.
3. Audit nearby and sibling paths for the same issue.
4. Write a concise fix plan using:

   ```text
   Review found these issues: <<PASTE RAW REVIEW RESULTS BY REPO>>.
   For each issue, identify the underlying invariant/class of bug, audit sibling
   paths for the same issue, and plan the smallest safe fix. Verify external
   assumptions with local commands and/or official sources. Preserve repo
   patterns, avoid unnecessary refactors, and list tests/checks per repo.
   ```

5. Execute the fix plan.
6. Re-run focused tests/checks and `codex exec review --uncommitted` in every
   repo with uncommitted changes.
7. Repeat until the final raw review output contains no findings, or stop if
   blocked or if a finding is incorrect after verification.

## Commit Gate

1. Before committing, run `git diff --check` and inspect the final diff.
2. Commit only the current task's intended tracked changes.
3. Use the commit message specified by the task. If the task does not specify
   one, use a concise conventional message that describes only the current task.
4. Push the task branch.
5. Verify the task branch worktree is clean after push, except for intentionally
   ignored generated files.

## Pull Request Gate

1. Create one PR for the current task.
2. The PR title must describe only the current task.
3. The PR body must include:
   - WHAT: what was changed
   - WHY: why the task was needed
   - CHANGES: concrete implementation changes
   - RESULTS: tests/checks/review results
   - RISK: skipped checks, blockers, or residual risk
4. Include the exact raw final `codex exec review --uncommitted` output for each
   changed repo in the PR body.
5. If CI or required checks exist, wait for them and fix failures before merge.
6. Merge the PR using the repository's configured/preferred merge method. If no
   preference is discoverable, use squash merge for a clean task-level history.
7. After merge, update the local target base branch and verify the worktree is
   clean.
8. Record the PR number, PR URL, branch name, and merged commit hash.
9. Delete the task branch after merge only if the repository normally does so or
   the merge command supports safe branch deletion.

## Parallel Task Rules

- Parallelize only when tasks are independent, have disjoint write sets, and can
  be reviewed and merged without order-dependent assumptions.
- Use a separate branch per task.
- Clearly assign each branch a task number and file ownership.
- Do not duplicate work across branches.
- If parallel branches conflict after one PR merges, rebase or update the
  remaining branch on the latest target base and re-run its checks/review.
- If a task becomes dependent on another task, stop treating it as parallel and
  merge the dependency first.

## Final Response After All Tasks

- List completed tasks.
- For each task, list branch, PR URL, merge status, and merged commit hash.
- List tests/checks run.
- Include exact final raw `codex exec review --uncommitted` output for the last
  task/repo.
- Mention skipped checks, blockers, or residual risk.
- Do not claim interactive `/review` is clean. Say:
  `codex exec review is clean; ready for manual /review.`

## Implementation Tasks

### Task 1: Create Tracking Issue For Local Review And Implement

Create a GitHub issue to track local `review` and `implement` dispatch after
the `ask` MVP.

Details:
- Create one GitHub issue in `jerryfane/gitmoot`.
- Title:
  `Add local agent review and implement dispatch`
- Body:

  ```md
  Gitmoot now needs local dispatch beyond `agent ask`.

  Follow-up commands:
  - `gitmoot agent review <agent> ...`
  - `gitmoot agent implement <agent> ...`

  These should reuse the same real registered-agent runtime path as
  `agent ask`, but must preserve PR, branch, head SHA, task, branch-lock, and
  merge-gate semantics. Do not treat them as simple free-form local asks.
  ```

- Do not implement these commands in this task.
- Record the issue number and URL in the PR body.

Acceptance:
- The issue exists and is linked in this task's PR.
- No code behavior changes are made in this task unless needed to add a small
  docs note pointing to the issue.

Suggested commit message:
- `docs: track local review and implement dispatch`

### Task 2: Add `gitmoot agent ask`

Add the MVP local dispatch command:

```sh
gitmoot agent ask <agent> "message" [--repo owner/repo] [--home path] [--json]
```

Details:
- Extend `gitmoot agent` routing and usage output with `ask`.
- Parse exactly one agent name and one message argument.
- Support `--home path` and use it for all local Gitmoot state reads/writes,
  matching existing command behavior.
- Support `--repo owner/repo` to select the target repo.
- If `--repo` is omitted, infer the repo from the current checkout origin using
  existing git remote parsing helpers.
- Ensure the repo is registered in local Gitmoot state. If the repo is missing,
  register the current checkout path for that repo using existing repo record
  helpers.
- Load the named agent from local state.
- Verify the agent is allowed on the target repo.
- Verify the agent has `ask` capability.
- Create a local `ask` job with:
  - `Agent`: selected agent
  - `Action`: `ask`
  - `Repo`: target repo
  - `Branch`: current branch when available
  - `Sender`: `local`
  - `Instructions`: message
  - `PullRequest`: `0`
- Snapshot the agent preset through existing mailbox behavior.
- Run the job synchronously through the registered runtime adapter and existing
  mailbox/result parsing path.
- Print text output by default:
  - job id
  - final job state
  - decision
  - summary
  - findings, needs, tests, and next agents when non-empty
- Print JSON output with `--json`:
  - job id
  - state
  - repo
  - agent
  - action
  - result object
  - raw output count
- Do not dump raw runtime output by default.
- Do not post PR comments for local ask jobs.
- Add focused tests in this task for:
  - required agent and message validation
  - `--home` selecting an isolated Gitmoot state directory for agent, repo, job,
    and result storage
  - successful `agent ask` dispatch through a fake runtime adapter
  - parsed `gitmoot_result` storage
  - text output rendering for the local result
  - `--json` output rendering without raw runtime output

Acceptance:
- `gitmoot agent ask planner "..." --repo owner/repo` runs a registered
  `planner` agent and stores a local job.
- `gitmoot job list --repo owner/repo` shows the completed ask job.
- `gitmoot job show <job-id>` shows preset id and result details.
- Missing agent, missing message, missing ask capability, and disallowed repo
  produce clear errors.
- `--home` is honored for isolated local state and covered by tests.
- Focused CLI tests prove the new command, runtime delivery path, result
  parsing, and output formats before this task is merged.

Suggested commit message:
- `feat: add local agent ask dispatch`

### Task 3: Extract Shared Dispatch Helpers

Keep the implementation organized by extracting helper functions instead of
duplicating daemon worker logic.

Details:
- Add small reusable helpers for:
  - resolving repo and checkout from `--repo` or the current checkout
  - ensuring/recording repo registration for local dispatch
  - checking agent repo access and capability
  - creating local ask job IDs
  - selecting a runtime adapter for a checkout
  - rendering local job results in text and JSON
- Reuse existing code where possible:
  - `runtimeAgent`
  - `repoRecordFromPath` / `repoRecordForCheckout`
  - `workflow.Mailbox`
  - `workflow.JobPayload`
  - runtime adapters
- Do not duplicate prompt rendering, preset snapshotting, result extraction, or
  runtime resume code.
- Keep helpers scoped to CLI packages unless a helper is clearly useful to
  workflow/runtime packages.
- Move any helper-level test assertions introduced in Task 2 only if the helper
  extraction requires it; do not leave the refactor without behavioral coverage.

Acceptance:
- Local ask and daemon dispatch share the mailbox/runtime path.
- New helpers are covered by focused tests or exercised through `agent ask`
  tests.
- Existing daemon PR-comment behavior is unchanged.

Suggested commit message:
- `refactor: share local agent dispatch helpers`

### Task 4: Update Plugin And Skill Docs

Remove the confusing parallel planner path from the plugin/skill docs and route
plugin usage through the real registered Gitmoot agent.

Details:
- Update `skills/gitmoot/SKILL.md` to describe local agent ask dispatch.
- Update `skills/gitmoot/references/CLI.md` with:
  - `gitmoot agent ask <agent> "message" [--repo owner/repo]`
  - planner examples using `gitmoot agent ask planner ...`
- Update `skills/gitmoot/references/WORKFLOWS.md` so plugin usage says:

  ```text
  $gitmoot:gitmoot Ask the planner agent to create a plan and goal file for this feature: ...
  ```

  and the skill-guided action is:

  ```sh
  gitmoot agent ask planner "Create a plan and goal file for..."
  ```

- Make the distinction explicit:
  - plugin path: current chat uses Gitmoot CLI to call a real registered agent
  - PR path: daemon consumes `/gitmoot planner ask ...`
  - both paths use the same registered agent, preset, runtime session, mailbox,
    and result contract
- Update README and beta smoke docs with a local ask smoke path.

Acceptance:
- Docs no longer imply `$gitmoot:gitmoot ...` itself is the planner agent.
- Docs show local plugin usage as a convenience path over `gitmoot agent ask`.
- Generated plugin package includes the updated skill docs.

Suggested commit message:
- `docs: route plugin planner workflow through agent ask`

### Task 5: Add Tests And Smoke Coverage

Add final smoke and package coverage for local ask dispatch and plugin workflow
docs. Core command/runtime tests must already exist from Tasks 2 and 3; do not
use this task to backfill coverage that was required before those PRs merged.

Required tests:
- Skill/plugin packaging tests include the updated local ask docs.
- Smoke documentation covers planner preset setup, `gitmoot agent ask`, job
  listing, and job inspection.
- If gaps are discovered in Task 2/3 tests, fix them in the relevant code area
  before merging Task 5.

Smoke test to document and run where feasible:

```sh
gitmoot preset update gitmoot-plan-and-goal
gitmoot agent start planner \
  --runtime codex \
  --repo owner/repo \
  --path . \
  --preset gitmoot-plan-and-goal
gitmoot agent ask planner "Write a task-by-task plan and goal file for this feature."
gitmoot job list --repo owner/repo
gitmoot job show <job-id>
```

Checks:
- focused tests for touched CLI/workflow/runtime packages
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- plugin package build smoke
- `codex exec review --uncommitted`

Suggested commit message:
- `test: cover local agent ask dispatch`
