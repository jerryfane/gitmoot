# Gitmoot Beta Goal

Implement the Gitmoot beta plan task by task. Each task must be developed,
reviewed, opened as its own pull request, merged, and verified before moving on,
unless tasks are explicitly safe to run in parallel.

Gitmoot is a local-first Go binary that coordinates persistent AI agent
sessions through GitHub pull requests. The beta must make the full local loop
work end to end: one background daemon watches one or more registered repos,
routes PR comments to allowed agents, executes queued jobs through runtime
adapters, posts attributed results back to PRs, advances workflow state, exposes
recovery/debug commands, and ships a beta release artifact with agent-facing
skill documentation.

## Core Rules

- Work one task at a time in the listed order by default.
- If tasks are independent, have disjoint file ownership, and do not depend on
  each other's results, they may be done in parallel on separate branches.
- Do not start dependent work until the prerequisite task has passed checks,
  passed `codex exec review --uncommitted`, been pushed, opened as a PR, merged,
  and verified on the target branch.
- Do not commit generated data, reports, caches, build artifacts, secrets,
  credentials, session archives, cloned helper repos, or large outputs unless
  the plan explicitly says they are intended tracked fixtures/artifacts.
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
  Claude assumptions into workflow, GitHub, daemon, or database packages.
- V1 beta is local-only. Do not add a hosted service, GitHub App, billing,
  remote dashboard, webhook receiver, or cloud runner unless a later task
  explicitly changes scope.
- Keep GitHub comments authored by the authenticated user for beta. Agent
  identity must be shown in the comment body, not spoofed as the GitHub author.

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
6. Verify local runtime tooling before runtime-adapter or worker tasks:
   - `codex exec resume --help`
   - `claude --help`, if implementing or testing Claude paths
7. Verify GitHub API behavior with official docs or `gh api` before implementing
   GitHub mutation paths. In particular, confirm PR comments use issue-comment
   APIs, release update checks use GitHub Releases APIs, and commit statuses are
   used rather than GitHub Checks for V1 beta.
8. Verify OpenClaw `SKILL.md` format from official documentation before editing
   the skill file.

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
   service-launcher, GitHub API, daemon, update, or runtime-adapter changes,
   include an operational smoke test or direct contract check. Syntax checks
   alone are not enough.
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

## Beta Architecture

The beta architecture is:

```text
GitHub PR comments/state
  -> local gitmoot daemon
  -> local SQLite repo/agent/job/task state
  -> runtime-neutral worker
  -> Codex, Claude Code, shell, or future runtime adapter
  -> attributed GitHub PR comments/statuses
  -> workflow state transitions and merge gate
```

Core beta decisions:
- One background daemon process manages all enabled repos for one Gitmoot home.
- `gitmoot daemon run` is foreground/debug mode.
- Agents are global identities. Repo access is many-to-many through explicit
  allow/deny bindings.
- A single agent may work across multiple repos when allowed.
- A PR's repository provides the routing context for `/gitmoot <agent> ...`.
- Implementation jobs must validate the target checkout before delivery.
- GitHub comments must include an attribution block:

  ```md
  > Agent: `agent-name`
  > Runtime: `codex|claude|shell`
  > Job: `job-id`
  ```

- Auto-update must be conservative. If Gitmoot cannot prove a safe update path,
  print exact manual commands instead of mutating the binary.

## Key Interfaces To Implement

- Repo commands:
  - `gitmoot repo add owner/repo --path <path>`
  - `gitmoot repo list`
  - `gitmoot repo remove owner/repo`
  - `gitmoot repo doctor owner/repo`
- Agent commands:
  - `gitmoot agent subscribe <name> --runtime codex|claude|shell --session <ref> --role <role> --capability <capability> [--repo owner/repo...]`
  - `gitmoot agent allow <name> --repo owner/repo`
  - `gitmoot agent deny <name> --repo owner/repo`
  - `gitmoot agent repos <name>`
  - existing `agent list/remove/doctor` behavior must remain.
