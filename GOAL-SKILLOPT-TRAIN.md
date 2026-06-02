# SkillOpt Train Mode

Implement the plan task by task. Each task must be developed, reviewed, opened
as its own pull request, merged, and verified before moving on, unless tasks are
explicitly safe to run in parallel.

Build a high-level `gitmoot skillopt train` workflow that enforces the full
human-feedback SkillOpt optimization loop. The intended user-facing outcome is
that a user can start a training session for an agent template, review generated
outputs in a clear GitHub issue, sync human feedback, run the external
`gitmoot-skillopt` optimizer, review clean candidate template rewrites, promote
or reject candidates, and continue only from a promoted candidate. This goal
touches SkillOpt CLI orchestration, state storage, feedback parsing, GitHub
review packets, training-package metadata, candidate review, docs, and skill
references. Do not embed the external optimizer into Gitmoot's Go binary; keep
Gitmoot as the product/control layer and call or package data for
`gitmoot-skillopt`.

External checks already confirmed the direction: SkillOpt's core loop is
rollout evidence, optimizer reflection, bounded skill edits, validation gates,
and exported reusable skills; GitHub issues/comments/PRs/repos/Pages are
API-addressable review surfaces; ranked/K-way human feedback is appropriate for
preference exploration. Re-verify current contracts before implementation when
touching GitHub APIs, GitHub Pages assumptions, external optimizer flags, or
runtime agent invocation.

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

### Task 1: Add SkillOpt Train Session State

Scope:

- Add canonical train session and train iteration storage using the existing
  `internal/db/store.go` migration style. Do not overload only
  `eval_runs.metadata_json` for the main state machine.
- Suggested entities:
  - train session id, template id/current version, target repo, workspace repo,
    optional preview repo, human request summary, task kind, state, created and
    updated timestamps.
  - train iteration id, session id, eval run id, base template version,
    candidate version, mode, exploration level, state, issue URL/number,
    PR URL/number, and metadata JSON.
- Add `internal/skillopt/train.go` or an equivalent focused package for train
  states, transitions, status summaries, and validation helpers.
- Supported states should include:
  - `request_confirmed`
  - `workspace_ready`
  - `items_ready`
  - `options_generated`
  - `review_published`
  - `feedback_synced`
  - `training_package_created`
  - `optimizer_completed`
  - `candidate_created`
  - `candidate_review_published`
  - `candidate_promoted`
  - `candidate_rejected`
  - `run_abandoned`
- Enforce that a new iteration cannot start unless the prior iteration is
  promoted, rejected with a reason, or abandoned.
- Add status formatting helpers that report current phase, completed steps,
  blocked step, next action, latest issue/PR links, latest candidate, and
  feedback count.

Acceptance criteria:

- Store migrations are backward-compatible with existing databases.
- Transition validation prevents skipping candidate generation after feedback
  sync.
- Status summaries work for an empty session, an active iteration, and a
  resolved iteration.
- Existing low-level SkillOpt commands continue to work unchanged.

Tests/checks:

- `go test ./internal/db ./internal/skillopt`
- `go test ./internal/cli -run SkillOpt`
- `go test ./...`
- `git diff --check`
- `codex exec review --uncommitted`

Suggested branch:

- `task/skillopt-train-state`

Suggested commit message:

- `feat(skillopt): add train session state`

### Task 2: Add Train CLI Skeleton And Request Confirmation

Scope:

- Add a `gitmoot skillopt train` command group to `internal/cli/skillopt.go`:

  ```sh
  gitmoot skillopt train start
  gitmoot skillopt train status
  gitmoot skillopt train continue
  gitmoot skillopt train stop
  ```

- Implement `train start` as a request/session initializer, not the full
  optimizer loop yet.
- Accept flags for:
  - `--template <id-or-version>`
  - `--repo owner/repo`
  - `--session <id>`
  - `--workspace-repo owner/repo`
  - `--preview-repo owner/repo`
  - `--request <text>`
  - `--request-file <path>`
  - `--task-kind correctness|ux|design|writing|data|custom`
  - `--mode explore|refine|distill|validate`
  - `--exploration-level high|medium|low`
  - `--options <N>`
- Add `--dry-run` to print the inferred session summary and next action without
  writing state.
- Resolve and pin the current template version when starting the session.
- Summarize the human request into structured metadata, using deterministic
  local parsing only for this task. Do not call an LLM yet.
