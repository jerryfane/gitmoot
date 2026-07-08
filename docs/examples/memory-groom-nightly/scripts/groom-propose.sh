#!/usr/bin/env sh
# Nightly memory-groom proposal stage (#737 P4.2).
#
# Runs `gitmoot memory groom --propose` (which NEVER mutates memory), then pings
# the owner via agentgram when the plan has anything to review. It prints the
# gitmoot_result the pipeline advancer folds on: `approved` whenever propose
# succeeds (whether or not there are proposals), `failed` only if propose errored.
#
# The owner reviews the written plan and applies it explicitly:
#     gitmoot memory groom --yes --plan <plan.json>
#
# Nothing here is destructive; there is no `--yes` in this script by design.
#
# Requires: `gitmoot` on PATH, `jq` for the count, and (optionally) `agentgram`
# for the notification. Override the plans dir with GROOM_PLANS_DIR.
set -eu

PLANS_DIR="${GROOM_PLANS_DIR:-${HOME}/evals/groom}"
mkdir -p "${PLANS_DIR}"
# A per-day filename keeps nightly runs from piling up while staying deterministic
# within a day (a re-run overwrites the same plan). `groom --propose`'s own default
# name is snapshot-derived; an explicit --out just pins the location.
PLAN="${PLANS_DIR}/groom-$(date -u +%Y%m%d).json"

emit() {
  # $1 decision, $2 summary
  printf '%s' "{\"gitmoot_result\":{\"decision\":\"$1\",\"summary\":\"$2\"}}"
}

if ! gitmoot memory groom --propose --out "${PLAN}" >/dev/null 2>&1; then
  emit failed "gitmoot memory groom --propose failed"
  exit 0
fi

COUNT=$(jq -r '.stats.proposed_retirements // 0' "${PLAN}" 2>/dev/null || echo 0)
FLAGS=$(jq -r '.stats.rewrite_flags // 0' "${PLAN}" 2>/dev/null || echo 0)

if [ "${COUNT}" -gt 0 ] || [ "${FLAGS}" -gt 0 ]; then
  # Notify only when there is something to review; best-effort (never fail the stage
  # on a notification hiccup).
  if command -v agentgram >/dev/null 2>&1; then
    agentgram send "gitmoot memory groom: ${COUNT} retirement(s) + ${FLAGS} rewrite flag(s) proposed. Review ${PLAN}, then apply: gitmoot memory groom --yes --plan ${PLAN}" >/dev/null 2>&1 || true
  fi
fi

emit approved "proposed ${COUNT} retirement(s), ${FLAGS} rewrite flag(s); plan at ${PLAN}"
