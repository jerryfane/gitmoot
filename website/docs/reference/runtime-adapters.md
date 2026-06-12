# Runtime Adapters

Runtime adapters keep Gitmoot workflow logic independent from Codex, Claude
Code, shell commands, and future runtimes. Gitmoot snapshots the agent template
and rendered job prompt before handing work to an adapter.

## Current Runtimes

- **Codex** starts and resumes sessions through the Codex CLI noninteractive
  commands. Prefer explicit session ids for long-running agents.
- **Claude Code** uses Claude CLI print/resume style commands when available.
  Restart the daemon or runtime session after changing token environment.
- **Shell** invokes a configured shell command and is mainly for smoke tests,
  demos, and adapter contract checks.

## Agent Session Values

`RuntimeRef` is runtime-specific:

- Codex accepts a session UUID, thread name, or `last`.
- Claude accepts a UUID or `last`.
- Shell uses the configured command.

Prefer explicit runtime session ids over `last` for durable agents. Use
`gitmoot agent doctor <name>` after subscribing or starting an agent.

## Runtime Safety

Adapters should pass the rendered Gitmoot prompt through without rewriting
workflow semantics. Gitmoot parses the returned `gitmoot_result` object after
delivery and keeps raw output for diagnostics.

Use the plugin docs for runtime discovery setup:
[Codex And Claude Plugins](../plugins/codex-claude.md). Use troubleshooting
when session validation or resume fails:
[Troubleshooting](../operations/troubleshooting.md).

The full adapter authoring reference lives in
[`docs/adapters.md`](https://github.com/jerryfane/gitmoot/blob/main/docs/adapters.md).