- Require explicit confirmation unless `--yes` is passed. For non-interactive
  mode, print the exact command with `--yes`.
- Add `train status` using the Task 1 status helpers.
- Add `train stop --reason <text>` to mark a session/iteration abandoned.

Acceptance criteria:

- `gitmoot skillopt --help` and `gitmoot skillopt train --help` show the new
  command group.
- `train start --dry-run` prints a request summary and does not mutate state.
- `train start --yes` creates a session and first iteration from a pinned
  template version.
- `train status` shows phase, blocked step, and next action.
- `train stop` records an abandonment reason and prevents accidental continue.

Tests/checks:

- `go test ./internal/cli -run SkillOpt`
- `go test ./internal/skillopt ./internal/db`
- CLI smoke in a temp home:
  - add a local template
  - start a train session with `--dry-run`
  - start with `--yes`
  - inspect status
  - stop with a reason
- `go test ./...`
- `git diff --check`
- `codex exec review --uncommitted`

Suggested branch:

- `task/skillopt-train-cli`

Suggested commit message:

- `feat(skillopt): add train cli skeleton`

### Task 3: Add Workspace, Item Planning, And Evaluation Gate Metadata

Scope:

- Add train-session support for workspace and preview repositories.
- Prefer selecting existing repos first. Creating a temporary private GitHub repo
  should be a future-compatible path, but if implemented now it must verify the
  real `gh`/GitHub API contract and token scopes before mutation.
- Add validation for preview publishing:
  - public or Pages-enabled preview repo for clickable demos;
  - clear warning when private Pages support cannot be assumed.
- Add training item planning input:
  - `--items-file <yaml|json>`
  - `--min-items <N>` defaulting to more than one item
  - item id, title, brief, target audience, output type, and optional artifact
    hints.
- Detect obviously homogeneous item sets with simple deterministic checks:
  duplicate titles/briefs, too few distinct product/category terms, or fewer
  than the configured minimum items.
- Store evaluation metadata on the train iteration and associated eval run:

  ```yaml
  evaluation:
    preferred_gate: hard | soft | hard_then_soft
  ```

- Default gate rules:
  - `hard` for correctness/data-contract tasks.
  - `soft` for UX/design/writing preference tasks.
  - `hard_then_soft` for human preference tasks where a correctness floor plus
    preference signal is useful.
- Update `gitmoot skillopt export` so training packages expose the preferred
  gate in `evaluator_config` without breaking existing contract version 1
  consumers.

Acceptance criteria:

- Train sessions can record workspace repo, preview repo, items, and preferred
  gate.
- Starting with too few items fails with a clear action.
- Homogeneous item sets warn but can be explicitly accepted with `--yes` or a
  dedicated override.
- Exported packages include preferred gate metadata for train-created runs.
- Existing manual `skillopt review create` exports remain compatible.

Tests/checks:

- `go test ./internal/cli -run SkillOpt`
- `go test ./internal/config ./internal/skillopt ./internal/db`
- Contract test proving `evaluator_config.evaluation.preferred_gate` round-trips.
- `go test ./...`
- `git diff --check`
- `codex exec review --uncommitted`

Suggested branch:

- `task/skillopt-train-workspace-items`

Suggested commit message:

- `feat(skillopt): add train workspace and item metadata`

### Task 4: Add Temporary Agent Option Generation

Scope:

- Add a train generation step that creates review options through Gitmoot-managed
  temporary agents instead of the current Codex session manually generating
  files.
- Reuse existing agent instance, runtime adapter, subprocess runner, resource
  lock, and job-event patterns where possible. Do not create a separate runtime
  abstraction.
- Provide a deterministic command shape, for example:

  ```sh
  gitmoot skillopt train continue --session <id>
  ```

  when the next required step is option generation.
- Generate one eval review run per iteration and add review items/options using
  the existing artifact-backed review item paths.
- Persist prompts, generated artifacts, preview URLs or paths, runtime name,
  agent name, job ids, and generation status in train metadata.
- If the selected runtime is unavailable, fail with a clear message and leave
  the iteration blocked at option generation.
- Respect existing repo checkout locks, runtime session locks, and branch locks.
- Keep generation strategy configurable, but default to high exploration with
  visibly different options when the session mode is `explore`.

Acceptance criteria:

- `train continue` can advance from `items_ready` to `options_generated` using
  temporary agent jobs.
- Failed generation records an actionable blocked status without corrupting the
  eval run.
- Generated options are registered as artifacts and review options exactly like
  low-level `skillopt review item add --option`.
- Re-running `train continue` after successful generation is idempotent or
  clearly refuses duplicate generation.

Tests/checks:

- Unit tests with fake runtime adapter/subprocess runner.
- Store tests for generation metadata and idempotence.
- CLI smoke with a fake local runtime or test adapter; do not require real
  Codex/Claude model calls in CI.
- `go test ./internal/cli ./internal/runtime ./internal/workflow ./internal/skillopt`
- `go test ./...`
- `git diff --check`
- `codex exec review --uncommitted`

Suggested branch:

- `task/skillopt-train-generation`

Suggested commit message:

- `feat(skillopt): generate train options with temporary agents`

### Task 5: Improve GitHub Review Packets And Feedback Normalization

Scope:

- Make train-created GitHub review issues concise and table-first.
- Keep the existing feedback collector reusable, but add train-aware rendering
  for:
  - run id and session id
  - what the human is comparing
  - table of items/options
  - preview links when artifact metadata includes URLs
  - short copyable YAML feedback format
  - minimal instructions
- Avoid including parseable sample comments outside the intended feedback block.
- Extend ranked feedback parsing with optional fields:

  ```yaml
  quality: poor | acceptable | strong
  continue_mode: explore | refine | distill | validate
  promote: yes | no
  ```

- Add canonical storage/export support for those fields, either as explicit
  columns or structured JSON on ranked feedback events. Prefer explicit fields
  only if they simplify phase logic without broad migration risk.
- Add an optional normalization step that can repair or canonicalize common
  human-feedback shapes:
  - malformed ranking whitespace
  - long reasoning containing colons
  - duplicated item-level notes
  - natural-language ranking summaries
- Keep normalization deterministic in Gitmoot for MVP. If LLM normalization is
  added later, keep it opt-in and visibly separate.
- Update phase recommendations so absolute poor quality or explicit
  `continue_mode: explore` can override ranking stability.

Acceptance criteria:

- GitHub train review issues are short and render items/options in tables.
- Feedback with rankings plus optional quality/continue/promote fields imports
  successfully.
- A stable winner no longer forces `refine` when every reviewed item says
  `quality: poor` or `continue_mode: explore`.
- Existing A/B feedback and existing ranked feedback without optional fields
  remain valid.
- Parse failures point to the exact comment/item and suggest block-scalar YAML
  for long reasoning.

Tests/checks:

- `go test ./internal/feedback ./internal/skillopt`
- `go test ./internal/cli -run SkillOpt`
- Golden/body tests for concise GitHub issue output.
- Parser tests for optional fields and malformed-but-normalizable feedback.
- Phase recommendation tests for quality/continue overrides.
- `go test ./...`
- `git diff --check`
- `codex exec review --uncommitted`

Suggested branch:

- `task/skillopt-train-feedback-ux`

Suggested commit message:

- `feat(skillopt): add train feedback quality signals`

### Task 6: Add Optimizer Handoff And Candidate Import Gate

Scope:

- Add the train step that exports the training package, invokes or instructs the
  external optimizer, imports the candidate package, and updates train state.
- Verify the current `gitmoot-skillopt optimize --help` contract before
  implementation. Keep the command configurable so future forks or installed
  paths can be used.
- Provide flags/config for:
  - optimizer executable path
  - model name, defaulting only if a safe default is configured
  - artifact root
  - output root
  - dry-run mode
  - preferred gate
- Do not expose secrets in logs, issue comments, PR bodies, artifacts, or
  status output.
- Record optimizer command metadata safely: executable name, package paths,
  model label if non-secret, duration, exit status, candidate file path, and
  summary.
- Import generated candidate packages through the existing
  `skillopt.ImportCandidatePackage` path so candidate validation remains shared.
- Enforce that feedback-synced train iterations must run this candidate
  generation step before another iteration can start.

Acceptance criteria:

- `train continue` advances from `feedback_synced` to
  `training_package_created`, `optimizer_completed`, and `candidate_created`
  when the external optimizer succeeds.
- `--dry-run` validates the package and command shape without model calls.
- Optimizer failures leave actionable status and do not create partial promoted
  templates.