- Daemon commands:
  - `gitmoot daemon start [--workers N]`
  - `gitmoot daemon run [--repo owner/repo] [--workers N] [--dry-run]`
  - `gitmoot daemon stop`
  - `gitmoot daemon restart`
  - `gitmoot daemon status`
  - `gitmoot daemon logs`
- Job commands:
  - `gitmoot job list [--repo owner/repo]`
  - `gitmoot job show <job-id>`
  - `gitmoot job events <job-id>`
  - `gitmoot job run <job-id>`
  - `gitmoot job retry <job-id>`
  - `gitmoot job cancel <job-id>`
- Lock commands:
  - `gitmoot lock list [--repo owner/repo]`
  - `gitmoot lock show owner/repo <branch>`
  - `gitmoot lock release owner/repo <branch> --owner <agent>`
  - `gitmoot lock release owner/repo <branch> --force`
- Setup/help/config/version/update commands:
  - `gitmoot setup --repo owner/repo --path . --agent <name> --runtime <runtime> --session <ref> [--start-daemon]`
  - `gitmoot config path`
  - `gitmoot config show`
  - `gitmoot events --repo owner/repo`
  - `gitmoot version`
  - `gitmoot version --json`
  - `gitmoot update --check`
  - `gitmoot update`
  - `gitmoot update --restart-daemon`
- PR commands:
  - `/gitmoot help`
  - `/gitmoot status`
  - `/gitmoot <agent> review [instructions]`
  - `/gitmoot <agent> implement [instructions]`
  - `/gitmoot ask <agent> [question]`
  - `/gitmoot merge`
  - `/gitmoot retry <job-id>`
  - `/gitmoot cancel <job-id>`
- Required result contract remains:

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

## Implementation Tasks

### Task 1: Repo Hygiene And Beta Foundations

Prepare the repo and shared CLI foundations for beta work.

Requirements:
- Add `/repos/` to root `.gitignore`.
- Add build metadata support for version, commit, build date, and Go version.
- Add shared helpers for text/JSON command output to avoid duplicated command
  formatting.
- Keep the current CLI help and existing commands working.
- Do not commit cloned helper repos or session archives.

Suggested commit message:
`chore: add beta foundations`

Required checks:
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 2: Repo Registry And Multi-Repo Store

Add first-class local repo registration.

Requirements:
- Extend repo state with checkout path, enabled flag, poll interval, last poll
  timestamp, and last error.
- Add store methods for creating, updating, listing, enabling/disabling, and
  removing repos.
- Add `gitmoot repo add/list/remove/doctor`.
- Validate repo path by checking Git root and `origin` remote match the
  requested `owner/repo`.
- Preserve current `--repo` flows by auto-upserting repo records when safe.

Suggested commit message:
`feat: add repo registry`

Required checks:
- focused tests for `internal/db`, `internal/git`, and `internal/cli`
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 3: Global Agents With Multi-Repo Access

Move from one repo scope per agent to global agents with explicit repo access.

Requirements:
- Add `agent_repos(agent_name, repo_full_name)` or equivalent store model.
- Migrate existing agents so their current repo scope becomes an allowed repo.
- Keep agent names globally unique.
- Update authorization checks to use agent repo access instead of the legacy
  single `repo_scope`.
- Add `gitmoot agent allow/deny/repos`.
- Allow repeated `--repo` flags during `agent subscribe` as shorthand for
  subscribe plus allow.
- Keep existing `agent list/remove/doctor` behavior working.

Suggested commit message:
`feat: add multi-repo agent access`

Required checks:
- focused tests for DB migration, agent CLI, daemon routing, and workflow
  authorization
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 4: Background Daemon Management

Make the daemon usable without keeping a terminal open.

Requirements:
- Add `daemon start/run/stop/restart/status/logs`.
- `daemon start` runs in the background and manages all enabled repos.
- `daemon run` runs in the foreground for debugging and may accept
  `--repo owner/repo`.
