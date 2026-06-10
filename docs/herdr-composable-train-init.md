# Running the train-init wizard in a Herdr pane

[Herdr](https://github.com/jerryfane/herdr) is a terminal workspace manager for
AI coding agents. Gitmoot's `skillopt train init` wizard works well inside a
visible Herdr pane, but **Herdr is an optional frontend, not a dependency**:
Gitmoot imports no Herdr code and never calls Herdr. The integration is purely
compositional — it uses Herdr's standard pane commands plus Gitmoot's existing
process, stdin/stdout, and `gitmoot interactive ... --json` surfaces.

## Why it composes

The interactive wizard (see [skillopt-train-workflow.md](./skillopt-train-workflow.md))
publishes each question as an interactive prompt record and blocks until that
question is answered on the pane's stdin **or** resolved externally with
`gitmoot interactive answer`. That second path is what makes Herdr optional: an
agent can run the wizard in a pane for a human to watch, yet answer the current
question from its own session without typing into the pane at all.

## Pane-based flow

Run the wizard in a new pane that the human can see, and drive it from the
agent's session:

```sh
# 1. Split a visible pane next to the current one and start the wizard there.
PANE=$(herdr pane split "$HERDR_PANE_ID" --direction right --cwd "$REPO" --no-focus)
herdr pane send-text "$PANE" 'gitmoot skillopt train init'
herdr pane send-keys "$PANE" Enter

# 2. The wizard now waits on its first question. Inspect the pending prompt
#    from the agent's own session (no pane scraping needed).
gitmoot interactive list --state pending --json
gitmoot interactive show <prompt-id> --json   # question + choices for the human

# 3. Relay the question to the human, then answer on their behalf. The wizard,
#    running in the pane, unblocks and renders the next question.
gitmoot interactive answer <prompt-id> <value> --source agent

# 4. Repeat list/show/answer until the wizard prints the scaffold summary, then
#    optionally read the pane to confirm and close it.
herdr pane read "$PANE" --source recent --lines 40
herdr pane close "$PANE"
```

Notes:

- The human can also type answers directly into the pane (`herdr pane send-text`
  / `send-keys`); stdin and `interactive answer` are interchangeable for the
  same question.
- `herdr pane read` is only for relaying/confirming output to a human — answers
  flow through `gitmoot interactive answer`, never by parsing pane text.

## Pure CLI fallback (no Herdr)

Nothing above needs Herdr. The same flow works in any terminal, and an agent
that cannot open a pane can answer everything in one asynchronous pass:

```sh
gitmoot skillopt train init --prompts          # emit all prompt records, then exit
gitmoot interactive list --state pending --json
gitmoot interactive answer <prompt-id> <value> --source agent   # for each field
gitmoot skillopt train init                     # rerun; applies the answers
```

An optional smoke script, [`scripts/herdr-train-init-smoke.sh`](../scripts/herdr-train-init-smoke.sh),
exercises the pane flow when `herdr` is on `PATH` and skips cleanly otherwise, so
it never fails a normal checkout that has no Herdr installed.
