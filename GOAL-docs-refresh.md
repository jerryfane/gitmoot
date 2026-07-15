# Refresh Gitmoot Public Docs

Implement the plan task by task. Each task must be developed, reviewed, opened
as its own pull request, merged, and verified before moving on, unless tasks are
explicitly safe to run in parallel.

Refresh Gitmoot's public documentation so a new user, an AI coding agent, and a
release consumer can install, verify, use, troubleshoot, and understand the
current Gitmoot product without relying on stale live pages or source-only
references. The work is tracked by GitHub issue #288:
https://github.com/gitmoot/gitmoot/issues/288

The goal touches public Docusaurus docs, root documentation, skill references,
AI-readable docs, and lightweight project support files. It should not change
Gitmoot CLI/runtime behavior.

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
- When delegating to Gitmoot agents, use `gitmoot agent run`,
  `gitmoot agent implement`, `gitmoot agent review`, or `gitmoot task run`.
  Do not put branch creation, commit, push, PR creation, or merge instructions
  inside `gitmoot agent ask`; Gitmoot owns repository orchestration.
- If implementation depends on external APIs, docs, CLIs, data formats,
  generated scripts, installers, service launchers, subprocess calls, env vars,
  config formats, or third-party libraries, verify the real contract with local
  commands and/or official sources before editing.
- When adding Mermaid diagrams to Docusaurus docs, use valid GitHub/Docusaurus
  Mermaid syntax. Quote labels containing paths, slashes, punctuation, or spaces
  that may confuse Mermaid, for example `P["/var/www/gitmoot-docs"]`.

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
   service-launcher, or deployment changes, include an operational smoke test or
   direct contract check. Syntax checks alone are not enough.
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
3. Use the commit message specified by the plan. If the plan does not specify
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

- Parallelize only when tasks are independent, have disjoint file ownership, and
  can be reviewed and merged without order-dependent assumptions.
- Use a separate branch per task.
- Clearly assign each branch a task number and file ownership.
- Do not duplicate work across branches.
- For Gitmoot-owned task PRs, let the merge gate update stale branches and
  retry. If a real content conflict remains, resolve it in an explicit fix task
  and re-run checks/review.
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

### Task 1: Sync Public Docs Structure And Release Notes

Bring the public Docusaurus docs into alignment with the current source docs and
remove the most visible live-doc drift.

Scope:

- Update `website/sidebars.ts` release notes newest-first:
  `v0.3.0-beta.1`, `v0.2.0-beta.2`, `v0.2.0-beta.1`,
  `v0.1.0-beta.8`, `v0.1.0-beta.1`.
- Add missing website release note pages from root docs:
  `docs/release-notes/v0.2.0-beta.2.md` and
  `docs/release-notes/v0.3.0-beta.1.md`.
- Verify the current source page name is `agents-templates-jobs-locks`; do not
  reintroduce the old `agents-presets-jobs-locks` naming.
- Set docs color mode default to dark in Docusaurus if no local test reveals a
  regression, while keeping the light-mode toggle available.
- Add a concise docs information-architecture Mermaid diagram to `intro.md`
  only if it helps orient users. The diagram must build under the existing
  Docusaurus Mermaid configuration.

Acceptance criteria:

- The sidebar includes all current release notes in newest-first order.
- `website/docs/release-notes/v0.3.0-beta.1.md` documents the dashboard/TUI,
  train-init improvements, optimizer/train-run reliability work, and known
  limitations from the root release note.
- `website/docs/release-notes/v0.2.0-beta.2.md` exists and is linked.
- No source docs refer to `agents-presets-jobs-locks` as the current concept
  page.
- Any Mermaid diagram builds and does not use unquoted path labels.

Tests/checks:

- `git diff --check`
- `cd website && npm run build`
- `rg 'release-notes/v0.3.0-beta.1|release-notes/v0.2.0-beta.2' website/sidebars.ts`
- `rg 'v0.3.0-beta.1|dashboard' website/docs/release-notes/v0.3.0-beta.1.md`
- `rg 'agents-presets-jobs-locks' website/docs docs skills || true`

Suggested commit message:

```text
docs: sync release notes and docs navigation
```

### Task 2: Rewrite Install And Quick Start Around Agent-First Onboarding

Make setup usable for three audiences: humans installing manually, release
consumers verifying binaries, and Codex/Claude users asking their agent to
install Gitmoot as a skill/plugin.

Scope:

- Expand `website/docs/getting-started/install.md` with:
  - script install path
  - direct GitHub Releases binary fallback
  - Linux and macOS verification steps
  - Apple Silicon vs Intel artifact guidance when artifact names are known
  - dependency checks for `git`, `gh`, and `gitmoot`
  - `gh auth status` guidance for repo/PR workflows
  - update/restart-daemon commands
  - rollback/manual reinstall guidance
  - uninstall guidance that avoids deleting user state by default
  - SHA256 verification examples for Linux, macOS, and Windows.