- Store PID, log, and metadata files under Gitmoot home.
- Detect already-running daemon processes.
- Clean up stale PID files safely.
- Keep foreground cancellation behavior reliable.

Suggested commit message:
`feat: add daemon process management`

Required checks:
- focused tests for daemon CLI lifecycle and stale PID handling
- direct smoke test for `daemon run` cancellation
- direct smoke test for background `daemon start/status/stop`
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 5: Multi-Repo Daemon Poller

Refactor the daemon to poll all enabled repos safely.

Requirements:
- Extract the current one-repo polling logic into a reusable per-repo poller.
- Add a supervisor loop that loads enabled repos and polls each one using its
  checkout path and poll interval.
- Keep `daemon run --repo owner/repo` as single-repo debug mode.
- Ensure all PR/comment/job/task/lock/status operations include repo context.
- Add GitHub API error backoff and persist per-repo `last_error`.
- Ensure one repo's failure does not stop polling other repos.

Suggested commit message:
`feat: add multi-repo daemon polling`

Required checks:
- fake GitHub tests with two repos
- tests proving repo A comments never route to repo B jobs
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 6: Worker Execution Loop

Wire queued jobs to actual runtime adapter execution.

Requirements:
- Add worker execution inside the daemon.
- Reuse `workflow.Engine.RunJob` and `workflow.Mailbox.Run`; do not duplicate
  delivery, repair, parsing, or state-transition logic.
- Add `--workers N` to `daemon start` and `daemon run`, default `1`.
- Claim queued jobs atomically.
- Load the assigned agent, choose the correct adapter, and run the job from the
  target repo checkout.
- Before implementation jobs, validate checkout path, current remote, branch,
  worktree state, and branch lock.
- Recover stale `running` jobs on daemon startup by marking them failed or
  returning them to queued with an event.

Suggested commit message:
`feat: add daemon job workers`

Required checks:
- worker tests with shell adapter success/failure
- malformed output repair tests
- cancellation/race tests around running jobs
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 7: Agent-Attributed PR Result Comments

Post clear, safe PR comments for completed jobs.

Requirements:
- Add one shared formatter for job result comments.
- Include the attribution block with agent, runtime, and job id.
- Include decision, summary, findings, changes made, tests run, needs, and next
  agents when present.
- Post failure and malformed-output diagnostics using the same attribution
  block.
- Redact common secret/token patterns before posting to GitHub.
- Keep raw output in local SQLite, not in PR comments when it is too large or
  risky.

Suggested commit message:
`feat: add attributed job result comments`

Required checks:
- formatter tests
- redaction tests
- fake GitHub comment-post tests
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 8: Job CLI And PR Recovery Commands

Add user-facing job inspection and recovery.

Requirements:
- Add `gitmoot job list/show/events/run/retry/cancel`.
- Add `/gitmoot retry <job-id>` and `/gitmoot cancel <job-id>`.
- Retry only failed, blocked, or cancelled jobs.
- Cancel only queued or running jobs.
- Preserve prior job events and raw outputs when retrying.
- Ensure `job run` uses the same internals as daemon workers.

Suggested commit message:
`feat: add job recovery commands`

Required checks:
- CLI tests for all job commands
- PR command tests for retry/cancel
- state-transition tests
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 9: Lock Management CLI

Expose safe branch-lock recovery.

Requirements:
- Add `gitmoot lock list/show/release`.
- Require exact owner for release unless `--force` is passed.
- Record lock release events.
- Keep implement jobs blocked unless the agent holds the branch lock.
- Make lock output repo-aware for multi-repo workflows.

Suggested commit message:
`feat: add lock management`

Required checks:
- lock CLI tests
- branch-lock workflow tests
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 10: Help, Setup, Config, Events, And Status UX

Improve onboarding and operational visibility.

