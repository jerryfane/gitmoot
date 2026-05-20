# Gitmoot Implementation Goal

Implement Gitmoot task by task. Each task must be developed, reviewed, opened
as its own pull request, merged, and verified before moving on, unless tasks are
explicitly safe to run in parallel.

Gitmoot is a local-first Go binary that coordinates persistent AI agent
sessions through GitHub pull requests. The daemon watches PR comments and PR
state, routes jobs to registered agents through runtime adapters, resumes the
correct Codex or Claude session, posts structured PR comments, advances task
state autonomously, and merges only when policy gates pass.

## Core Rules

- Work one task at a time in the listed order by default.
- If tasks are independent, have disjoint file ownership, and do not depend on
  each other's results, they may be done in parallel on separate branches.
- Do not start dependent work until the prerequisite task has passed checks,
  passed `codex exec review --uncommitted`, been pushed, opened as a PR, merged,
  and verified on the target branch.
- Do not commit generated data, reports, caches, build artifacts, secrets,
  credentials, or large outputs unless the plan explicitly says they are
  intended tracked fixtures/artifacts.
- Preserve existing behavior unless the current task explicitly changes it.
- Keep changes clean, scoped, and organized. Avoid broad rewrites.
- Avoid code duplication. When repeated logic appears, extract small reusable
  helpers that match existing repo patterns.
- If implementation depends on external APIs, docs, CLIs, data formats,
  generated scripts, installers, service launchers, subprocess calls, env vars,
  config formats, or third-party libraries, verify the real contract with local
  commands and/or official sources before editing.
- GitHub PR comments are the public audit trail. Local SQLite state is the
  workflow source of truth.
- Runtime-specific behavior must live behind adapters. Do not hardcode Codex or
  Claude assumptions into workflow, GitHub, or daemon packages.
- V1 is local-only. Do not add a hosted service, GitHub App, billing, remote
  dashboard, or cloud runner unless a later task explicitly changes scope.

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
4. Inspect relevant existing patterns before editing.
5. Verify PR tooling is available before the first PR:
   - `gh auth status`
   - repo remote resolves to the expected GitHub repository
6. Verify local runtime tooling before runtime-adapter tasks:
   - `codex exec resume --help`
   - `claude --help`, if implementing or testing the Claude adapter
7. Verify GitHub API behavior with official docs or `gh api` before implementing
   GitHub mutation paths.

## Per-Task Branch Workflow

1. Confirm the current task's scope.
2. Create a task branch from the latest target base branch.
3. Implement only that task.
4. Add or update focused tests/checks appropriate to the task.
5. Run focused tests for touched modules.
6. Run broader checks when the task touches shared behavior, CLI/API surfaces,
   data/model/evaluation logic, generated scripts, installers, service
   launchers, docs build systems, or user-facing workflows.
7. For wrapper, installer, CLI, subprocess, generated-script, env propagation,
   service-launcher, GitHub API, daemon, or runtime-adapter changes, include an
   operational smoke test or direct contract check. Syntax checks alone are not
   enough.
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

## Product Architecture

Build one Go binary named `gitmoot`.

Gitmoot V1 runs on a single user machine:

```text
GitHub PR comments/state
  -> local gitmoot daemon
  -> local SQLite state machine and job mailbox
  -> registered runtime adapter
  -> Codex, Claude Code, or future agent runtime
  -> GitHub PR comments, statuses, branches, PRs, and merges
```

The core primitive is not a Codex session. The core primitive is a
runtime-neutral Gitmoot agent:

```text
agent name
role
runtime: codex | claude | shell
runtime_ref: session id, session name, or command target
repo scope
capabilities
autonomy policy
health status
```

Use GitHub commit statuses for V1 status reporting. Do not implement the
GitHub Checks API in V1 because checks write access is GitHub-App-oriented.

## Key Interfaces To Implement

- CLI commands:
  - `gitmoot init`
  - `gitmoot doctor`
  - `gitmoot daemon start`
  - `gitmoot agent subscribe <name> --runtime codex|claude|shell --session <id|name|last> --role <role> --repo owner/repo --capability <capability>`
  - `gitmoot agent list`
  - `gitmoot agent remove <name>`
  - `gitmoot agent doctor <name>`
  - `gitmoot status`
  - `gitmoot goal import --file <path>`
  - `gitmoot task run <id>`