- Update `website/docs/getting-started/quick-start.md` so the first path is a
  copyable agent prompt:

  ```text
  Install Gitmoot as a Codex or Claude skill/plugin in this repo, verify `gitmoot version`, run `gitmoot plugin doctor`, check `gh auth status`, and summarize the next Gitmoot workflow I can use.
  ```

- Keep manual CLI commands as a second path.
- Explain the execution model: plugin/skill provides discovery and guidance,
  the `gitmoot` CLI performs local execution, the daemon coordinates background
  jobs, and GitHub CLI supplies repo/PR auth.
- Add a Mermaid architecture diagram to install or quick-start docs if it makes
  the agent/plugin/CLI/daemon/GitHub relationship clearer.

Acceptance criteria:

- Install docs include `sha256sum`, `shasum -a 256`, and `certutil -hashfile`.
- Install docs include `gitmoot update --check` and
  `gitmoot update --restart-daemon`.
- Quick start includes the copyable agent prompt and still includes manual setup
  commands.
- Docs clearly state that plugins/skills do not replace the local CLI/daemon.
- Mermaid diagram, if added, builds cleanly.

Tests/checks:

- `git diff --check`
- `cd website && npm run build`
- `rg 'sha256sum|shasum -a 256|certutil -hashfile' website/docs/getting-started/install.md`
- `rg 'Install Gitmoot as a Codex or Claude skill/plugin' website/docs/getting-started/quick-start.md website/docs/getting-started/install.md`
- `rg 'gitmoot update --check|gitmoot update --restart-daemon' website/docs/getting-started/install.md`

Suggested commit message:

```text
docs: improve install verification and agent onboarding
```

### Task 3: Refresh CLI Reference And Dashboard/TUI Docs

Update public command documentation to match the current command surface and
make the dashboard/TUI discoverable.

Scope:

- Refresh `website/docs/reference/cli.md` from
  `skills/gitmoot/references/CLI.md`.
- Add or refresh coverage for:
  - `gitmoot dashboard`
  - `gitmoot interactive`
  - `gitmoot agent run`
  - `gitmoot agent review`
  - `gitmoot agent implement`
  - `gitmoot job cancel`
  - `gitmoot job events`
  - `gitmoot daemon logs`
  - `gitmoot lock release`
  - `gitmoot update --restart-daemon`
  - SkillOpt train `init`, `run`, `recover`, and current `continue` options.
- Add a dashboard/TUI section either inside `reference/cli.md` or as a linked
  short reference page. Keep it in `reference/cli.md` unless the page becomes
  too long.
- Document dashboard behavior:
  - real terminal opens interactive TUI
  - nonterminal/CI contexts should use `--plain` or `--json`
  - `--watch` redraws plain output
  - prompt answer/dismiss examples
  - dashboard shows daemon health, repos, agents, jobs, locks, prompts, and
    SkillOpt train state.
- Include an optional Mermaid state/flow diagram for dashboard state only if it
  clarifies the TUI/plain/json modes.

Acceptance criteria:

- The public CLI reference mentions all current major dashboard, agent, job,
  daemon, lock, interactive, and SkillOpt train commands.
- Dashboard usage examples include `--plain`, `--json`, `--watch`, `--answer`,
  and `--dismiss`.
- Workflow docs link to CLI reference for full option lists instead of
  duplicating every flag.

Tests/checks:

- `git diff --check`
- `cd website && npm run build`
- `rg 'gitmoot dashboard|gitmoot agent run|gitmoot interactive' website/docs/reference/cli.md`
- `rg 'agent review|agent implement|job cancel|job events|daemon logs|lock release' website/docs/reference/cli.md`
- `rg 'dashboard --plain|dashboard --json|dashboard --watch|dashboard --answer|dashboard --dismiss' website/docs/reference/cli.md`

Suggested commit message:

```text
docs: refresh CLI and dashboard reference
```

### Task 4: Update SkillOpt Workflow, Exchange Contract, And Preflight Guidance

Bring public SkillOpt docs up to the current train workflow and make
`gitmoot-skillopt` installation/preflight explicit before optimizer handoff.

Scope:

- Sync `website/docs/workflows/skillopt-train-workflow.md` from the fuller root
  `docs/skillopt-train-workflow.md`, while keeping the public page readable.
- Ensure the workflow covers:
  - what SkillOpt is
  - high-level `gitmoot skillopt train` vs low-level review/feedback/export
  - `train init`
  - template listing
  - interactive prompt collection
  - train start/status/run/continue/recover/stop
  - review publish/sync
  - optimizer handoff
  - candidate import/review
  - promote/reject/start-next
  - stale lock/heartbeat and no-candidate recovery.