- Imported candidates are pending and never promoted automatically.
- Existing `gitmoot skillopt export` and `gitmoot skillopt import` commands
  still work standalone.

Tests/checks:

- Unit tests with fake subprocess runner.
- CLI tests for dry-run, optimizer failure, and candidate import success.
- Contract smoke using a local fake `gitmoot-skillopt` executable.
- `go test ./internal/cli ./internal/skillopt ./internal/subprocess ./internal/db`
- `go test ./...`
- `git diff --check`
- `codex exec review --uncommitted`

Suggested branch:

- `task/skillopt-train-optimizer`

Suggested commit message:

- `feat(skillopt): orchestrate train optimizer handoff`

### Task 7: Add Candidate Review, PR/Preview Links, And Promotion Flow

Scope:

- Add train candidate review behavior after candidate import.
- Publish a candidate review step that links:
  - candidate template diff
  - candidate summary
  - candidate eval report
  - old-template vs candidate-template preview comparisons when available
  - issue thread and optional PR.
- Keep most human decisions in one canonical GitHub issue. Use PRs for candidate
  file diffs and preview artifacts when useful, and link PR and issue both ways.
- If GitHub Pages previews are requested, write clear metadata/links to the
  review issue. Do not assume private Pages availability.
- Add `train continue` behavior for promote/reject decisions:
  - promote requires explicit `--promote <candidate-version>` or parsed human
    decision.
  - reject requires a reason.
  - after promotion, ask whether to stop or continue.
- Starting the next iteration must use the promoted candidate version as the
  base template snapshot.

Acceptance criteria:

- Candidate review is visible in GitHub with enough context for a human to
  decide.
- Promotion and rejection reuse existing candidate commands/state rather than
  duplicating template-version logic.
- A promoted candidate becomes the base for the next iteration.
- Rejected candidates record a reason and cannot silently become current.
- `train status` clearly says whether the session is waiting for candidate
  review, promotion, rejection, or the next iteration decision.

Tests/checks:

- `go test ./internal/cli -run SkillOpt`
- `go test ./internal/feedback ./internal/github ./internal/skillopt`
- Fake GitHub client tests for issue/PR linking.
- CLI smoke with imported fake candidate package and promote/reject paths.
- `go test ./...`
- `git diff --check`
- `codex exec review --uncommitted`

Suggested branch:

- `task/skillopt-train-candidate-review`

Suggested commit message:

- `feat(skillopt): add train candidate review flow`

### Task 8: Update Docs, Skill References, And End-To-End Smoke Tests

Scope:

- Update CLI help and docs:
  - `docs/skillopt-exchange-contract.md`
  - `website/docs/reference/skillopt-exchange-contract.md`
  - `skills/gitmoot/references/CLI.md`
  - `skills/gitmoot/references/WORKFLOWS.md`
  - `skills/gitmoot/SKILL.md`
  - relevant website docs or release notes.
- Add a train-mode workflow doc that explains:
  - when to use low-level `skillopt review` commands
  - when to use high-level `skillopt train`
  - how sessions/iterations work
  - workspace repo vs preview repo
  - ranked review format and optional quality fields
  - candidate promotion and stopping/continuing.
- Add an operational smoke script or documented smoke command that runs with a
  fake optimizer and fake/sandboxed GitHub where possible:
  - create local template
  - start train session
  - add/validate items
  - generate fake options
  - publish or export review packet
  - import feedback
  - run fake optimizer
  - import fake candidate
  - publish candidate review
  - promote or reject.
- Update plugin/skill generated assets if the repo requires it.
- Add release-note guidance for the new command after implementation.

Acceptance criteria:

- A new user can understand `gitmoot skillopt train start/status/continue/stop`
  from docs and skill references.
- Docs clearly state that low-level commands are for advanced/debug use.
- Smoke test demonstrates that train mode enforces the optimizer/candidate gate
  and prevents manual append-style next iterations.
- Website docs build or static docs checks pass if touched.

Tests/checks:

- `go test ./...`
- Run any docs build or `website` lint/build command used by the repo if docs
  generation is touched.
- Run the train smoke test with fake optimizer/runtime.
- `git diff --check`
- `codex exec review --uncommitted`

Suggested branch:

- `task/skillopt-train-docs-smoke`

Suggested commit message:

- `docs: document skillopt train workflow`