- Runtime adapter behavior:
  - `Validate(agent)`
  - `Deliver(job)`
  - `Health(agent)`
  - `Capabilities()`
- GitHub PR command syntax:
  - `/gitmoot <agent> review [instructions]`
  - `/gitmoot <agent> implement [instructions]`
  - `/gitmoot ask <agent> [question]`
  - `/gitmoot status`
  - `/gitmoot merge`
- Required result contract:

  ```json
  {
    "gitmoot_result": {
      "decision": "approved|changes_requested|blocked|implemented|failed",
      "summary": "...",
      "findings": [],
      "changes_made": [],
      "tests_run": [],
      "needs": [],
      "next_agents": []
    }
  }
  ```

If an agent does not return a valid `gitmoot_result` block, store the raw output
and retry once with a repair prompt.

## Implementation Tasks

### Task 1: Bootstrap Go Project

Create the initial Go project structure for a clean, testable CLI.

Requirements:
- Initialize the Go module for `gitmoot`.
- Create one binary entrypoint.
- Add an organized internal package layout for config, DB, GitHub, Git,
  workflow, runtime adapters, daemon, prompts, logging, and CLI commands.
- Add shared helpers for subprocess execution, JSON parsing, path handling, repo
  identity, and PR URL parsing.
- Add baseline tests that prove the package structure compiles.
- Add a README describing the local-only V1 architecture and expected workflow.

Suggested commit message:
`chore: bootstrap gitmoot go project`

Required checks:
- `go test ./...`
- `go vet ./...`
- `gofmt` check
- `git diff --check`
- `codex exec review --uncommitted`

### Task 2: Config, Doctor, And Local Store

Implement local initialization, health checks, and SQLite persistence.

Requirements:
- Implement `gitmoot init`.
- Implement `gitmoot doctor`.
- Create `~/.gitmoot/config.toml`, `~/.gitmoot/gitmoot.db`,
  `~/.gitmoot/logs/`, and `~/.gitmoot/workspaces/` as needed.
- Add embedded, idempotent SQLite migrations.
- Add repository methods for agents, repos, goals, tasks, PRs, comments, jobs,
  job events, locks, and merge gates.
- `doctor` must verify `git`, `gh auth status`, `codex`, optional `claude`,
  current repo remote, GitHub repository identity, and base branch.

Suggested commit message:
`feat: add config doctor and local store`

Required checks:
- focused unit tests for config paths, migrations, and doctor probes
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 3: GitHub Client Layer

Implement the GitHub integration behind one internal client.

Requirements:
- Shell through `gh` first for authenticated operations.
- Support listing PRs, listing/posting issue comments, creating PRs, merging
  PRs, inspecting CI/check state, creating commit statuses, and fetching PR
  files/commits.
- Dedupe comments by GitHub comment id.
- Use conservative API behavior: serial mutating requests, basic rate-limit
  backoff, and conditional request support where practical.
- Do not implement GitHub Checks API in this task.

Suggested commit message:
`feat: add github client integration`

Required checks:
- fake client tests for comment listing/posting, status creation, PR merge, and
  rate-limit/backoff behavior
- direct `gh api` smoke tests where safe
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 4: Agent Registry And Runtime Adapters

Implement runtime-neutral agents and the first adapters.

Requirements:
- Implement `gitmoot agent subscribe/list/remove/doctor`.
- Store agent name, role, runtime, runtime reference, repo scope, capabilities,
  autonomy policy, and health state.
- Implement the `codex` adapter with `codex exec resume <session> <prompt>`.
- Implement the `claude` adapter with `claude --resume <session> -p`, using JSON
  output when available and text capture as fallback.
- Implement a `shell` adapter for simple future/custom runtimes.
- Keep workflow code runtime-agnostic.

Suggested commit message:
`feat: add agent registry and runtime adapters`

Required checks:
- unit tests for agent validation and runtime command construction
- smoke checks for `codex exec resume --help` and `claude --help` if Claude is
  installed
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 5: Job Mailbox And Prompt Contract

Implement the local job queue and normalized prompt/result contract.

Requirements:
- Add job states: `queued`, `running`, `blocked`, `failed`, `succeeded`,
  `cancelled`.
- Add job event logging.
- Render structured prompts containing repo, branch, PR, task, sender, requested
  action, constraints, and required output format.