- Refresh `website/docs/reference/skillopt-exchange-contract.md` from
  `docs/skillopt-exchange-contract.md`.
- Update `SKILL.md` and `skills/gitmoot/SKILL.md` if needed so agents are told
  to check `gitmoot-skillopt --version` and
  `gitmoot-skillopt optimize --help` before launching optimization.
- Add explicit `gitmoot-skillopt` install/preflight examples. Use the current
  package URL already referenced by canonical docs if no newer package source is
  present.
- Keep schema and package-format detail in the exchange contract, not in the
  quick-start workflow.
- Add or preserve a Mermaid sequence diagram for the train workflow. It should
  show User -> Gitmoot -> Agent -> GitHub -> gitmoot-skillopt -> Gitmoot
  candidate review/promotion.

Acceptance criteria:

- Public SkillOpt train docs include `train init`, `train run`, `train recover`,
  and `gitmoot-skillopt` preflight.
- Exchange contract cross-links to the train workflow, and the train workflow
  cross-links back to the exchange contract.
- Agent-facing skill instructions tell agents not to wait until optimizer
  handoff to discover that `gitmoot-skillopt` is missing.
- Mermaid sequence diagram builds cleanly.

Tests/checks:

- `git diff --check`
- `cd website && npm run build`
- `rg 'train init|train run|train recover|gitmoot-skillopt --version|optimize --help' website/docs/workflows/skillopt-train-workflow.md`
- `rg 'skillopt train|exchange contract|candidate package|training package' website/docs/reference/skillopt-exchange-contract.md`
- `rg 'gitmoot-skillopt --version|gitmoot-skillopt optimize --help' SKILL.md skills/gitmoot/SKILL.md`

Suggested commit message:

```text
docs: document SkillOpt train preflight and exchange flow
```

### Task 5: Expand Plugin, Runtime, Troubleshooting, And Operations Docs

Make runtime/plugin behavior and operational failure modes clear enough for
users and agents to recover without guessing.

Scope:

- Expand `website/docs/plugins/codex-claude.md` from `docs/plugins.md`.
- Include:
  - `gitmoot plugin install codex`
  - `gitmoot plugin install claude`
  - `gitmoot plugin doctor`
  - runtime discovery expectations
  - install location behavior at a high level
  - refresh/update/remove plugin guidance
  - Codex current-chat prompt import vs background Gitmoot jobs
  - Claude token/environment inheritance guidance without exposing secrets.
- Refresh `website/docs/reference/runtime-adapters.md` from root adapter docs.
- Update `website/docs/operations/troubleshooting.md` with symptom/cause/check/fix
  sections for:
  - install script failed
  - binary not on PATH
  - checksum mismatch
  - `gh auth status` fails
  - plugin doctor fails
  - runtime session not found
  - daemon not running
  - stale lock
  - job stuck or failed
  - dashboard blank/noninteractive
  - SkillOpt optimizer missing
  - SkillOpt dependency or credential failure
  - train session recoverable
  - live docs/LLM context stale after deploy.
- Update `website/docs/operations/deployment.md` with the current docs build
  and deploy flow plus post-deploy smoke checks.
- Add a Mermaid deployment/source-flow diagram in operations docs only if it is
  useful. Quote all path labels such as `"/var/www/gitmoot-docs"`.

Acceptance criteria:

- Plugin docs clearly distinguish runtime discovery from Gitmoot execution.
- Troubleshooting docs give actionable diagnostics without destructive cleanup
  by default.
- Deployment docs include `npm run build`, `rsync -a --delete`, and live smoke
  checks for CLI, SkillOpt, release notes, and `llms.txt`.
- Runtime docs are linked from install, plugin docs, and troubleshooting.

Tests/checks:

- `git diff --check`
- `cd website && npm run build`
- `rg 'plugin install codex|plugin install claude|plugin doctor' website/docs/plugins/codex-claude.md`
- `rg 'symptom|likely cause|diagnostic|fix|checksum mismatch|dashboard blank|SkillOpt optimizer missing' website/docs/operations/troubleshooting.md`
- `rg 'rsync -a --delete|llms.txt|release-notes/v0.3.0-beta.1' website/docs/operations/deployment.md`

Suggested commit message:

```text
docs: expand plugin runtime and operations guidance
```

### Task 6: Update AI-Readable Docs And Project Support Files

Make Gitmoot easier for AI agents and contributors to understand by refreshing
`llms.txt`, `llms-full.txt` generation, and lightweight support files.

Scope:

