package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/pipeline"
	"github.com/jerryfane/gitmoot/internal/runtime"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// The #681 pipeline RUN engine: create a run, enqueue ready stages, and advance it
// by a scan (mirroring runHeartbeatScanOnce). The advancer is decision-based — it
// folds each settled stage job by its gitmoot_result DECISION, never jobs.state,
// because changes_requested is a SUCCEEDED job that must NOT advance (the
// stateForDecision trap, workflow/result.go). Stage jobs are ordinary queued jobs
// run through the SHELL runtime via a per-job runtime override; the normal worker
// tick claims + runs them, and this advancer folds the results. The daemon-loop
// wiring of runPipelineScanOnce lands in a later step — here it is driven by
// `pipeline run` and the tests.

// pipelineStageEnqueuer enqueues one pipeline stage job. In production it wraps
// workflow.Mailbox.Enqueue (matching newHeartbeatEnqueuer); tests inject a fake to
// assert the request shape without a real worker.
type pipelineStageEnqueuer func(ctx context.Context, request workflow.JobRequest) (db.Job, error)

// newPipelineStageEnqueuer builds the production enqueuer: a Mailbox bound to the
// store and the daemon's canary-routing policy, so a pipeline stage job is
// indistinguishable from a normal background job once enqueued (the runner agent
// carries no template, so canary never actually samples).
func newPipelineStageEnqueuer(store *db.Store, home string) pipelineStageEnqueuer {
	mailbox := workflow.Mailbox{Store: store, CanaryEnabled: canaryRoutingEnabled(home)}
	return func(ctx context.Context, request workflow.JobRequest) (db.Job, error) {
		return mailbox.Enqueue(ctx, request)
	}
}

// pipelineRunID derives a deterministic-per-invocation run id: a recognizable
// "prun-" marker, the pipeline name (a validated name-safe token), and the
// nanosecond clock so distinct runs never collide. The marker also lets
// `pipeline show` tell a run id from a pipeline name.
func pipelineRunID(pipelineName string, now time.Time) string {
	return fmt.Sprintf("prun-%s-%x", strings.TrimSpace(pipelineName), now.UTC().UnixNano())
}

// pipelineStageJobID is the DETERMINISTIC stage job id: run id + stage id +
// attempt. It is deterministic so a re-scan re-derives the same id and the
// idempotent enqueue (enqueuePipelineStageJob) never double-creates a job; the
// attempt suffix mints a fresh id for each retry.
func pipelineStageJobID(runID, stageID string, attempt int) string {
	return fmt.Sprintf("%s-%s-a%d", runID, stageID, attempt)
}

// pipelineStageFingerprint is the stage job fingerprint `pipeline:<name>:<run>:
// <stage>:<attempt>` (decision 2), unique per stage attempt.
func pipelineStageFingerprint(pipelineName, runID, stageID string, attempt int) string {
	return fmt.Sprintf("pipeline:%s:%s:%s:%d", pipelineName, runID, stageID, attempt)
}

// pipelineStageJobRequest builds the JobRequest for one stage attempt. The stage
// command travels in RuntimeOverrideRef (the shell runtime runs it verbatim as
// `sh -c <cmd> gitmoot <prompt>`), NOT on the runner agent's runtime_ref, so one
// runner serves every stage. ParentJobID and DelegationID stay EMPTY (any
// ParentJobID would enter delegation advancement, engine.go); RootJobID is the run
// id so all of a run's stage jobs share a root. The per-stage timeout is plumbed
// as JobTimeout.
func pipelineStageJobRequest(rec db.Pipeline, stage pipeline.Stage, run db.PipelineRun, attempt int) workflow.JobRequest {
	return workflow.JobRequest{
		ID:                 pipelineStageJobID(run.ID, stage.ID, attempt),
		Agent:              pipelineRunnerAgentName(rec.Name),
		Action:             "ask",
		Repo:               rec.Repo,
		Sender:             workflow.PipelineJobSender,
		Instructions:       fmt.Sprintf("pipeline %s run %s stage %s", rec.Name, run.ID, stage.ID),
		Fingerprint:        pipelineStageFingerprint(rec.Name, run.ID, stage.ID, attempt),
		RootJobID:          run.ID,
		JobTimeout:         stage.Timeout,
		RuntimeOverride:    runtime.ShellRuntime,
		RuntimeOverrideRef: stage.Cmd,
	}
}

