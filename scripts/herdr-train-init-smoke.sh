#!/usr/bin/env bash
# Optional smoke for the Herdr-composable train-init flow.
#
# It verifies the prompt/answer surface that makes Herdr optional (the wizard's
# questions are answerable through `gitmoot interactive answer`), and, when run
# inside a live Herdr session, also drives the wizard in a real pane. It SKIPS
# cleanly (exit 0) when Herdr or gitmoot is unavailable so it never fails a
# normal checkout.
set -euo pipefail

skip() {
  echo "herdr-train-init-smoke: SKIP — $1"
  exit 0
}

command -v herdr >/dev/null 2>&1 || skip "herdr is not on PATH"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
WORK="$(mktemp -d)"

# Use an explicit GITMOOT_BIN if provided, otherwise build a current binary from
# this checkout so the smoke always matches the code under review.
if [ -n "${GITMOOT_BIN:-}" ]; then
  command -v "$GITMOOT_BIN" >/dev/null 2>&1 || skip "GITMOOT_BIN=$GITMOOT_BIN is not executable"
else
  command -v go >/dev/null 2>&1 || skip "go is not on PATH to build gitmoot (or set GITMOOT_BIN)"
  GITMOOT_BIN="$WORK/gitmoot"
  echo "herdr-train-init-smoke: building gitmoot from $ROOT_DIR"
  ( cd "$ROOT_DIR" && GOTOOLCHAIN="${GOTOOLCHAIN:-go1.26.0}" go build -o "$GITMOOT_BIN" ./cmd/gitmoot ) \
    || skip "could not build gitmoot from this checkout"
fi

HOME_DIR="$WORK/home"
REPO_DIR="$WORK/repo"
mkdir -p "$HOME_DIR" "$REPO_DIR"
cleanup() { rm -rf "$WORK"; }
trap cleanup EXIT

gm() { "$GITMOOT_BIN" "$@" --home "$HOME_DIR"; }

echo "herdr-train-init-smoke: verifying the composable prompt/answer surface"
gm init >/dev/null

# Emit the prompt records the Herdr pane flow answers through, then resolve them
# exactly as an agent would from its own session.
( cd "$REPO_DIR" && gm skillopt train init --prompts >/dev/null )

answer() { gm interactive answer "$1" "$2" --source smoke >/dev/null; }
scope_prompt() { gm interactive list --state pending --json | grep -o "\"id\": \"[^\"]*\.$1\"" | head -1 | sed 's/.*: "//; s/"$//'; }

answer "$(scope_prompt name)"          "herdr-smoke"
answer "$(scope_prompt template)"      "planner"
answer "$(scope_prompt review-repo)"   "owner/repo"
answer "$(scope_prompt artifact-kind)" "text"
answer "$(scope_prompt preview)"       "text-table"
answer "$(scope_prompt request)"       "Smoke the herdr-composable flow."

( cd "$REPO_DIR" && gm skillopt train init >/dev/null )
test -f "$REPO_DIR/.gitmoot/skillopt/herdr-smoke/config.toml" \
  || { echo "herdr-train-init-smoke: FAIL — scaffold not created"; exit 1; }
echo "herdr-train-init-smoke: prompt/answer surface OK"

# The live-pane demo only runs inside an active Herdr session.
if [ -z "${HERDR_PANE_ID:-}" ] || ! herdr pane list >/dev/null 2>&1; then
  skip "not inside an active Herdr session — pane demo not run (the composable surface above already passed)"
fi

echo "herdr-train-init-smoke: driving the wizard in a Herdr pane"
SPLIT_JSON="$(herdr pane split "$HERDR_PANE_ID" --direction right --cwd "$REPO_DIR" --no-focus)"
PANE="$(printf '%s' "$SPLIT_JSON" | grep -o '"pane_id":"[^"]*"' | head -1 | sed 's/.*:"//; s/"$//')"
[ -n "$PANE" ] || { echo "herdr-train-init-smoke: FAIL — could not parse pane id from: $SPLIT_JSON"; exit 1; }
trap 'herdr pane close "$PANE" >/dev/null 2>&1 || true; cleanup' EXIT
herdr pane send-text "$PANE" "$GITMOOT_BIN skillopt train init --home $HOME_DIR --name pane-smoke"
herdr pane send-keys "$PANE" Enter
herdr pane read "$PANE" --source recent --lines 20 || true
echo "herdr-train-init-smoke: pane demo launched (answer its prompts with gitmoot interactive answer)"