- Update `website/static/llms.txt` with direct links for:
  - Introduction
  - Install
  - Quick Start
  - Dashboard/TUI or CLI reference
  - CLI Reference
  - Runtime Adapters
  - Codex and Claude Plugins
  - SkillOpt Train Workflow
  - SkillOpt Exchange Contract
  - Troubleshooting
  - Release Notes
  - Full LLM Context
  - GitHub Repository.
- Update `website/scripts/build-llms-full.mjs` to include:
  - `docs/skillopt-train-workflow.md`
  - `docs/skillopt-exchange-contract.md`
  - `docs/herdr-composable-train-init.md` if still relevant
  - release notes files
  - plugin docs
  - troubleshooting docs
  - current skill CLI/workflow references.
- Preserve the current generated-file model:
  - script reads canonical Markdown sources
  - script writes `website/static/llms-full.txt`
  - Docusaurus copies it into the public static output.
- Add root-level `SECURITY.md` unless the project owner already has an
  applicable org-level policy.
- Add root-level `CONTRIBUTING.md` with setup, build/test, docs build,
  `llms-full.txt`, style, and smoke-test guidance.
- Do not add `.github/ISSUE_TEMPLATE` or PR template unless this task remains
  small after the required work. If added, keep templates minimal.

Acceptance criteria:

- `/llms.txt` points agents to current install, dashboard, SkillOpt, release,
  CLI, runtime, plugin, troubleshooting, and full-context pages.
- `llms-full.txt` generation includes current root SkillOpt docs, release
  notes, and skill references.
- `SECURITY.md` gives beta-appropriate vulnerability reporting guidance without
  overpromising SLAs.
- `CONTRIBUTING.md` gives enough local setup/check commands for contributors.

Tests/checks:

- `git diff --check`
- `cd website && npm run build`
- `rg 'SkillOpt|Dashboard|Release Notes|CLI Reference|Troubleshooting' website/static/llms.txt`
- `rg 'skillopt-train-workflow|skillopt-exchange-contract|release-notes/v0.3.0-beta.1|skills/gitmoot/references/CLI.md' website/static/llms-full.txt`
- `rg 'vulnerability|security|supported' SECURITY.md`
- `rg 'npm run build|go test|git diff --check|llms-full' CONTRIBUTING.md`

Suggested commit message:

```text
docs: refresh agent context and contributor guidance
```

### Task 7: Final Build, Deploy, And Live Verification

After all docs PRs are merged, rebuild and deploy the public docs, then verify
the live site no longer serves stale routes.

Scope:

- Update the local base branch to the merged target branch.
- Run the final docs build.
- Deploy built docs with delete semantics:

  ```sh
  rsync -a --delete /root/gitmoot/website/build/ /var/www/gitmoot-docs/
  ```

- Verify live docs:
  - intro loads
  - CLI page contains dashboard/current agent commands
  - SkillOpt train page contains init/recover/preflight
  - release notes `v0.3.0-beta.1` renders as a real page
  - `/llms.txt` contains current links
  - `/llms-full.txt` contains current full context.
- Record deployment commands and live smoke results in the final response and,
  if appropriate, in issue #288.

Acceptance criteria:

- `https://gitmoot.io/docs/release-notes/v0.3.0-beta.1` renders a real
  Docusaurus docs page, not a meta-refresh to `/docs/intro`.
- Live CLI docs mention `gitmoot dashboard`, `gitmoot agent run`,
  `gitmoot interactive`, `agent review`, `agent implement`, job events/cancel,
  daemon logs, and lock release.
- Live install docs cover macOS, binary fallback, dependency checks, updates,
  uninstall, and SHA256 verification.
- Live quick start includes the copyable agent-install prompt.
- Live SkillOpt workflow includes `gitmoot-skillopt` install/preflight before
  optimizer handoff.
- `/llms.txt` and `/llms-full.txt` expose current SkillOpt, dashboard, CLI,
  release notes, and troubleshooting content.

Tests/checks:

- `git status --short --branch`
- `git diff --check`
- `cd website && npm run build`
- `curl -fsS https://gitmoot.io/docs/intro >/dev/null`
- `curl -fsS https://gitmoot.io/docs/reference/cli | rg 'gitmoot dashboard|agent run|interactive'`
- `curl -fsS https://gitmoot.io/docs/workflows/skillopt-train-workflow | rg 'train init|train recover|gitmoot-skillopt'`
- `curl -fsS https://gitmoot.io/docs/release-notes/v0.3.0-beta.1 | rg 'v0.3.0-beta.1|dashboard'`
- `curl -fsS https://gitmoot.io/llms.txt | rg 'SkillOpt|Dashboard|Release Notes'`
- `curl -fsS https://gitmoot.io/llms-full.txt | rg 'skillopt-train-workflow|CLI.md|release-notes'`

Suggested commit message:

```text
docs: deploy refreshed public documentation
```