- Parse `gitmoot_result` JSON blocks from agent output.
- If output is malformed, store raw output and retry once with a repair prompt.

Suggested commit message:
`feat: add job mailbox and agent result contract`

Required checks:
- unit tests for job state transitions, prompt rendering, result extraction, and
  repair retry behavior
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 6: Daemon And PR Comment Router

Implement the local daemon that watches GitHub and dispatches jobs.

Requirements:
- Implement `gitmoot daemon start --repo owner/repo --poll 30s`.
- Poll open PRs and comments.
- Parse `/gitmoot` commands.
- Resolve agent names and capabilities.
- Create jobs automatically.
- Post acknowledgement comments.
- Store every meaningful transition in SQLite.
- Keep polling conservative and resilient to API/rate-limit errors.

Suggested commit message:
`feat: add daemon and pr comment routing`

Required checks:
- fake GitHub tests for polling, dedupe, command parsing, unknown agents,
  acknowledgement comments, and job creation
- daemon cancellation/shutdown tests
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 7: Autonomous Workflow Engine

Implement automatic task/review/fix progression.

Requirements:
- Add task states: `planned`, `implementing`, `pr_open`, `reviewing`,
  `changes_requested`, `ready_to_merge`, `merged`, `blocked`.
- When a lead agent opens or updates a PR, automatically dispatch required
  reviewer jobs.
- When reviewers request changes, dispatch the fix job back to the lead agent.
- When reviewers approve, run the merge gate.
- Allow agents to request other agents through `next_agents`, without human
  approval when capability and repo scope allow it.
- Block and explain the reason when capability, scope, lock, or merge-gate
  policy rejects an action.

Suggested commit message:
`feat: add autonomous workflow engine`

Required checks:
- state machine tests for implement, review, changes requested, fix, approval,
  blocked, and ready-to-merge flows
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 8: Branch, PR, And Locking Rules

Implement safe branch ownership and PR conventions.

Requirements:
- Use one branch per task.
- Add one writer lock per branch.
- Reviewer agents are read/comment-only by default.
- Implementer agents may write only when they hold the branch lock.
- PR bodies must include WHAT, WHY, CHANGES, RESULTS, RISK, raw final review
  output, agent names, and task id.
- Use `gh pr create` and Git commands through shared subprocess helpers.
- Avoid duplicate Git/GitHub shell logic across commands.

Suggested commit message:
`feat: add task branches pr bodies and locks`

Required checks:
- unit tests for lock acquisition/release and PR body rendering
- local git smoke test with a temporary repo
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 9: Merge Gate

Implement the policy gate that controls automated merges.

Requirements:
- Gate requires clean local worktree, branch up to date or safely rebased,
  required reviewer approvals, no blocking findings, successful Gitmoot commit
  statuses, external CI success if present, final agent review captured, and no
  unhandled API/rate-limit errors.
- If a repo has no CI, record that explicitly in PR body/status.
- Merge with `gh pr merge --squash --match-head-commit <sha>` by default.
- After merge, update base, record merged commit hash, delete the task branch
  only when safe, and enqueue the next task.

Suggested commit message:
`feat: add merge gate`

Required checks:
- fake GitHub tests for passing/failing gate cases
- local smoke test for clean/dirty worktree detection
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 10: Documentation And End-To-End Example

Document the product and add a realistic local demo path.

Requirements:
- Add a walkthrough where the user starts Codex/Claude sessions, subscribes
  them as agents, imports a plan, starts the daemon, and watches PRs converge.
- Document local-only limitations: machine must stay online, polling is V1,
  GitHub Checks need future GitHub App mode, session ids should be explicit, and
  GitHub comments are audit trail rather than canonical state.
- Add adapter authoring docs for future runtimes.
- Add troubleshooting docs for `gh`, Codex, Claude, repo remotes, permissions,
  stale locks, malformed agent output, and rate limits.

Suggested commit message:
`docs: document gitmoot local workflow`

Required checks:
- docs link/path sanity check
- command examples manually reviewed against actual CLI names
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

## Final Response After All Tasks

- List completed tasks.
- For each task, list branch, PR URL, merge status, and merged commit hash.
- List tests/checks run.
- Include exact final raw `codex exec review --uncommitted` output for the last
  task/repo.
- Mention skipped checks, blockers, or residual risk.
- Do not claim interactive `/review` is clean. Say:
  `codex exec review is clean; ready for manual /review.`