// enqueuePipelineStageJob enqueues a stage job idempotently: on an enqueue error
// (e.g. a duplicate id from a re-scan that raced the stage-row write) it adopts an
// already-created job with the same deterministic id, mirroring the engine's
// enqueue idiom (engine.go). A genuinely new error with no matching job propagates.
func enqueuePipelineStageJob(ctx context.Context, store *db.Store, enqueue pipelineStageEnqueuer, request workflow.JobRequest) (db.Job, error) {
	job, err := enqueue(ctx, request)
	if err == nil {
		return job, nil
	}
	if existing, getErr := store.GetJob(ctx, request.ID); getErr == nil {
		return existing, nil
	}
	return db.Job{}, err
}

// createPipelineRun creates a fresh manual/scheduled run: the pipeline_runs row
// (snapshotting the pipeline's current spec_hash) plus one pending
// pipeline_run_stages row per spec stage, and records the run on the pipeline's
// last-run bookkeeping. It does NOT enqueue anything — the caller advances the run
// once to enqueue the ready root stages, so creation and the first advance are the
// same idempotent code path a re-scan uses.
func createPipelineRun(ctx context.Context, store *db.Store, rec db.Pipeline, spec pipeline.Spec, trigger string, now time.Time) (db.PipelineRun, error) {
	run := db.PipelineRun{
		ID:        pipelineRunID(rec.Name, now),
		Pipeline:  rec.Name,
		Trigger:   trigger,
		SpecHash:  rec.SpecHash,
		State:     pipeline.RunRunning,
		StartedAt: now.UTC(),
	}
	if err := store.CreatePipelineRun(ctx, run); err != nil {
		return db.PipelineRun{}, err
	}
	for _, stage := range spec.Stages {
		if err := store.CreatePipelineRunStage(ctx, db.PipelineRunStage{
			RunID:   run.ID,
			StageID: stage.ID,
			State:   pipeline.StagePending,
		}); err != nil {
			return db.PipelineRun{}, err
		}
	}
	if err := store.UpdatePipelineLastRun(ctx, rec.Name, run.ID, pipeline.RunRunning, now.UTC()); err != nil {
		return db.PipelineRun{}, err
	}
	return run, nil
}