Requirements:
- Add `/gitmoot help` with repo-specific commands and known allowed agents.
- Add `gitmoot setup --repo owner/repo --path . --agent <name> --runtime <runtime> --session <ref> [--start-daemon]`.
- Add `gitmoot config path` and `gitmoot config show`.
- Add `gitmoot events --repo owner/repo`.
- Keep setup explicit and non-magical: print each step and stop on risky
  choices.
- Update `gitmoot status` so it is useful both globally and with `--repo`.

Suggested commit message:
`feat: add setup and help commands`

Required checks:
- CLI tests for setup/config/events/status
- PR help command tests
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 11: Version And Update Commands

Add local operational update support for beta users.

Requirements:
- Add `gitmoot version` and `gitmoot version --json`.
- Add `gitmoot update --check`, `gitmoot update`, and
  `gitmoot update --restart-daemon`.
- Embed version, commit, build date, and Go version with sensible development
  defaults.
- `update --check` queries GitHub Releases latest release.
- `update` only self-updates when the install method and binary path are safe.
- If safe auto-update is not possible, print exact manual update commands.
- Restart daemon only when `--restart-daemon` is explicit.

Suggested commit message:
`feat: add version and update commands`

Required checks:
- version CLI tests
- fake release-check tests
- unsafe update path tests
- direct `gitmoot version` smoke test
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 12: OpenClaw-Compatible Agent Skill

Add agent-facing skill documentation.

Requirements:
- Add root `SKILL.md`.
- Use official OpenClaw-compatible frontmatter with:
  - `name`
  - `description`
  - `version`
  - `metadata.openclaw.requires.bins`
  - optional `GH_TOKEN` env var declaration
- Explain what Gitmoot is, PR command workflow, required `gitmoot_result`,
  branch locks, repo access, status/debug commands, and safe agent behavior.
- Tell agents to reread `SKILL.md` when they are unsure how Gitmoot expects
  them to behave.

Suggested commit message:
`docs: add gitmoot agent skill`

Required checks:
- frontmatter parse test or lightweight validation test
- docs link check by inspection
- `go test ./...`
- `go vet ./...`
- `git diff --check`
- `codex exec review --uncommitted`

### Task 13: Docs And Live Smoke Test Guide

Update user-facing docs for the beta workflow.

Requirements:
- Update README and docs for multi-repo setup, background daemon management,
  worker execution, job/lock recovery, update/version, and skill usage.
- Add a one-repo live smoke test guide:
  comment -> queued job -> adapter result -> PR comment -> status update.
- Add a two-repo live smoke test guide:
  one daemon -> two registered repos -> same allowed agent -> no cross-routing.
- Document known V1 limits: local-only, polling not webhooks, GitHub comments
  authored by authenticated user, no hosted dashboard, no GitHub App bot
  identity.

Suggested commit message:
`docs: document beta workflow`

Required checks:
- `go test ./...`
- `go vet ./...`
- docs command snippets inspected against current CLI help
- `git diff --check`
- `codex exec review --uncommitted`

### Task 14: Beta Release

Cut the first beta only after the full workflow is verified.

Requirements:
- Run complete automated checks:
  - `go test ./...`
  - `go vet ./...`
  - `git diff --check`
- Run live one-repo smoke test.
- Run live two-repo smoke test.
- Confirm no generated data, cloned repos, secrets, session archives, or large
  outputs are tracked.
- Tag `v0.1.0-beta.1`.
- Create GitHub release notes listing features and known V1 limits.

Suggested commit message:
`chore: prepare v0.1.0-beta.1`

Required checks:
- exact live smoke test commands and results recorded
- `codex exec review --uncommitted`

## Final Response After All Tasks

- List completed tasks.
- For each task, list branch, PR URL, merge status, and merged commit hash.
- List tests/checks run.
- Include exact final raw `codex exec review --uncommitted` output for the last
  task/repo.
- Mention skipped checks, blockers, or residual risk.
- Include beta release tag and release URL if Task 14 completed.
- Do not claim interactive `/review` is clean. Say:
  `codex exec review is clean; ready for manual /review.`
