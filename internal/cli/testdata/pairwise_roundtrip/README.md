# Cross-repo pairwise round-trip fixture (#507/#508)

This directory holds a **fork-GENERATED** fixture for the cross-repo pairwise
round-trip E2E (`internal/cli/skillopt_pairwise_roundtrip_test.go`). Its purpose
is to catch a contract-shape mismatch between:

- the **producer** — the `gitmoot-skillopt` fork's live-pairwise emission (#507,
  `gitmoot_skillopt/pairwise.py:run_pairwise_eval`), which writes a blinded human
  review packet plus a SEPARATE secret unblinding map; and
- the **consumer** — the Go importer (#508,
  `gitmoot skillopt pairwise import`), which reads that packet + secret map +
  reviewer picks and unblinds each pick back to champion/challenger.

The two were built against an *assumed* shared shape on each side and never run
as one real round-trip. This fixture closes that gap.

## Files

| file | origin |
| --- | --- |
| `pairwise-review.json` | **fork-emitted** blinded review packet (verbatim) |
| `pairwise-secret-map.json` | **fork-emitted** secret unblinding map (only the volatile absolute temp-path *root* of the admin/debug trace paths is normalized to `/FIXTURE`; the Go importer never reads those fields) |
| `pairwise-picks.json` | authored reviewer picks (A/B labels only — the fork does not emit picks; they are the human's review input) |
| `expected.json` | ground-truth expected unblind outcome, computed from the fork's REAL secret map, used by the Go test to assert correct unblinding |

**The packet and secret map are NOT hand-authored.** They are produced by
executing the fork's real emission code. Hand-crafting JSON to match the Go
parser would be circular and would defeat the entire purpose of this E2E.

## Regenerating

Refresh the fixture whenever the fork's packet/secret-map shape changes:

```sh
cd /root/gitmoot-skillopt && pip install -e ".[dev]"   # first time / if deps missing
python3 internal/cli/testdata/pairwise_roundtrip/regen.py
```

`regen.py` stubs the live rollout, so it runs fully **offline** (no LLM, no
network) and is deterministic (the fork seeds A/B placement from a fixed
seed + run_id). CI never runs `regen.py`: the committed fixture means the Go
round-trip test runs with Go only, no Python required.