// runPipelineScanOnce advances every in-flight pipeline run once (#681). It is the
// scan entry point wired next to runHeartbeatScanOnce; the daemon supervisor-loop
// wiring lands in a later step. Only state='running' runs are advanced, so parked
// (blocked/failed) and terminal runs consume zero compute. A per-run error is
// collected (first wins) but never stops the remaining runs. Runs whose pipeline
// was removed, whose stored spec no longer parses, or whose spec drifted (hash no
// longer matches the run's snapshot) are skipped rather than executed against a
// changed spec.
func runPipelineScanOnce(ctx context.Context, store *db.Store, enqueue pipelineStageEnqueuer, now time.Time) error {
	runs, err := store.ListActivePipelineRuns(ctx)
	if err != nil {
		return err
	}
	now = now.UTC()
	var firstErr error
	for _, run := range runs {
		rec, ok, err := store.GetPipeline(ctx, run.Pipeline)
		if err != nil {
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if !ok {
			continue
		}
		spec, err := pipeline.Load([]byte(rec.SpecYAML))
		if err != nil {
			continue
		}
		// A run executes its SNAPSHOT: if the pipeline's spec was edited since the run
		// was created, its hash no longer matches and we do NOT advance against the
		// changed DAG/commands. Parking/repairing spec drift is a follow-up; skipping
		// is safe (the run stays running and is left untouched).
		if strings.TrimSpace(rec.SpecHash) != strings.TrimSpace(run.SpecHash) {
			continue
		}
		if _, err := advancePipelineRun(ctx, store, enqueue, rec, spec, run, now); err != nil && firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// advancePipelineRun is the per-run advancer (decision 6). It is a single
// idempotent pass: FOLD every settled stage job into its stage row by DECISION,
// ENQUEUE newly-ready stages (unless the run is halting on a block/fail), then, if
// nothing is in flight, SETTLE the run — succeeded, or parked blocked/failed with
// the halt stage + reason + (for blocked) the aggregated needs persisted verbatim.
// It writes only rows that actually change, so a re-scan on an unchanged run makes
// no writes and no enqueues. It assumes run.State == running; a parked/terminal run
// is a no-op. The updated run is returned for callers/tests.
func advancePipelineRun(ctx context.Context, store *db.Store, enqueue pipelineStageEnqueuer, rec db.Pipeline, spec pipeline.Spec, run db.PipelineRun, now time.Time) (db.PipelineRun, error) {
	now = now.UTC()
	if run.State != pipeline.RunRunning {
		return run, nil
	}
	specByID := make(map[string]pipeline.Stage, len(spec.Stages))
	for _, stage := range spec.Stages {
		specByID[stage.ID] = stage
	}
	rows, err := store.ListPipelineRunStages(ctx, run.ID)
	if err != nil {
		return run, err
	}
	byID := make(map[string]db.PipelineRunStage, len(rows))
	for _, row := range rows {
		byID[row.StageID] = row
	}

	// --- FOLD: settle stage jobs by decision ---------------------------------
	for _, stage := range spec.Stages {
		row, ok := byID[stage.ID]
		if !ok {
			continue
		}
		if row.State != pipeline.StageQueued && row.State != pipeline.StageRunning {
			continue
		}
		if strings.TrimSpace(row.JobID) == "" {
			continue
		}
		job, err := store.GetJob(ctx, row.JobID)
		if err != nil {
			return run, err
		}
		if !workflow.IsSettledJobState(job.State) {
			// Not terminal yet: reflect a queued->running transition so the funnel
			// tracks the live job, but otherwise leave it in flight.
			if job.State == string(workflow.JobRunning) && row.State == pipeline.StageQueued {
				row.State = pipeline.StageRunning
				if err := persistPipelineStage(ctx, store, byID[stage.ID], row); err != nil {
					return run, err
				}
				byID[stage.ID] = row
			}
			continue
		}
		state, summary, needs := foldPipelineStageOutcome(spec.EffectiveSuccessDecisions(stage), job)
		if state == pipeline.StageFailed && row.Attempt < stage.Retry {
			// Retry budget remains: bump the attempt and reset to pending so the
			// enqueue phase re-launches it under a fresh deterministic id/fingerprint.
			row.Attempt++
			row.State = pipeline.StagePending
			row.JobID = ""
			row.Summary = summary
			row.NeedsJSON = ""
			row.FinishedAt = time.Time{}
		} else {
			row.State = state
			row.Summary = summary
			row.NeedsJSON = marshalPipelineNeeds(needs)
			row.FinishedAt = now
		}
		if err := persistPipelineStage(ctx, store, byID[stage.ID], row); err != nil {
			return run, err
		}
		byID[stage.ID] = row
	}

	// --- ENQUEUE: launch newly-ready stages ----------------------------------
	// Once any stage is blocked or failed the run is halting: stop launching new
	// work and let the in-flight stages drain, then park.
	halting := anyPipelineStageInState(byID, pipeline.StageBlocked, pipeline.StageFailed)
	if !halting {
		for _, stage := range spec.Stages {
			row := byID[stage.ID]
			if row.State != pipeline.StagePending || !pipelineStageDepsSucceeded(stage, byID) {
				continue
			}
			job, err := enqueuePipelineStageJob(ctx, store, enqueue, pipelineStageJobRequest(rec, stage, run, row.Attempt))
			if err != nil {
				return run, err
			}
			row.State = pipeline.StageQueued
			row.JobID = job.ID
			row.StartedAt = now
			if err := persistPipelineStage(ctx, store, byID[stage.ID], row); err != nil {
				return run, err
			}
			byID[stage.ID] = row
		}
	}

	// --- SETTLE: park or finish once nothing is in flight --------------------
	if anyPipelineStageInState(byID, pipeline.StageQueued, pipeline.StageRunning) {
		return run, nil
	}

	if halting {
		// Nothing more can run: every still-pending stage is unreachable (a dep chain
		// is broken by a blocked/failed stage), so mark them skipped for a clean
		// terminal picture — this is also why a blocked run's downstream stages are
		// never enqueued.
		for _, stage := range spec.Stages {
			row := byID[stage.ID]
			if row.State == pipeline.StagePending {
				row.State = pipeline.StageSkipped
				row.FinishedAt = now
				if err := persistPipelineStage(ctx, store, byID[stage.ID], row); err != nil {
					return run, err
				}
				byID[stage.ID] = row
			}
		}
	}

	settled := computePipelineRunSettlement(spec, byID, now)
	return applyPipelineRunSettlement(ctx, store, rec, run, settled)
}

// pipelineRunSettlement is the computed run-level outcome of an advance pass once
// nothing is in flight: the terminal state plus the halt stage/reason/needs.
type pipelineRunSettlement struct {
	State      string
	HaltStage  string
	HaltReason string
	NeedsJSON  string
	FinishedAt time.Time
}

// computePipelineRunSettlement derives the run's terminal state from the settled
// stage rows (pure). Failed takes precedence over blocked (a hard failure is the
// more urgent halt); otherwise blocked parks with the aggregated needs; otherwise
// every stage is succeeded/skipped and the run succeeded. The halt stage is the
// first blocked/failed stage in spec (topological) order for a stable funnel.
func computePipelineRunSettlement(spec pipeline.Spec, byID map[string]db.PipelineRunStage, now time.Time) pipelineRunSettlement {
	if stage, ok := firstPipelineStageInState(spec, byID, pipeline.StageFailed); ok {
		return pipelineRunSettlement{
			State:      pipeline.RunFailed,
			HaltStage:  stage.StageID,
			HaltReason: stage.Summary,
			FinishedAt: now,
		}
	}
	if stage, ok := firstPipelineStageInState(spec, byID, pipeline.StageBlocked); ok {
		return pipelineRunSettlement{
			State:      pipeline.RunBlocked,
			HaltStage:  stage.StageID,
			HaltReason: stage.Summary,
			NeedsJSON:  marshalPipelineNeeds(aggregatePipelineBlockedNeeds(spec, byID)),
			FinishedAt: now,
		}
	}
	return pipelineRunSettlement{State: pipeline.RunSucceeded, FinishedAt: now}
}

// applyPipelineRunSettlement persists the run's terminal state (only when it
// actually changed) and mirrors it onto the pipeline's last_status.
func applyPipelineRunSettlement(ctx context.Context, store *db.Store, rec db.Pipeline, run db.PipelineRun, settled pipelineRunSettlement) (db.PipelineRun, error) {
	updated := run
	updated.State = settled.State
	updated.HaltStage = settled.HaltStage
	updated.HaltReason = settled.HaltReason
	updated.NeedsJSON = settled.NeedsJSON
	updated.FinishedAt = settled.FinishedAt
	if pipelineRunEqual(run, updated) {
		return run, nil
	}
	if err := store.UpdatePipelineRun(ctx, updated); err != nil {
		return run, err
	}
	if err := store.UpdatePipelineLastRun(ctx, rec.Name, run.ID, updated.State, time.Time{}); err != nil {
		return run, err
	}
	return updated, nil
}

// foldPipelineStageOutcome maps a settled stage job to a stage outcome BY DECISION
// (decision 6). It reads payload.Result.Decision, never jobs.state: a
// changes_requested job is SUCCEEDED but must NOT advance, so any decision outside
// the stage's success_decisions (and not "blocked") is a failure. A cancelled job
// or a job with no result (errored before parse / unparseable) is a failure too.
func foldPipelineStageOutcome(successDecisions []string, job db.Job) (state, summary string, needs []string) {
	if job.State == string(workflow.JobCancelled) {
		return pipeline.StageFailed, "stage job cancelled", nil
	}
	payload, err := workflow.ParseJobPayload(job.Payload)
	if err != nil || payload.Result == nil {
		return pipeline.StageFailed, "stage job produced no gitmoot_result", nil
	}
	decision := strings.TrimSpace(payload.Result.Decision)
	summary = strings.TrimSpace(payload.Result.Summary)
	switch {
	case containsPipelineDecision(successDecisions, decision):
		return pipeline.StageSucceeded, summary, nil
	case decision == "blocked":
		if summary == "" {
			summary = "stage blocked"
		}
		return pipeline.StageBlocked, summary, append([]string(nil), payload.Result.Needs...)
	default:
		if summary == "" {
			summary = fmt.Sprintf("stage returned decision %q", decision)
		}
		return pipeline.StageFailed, summary, nil
	}
}

// pipelineStageDepsSucceeded reports whether every stage this one needs has
// reached StageSucceeded (so it is ready to enqueue). A missing dep row is treated
// as not-succeeded (defensive; validation already rejects unknown needs).
func pipelineStageDepsSucceeded(stage pipeline.Stage, byID map[string]db.PipelineRunStage) bool {
	for _, dep := range stage.Needs {
		if dep == "" {
			continue
		}
		if byID[dep].State != pipeline.StageSucceeded {
			return false
		}
	}
	return true
}

func anyPipelineStageInState(byID map[string]db.PipelineRunStage, states ...string) bool {
	for _, row := range byID {
		for _, want := range states {
			if row.State == want {
				return true
			}
		}
	}
	return false
}

// firstPipelineStageInState returns the first stage row (in spec/topological
// order) that is in the wanted state, for a stable halt-stage / funnel ordering.
func firstPipelineStageInState(spec pipeline.Spec, byID map[string]db.PipelineRunStage, want string) (db.PipelineRunStage, bool) {
	for _, stage := range spec.Stages {
		if row, ok := byID[stage.ID]; ok && row.State == want {
			return row, true
		}
	}
	return db.PipelineRunStage{}, false
}

// aggregatePipelineBlockedNeeds collects the persisted needs of every blocked
// stage, in spec order, deduped, so the run-level needs_json is the union of what
// every parked stage is waiting on.
func aggregatePipelineBlockedNeeds(spec pipeline.Spec, byID map[string]db.PipelineRunStage) []string {
	seen := make(map[string]struct{})
	var needs []string
	for _, stage := range spec.Stages {
		row, ok := byID[stage.ID]
		if !ok || row.State != pipeline.StageBlocked {
			continue
		}
		for _, need := range decodePipelineNeeds(row.NeedsJSON) {
			if _, dup := seen[need]; dup {
				continue
			}
			seen[need] = struct{}{}
			needs = append(needs, need)
		}
	}
	return needs
}

// persistPipelineStage writes a stage row only when a field the advancer owns
// actually changed, keeping a re-scan write-free.
func persistPipelineStage(ctx context.Context, store *db.Store, old, updated db.PipelineRunStage) error {
	if pipelineStageEqual(old, updated) {
		return nil
	}
	return store.UpdatePipelineRunStage(ctx, updated)
}

func pipelineStageEqual(a, b db.PipelineRunStage) bool {
	return a.State == b.State && a.JobID == b.JobID && a.Attempt == b.Attempt &&
		a.NeedsJSON == b.NeedsJSON && a.Summary == b.Summary &&
		a.StartedAt.Equal(b.StartedAt) && a.FinishedAt.Equal(b.FinishedAt)
}

func pipelineRunEqual(a, b db.PipelineRun) bool {
	return a.State == b.State && a.HaltStage == b.HaltStage && a.HaltReason == b.HaltReason &&
		a.NeedsJSON == b.NeedsJSON && a.FinishedAt.Equal(b.FinishedAt)
}

func containsPipelineDecision(decisions []string, decision string) bool {
	for _, d := range decisions {
		if d == decision {
			return true
		}
	}
	return false
}

// marshalPipelineNeeds encodes a needs slice as a compact JSON array for
// needs_json (empty slice => empty string, so no needs stores as "").
func marshalPipelineNeeds(needs []string) string {
	needs = compactPipelineNeeds(needs)
	if len(needs) == 0 {
		return ""
	}
	encoded, err := json.Marshal(needs)
	if err != nil {
		return ""
	}
	return string(encoded)
}

// decodePipelineNeeds parses a needs_json array back into a slice; a blank or
// malformed value decodes to no needs.
func decodePipelineNeeds(value string) []string {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	var needs []string
	if err := json.Unmarshal([]byte(value), &needs); err != nil {
		return nil
	}
	return compactPipelineNeeds(needs)
}

func compactPipelineNeeds(needs []string) []string {
	out := make([]string, 0, len(needs))
	for _, need := range needs {
		if trimmed := strings.TrimSpace(need); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}
