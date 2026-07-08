# Nightly memory-groom proposal pipeline (#737 P4.2)

A ready-to-register [pipeline](../../pipelines.md) that runs the deterministic
memory groomer every night and pings the owner when there is something to review.
It is **proposal-only and human-gated**: it never mutates confirmed memory. The
detectors write a reviewable plan artifact; a human applies it explicitly.

This mirrors the existing memory-ingest sweep pattern — a nightly job that
proposes and notifies, with the destructive step held behind an owner `--yes`.

## What it does

Each night the single `propose` stage runs:

```sh
gitmoot memory groom --propose --out <plans-dir>/groom-<YYYYMMDD>.json
```

`groom --propose` reads every **active** confirmed memory, computes the current
vault `snapshot_hash` (the same anchor `vault export`/`import` use), runs the
deterministic detectors, and writes a plan of:

- **proposed retirements** — status/changelog/ToC snapshot notes, bare to-do
  lists, and exact-duplicate content (keeping the lowest id of each duplicate set);
- **rewrite flags** — over-long multi-fact "bricks" are *flagged for owner review*,
  never retired or rewritten by this track (LLM rewriting is the follow-up P4.3).

When the plan is non-empty the stage pings the owner via `agentgram`. **Nothing is
retired until the owner reviews the plan and runs:**

```sh
gitmoot memory groom --yes --plan <plans-dir>/groom-<YYYYMMDD>.json
```

`--yes` recomputes the `snapshot_hash` and **aborts as stale** if the memory store
changed since the proposal — so a vault edit between propose and apply can never
retire the wrong revision.

## Register it

1. Copy `scripts/groom-propose.sh` somewhere the pipeline's stage cwd can reach
   (the stage runs `sh -c '<cmd>'` from a worktree of the pipeline's `repo`), and
   adjust the `cmd` in `groom-nightly.yaml` to point at it. Requirements on the
   host: `gitmoot` on `PATH`, `jq`, and (optionally) `agentgram` for the ping.

2. Set `repo:` in `groom-nightly.yaml` to your gitmoot management repo. A
   scheduled pipeline needs a managed repo for the worker to claim its stage jobs;
   groom itself operates on the whole local memory store regardless of which repo
   the stage runs against.

3. Register and enable it:

   ```sh
   gitmoot pipeline add groom-nightly.yaml --enable
   gitmoot pipeline list
   ```

The spec is stored **verbatim** at `add` time; later edits to the file don't affect
an in-flight run. Disable with `gitmoot pipeline disable memory-groom-nightly`.

## Files

- `groom-nightly.yaml` — the pipeline spec (24h schedule, one proposal stage).
- `scripts/groom-propose.sh` — the stage command: propose, notify-on-nonempty,
  emit the `gitmoot_result` the advancer folds on.
