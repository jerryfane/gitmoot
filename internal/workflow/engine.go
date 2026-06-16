package workflow

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"hash/fnv"
	"strconv"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/runtime"
)

type Engine struct {
	Store                   *db.Store
	RequiredReviewers       []string
	MergeGate               MergeGate
	JobID                   func(JobRequest) string
	PayloadRefresher        func(context.Context, db.Job, JobPayload) (JobPayload, error)
	ImplementationFinalizer ImplementationFinalizer
	// Home is the resolved GITMOOT_HOME root used to place per-delegation
	// worktrees. DelegationWorktrees is the checkout-bound git client that
	// performs the worktree-add. Both are optional: when either is unset, the
	// dispatcher enqueues implement delegations against the shared checkout
	// (legacy behavior) rather than allocating isolated worktrees.
	Home                string
	DelegationWorktrees WorktreeManager
	DelegationCheckout  string
	// ArtifactRoot is the filesystem root under which delegation artifacts
	// (delegations/<parent-job-id>/brief.md and context-manifest.json) are
	// written when a coordinator returns delegations that request artifacts.
	// It is the resolved GITMOOT_HOME root (already ending in .gitmoot), kept
	// outside any repo checkout so generated briefs are never committed. When
	// empty, artifact writing is skipped (ask-path and tests that build an
	// Engine without it keep their existing behavior).
	ArtifactRoot string
}

type PullRequestEvent struct {
	Repo              string
	Branch            string
	PullRequest       int
	HeadSHA           string
	GoalID            string
	TaskID            string
	TaskTitle         string
	LeadAgent         string
	Sender            string
	RequiredReviewers []string
}

type MergeRequest struct {
	Repo           string
	Branch         string
	PullRequest    int
	HeadSHA        string
	TaskID         string
	Reviewer       string
	ReviewOptional bool
}

type MergeDecision struct {
	Ready          bool
	Merged         bool
	MergeCommitSHA string
	Reason         string
}

type MergeGate interface {
	Evaluate(ctx context.Context, request MergeRequest) (MergeDecision, error)
}

type ImplementationFinalizer interface {
	FinalizeImplementation(ctx context.Context, job db.Job, payload JobPayload) (JobPayload, error)
}

type BlockedError struct {
	Reason string
}

func (e BlockedError) Error() string {
	return "workflow blocked: " + e.Reason
}

func (e Engine) HandlePullRequestOpened(ctx context.Context, event PullRequestEvent) error {
	if err := e.validate(); err != nil {
		return err
	}
	if err := validatePullRequestEvent(event); err != nil {
		return err
	}
	ref := taskRefFromPullRequest(event)
	if err := e.setTaskState(ctx, ref, TaskPullRequestOpen); err != nil {
		return err
	}
	if err := e.ensureAgentAllowed(ctx, JobRequest{
		Agent:  event.LeadAgent,
		Action: "implement",
		Repo:   event.Repo,
		Branch: event.Branch,
	}, ref); err != nil {
		return err
	}
	reviewers := compactStrings(append([]string{}, event.RequiredReviewers...))
	if len(reviewers) == 0 {
		reviewers = compactStrings(append([]string{}, e.RequiredReviewers...))
	}
	if len(reviewers) == 0 {
		decision, err := e.runMergeGate(ctx, "", JobPayload{
			Repo:        event.Repo,
			Branch:      event.Branch,
			PullRequest: event.PullRequest,
			HeadSHA:     event.HeadSHA,
			GoalID:      event.GoalID,
			TaskID:      event.TaskID,
			TaskTitle:   event.TaskTitle,
			LeadAgent:   event.LeadAgent,
		}, ref)
		if err != nil {
			return err
		}
		if decision.Merged {
			return nil
		}
		return e.recordPullRequestBaseline(ctx, event)
	}
	reviewRound, err := e.nextReviewRound(ctx, event)
	if err != nil {
		return err
	}
	requests := make([]JobRequest, 0, len(reviewers))
	for _, reviewer := range reviewers {
		request := JobRequest{
			Agent:       reviewer,
			Action:      "review",
			Repo:        event.Repo,
			Branch:      event.Branch,
			PullRequest: event.PullRequest,
			HeadSHA:     event.HeadSHA,
			GoalID:      event.GoalID,
			TaskID:      event.TaskID,
			TaskTitle:   event.TaskTitle,
			LeadAgent:   event.LeadAgent,
			Reviewers:   reviewers,
			ReviewRound: reviewRound,
			Sender:      event.Sender,
			Instructions: fmt.Sprintf(
				"Review pull request #%d for task %s.",
				event.PullRequest,
				taskLabel(event.TaskID, event.TaskTitle),
			),
		}
		requests = append(requests, request)
	}
	for _, request := range requests {
		if err := e.ensureAgentAllowed(ctx, request, ref); err != nil {
			return err
		}
	}
	for _, request := range requests {
		if err := e.enqueue(ctx, request); err != nil {
			return err
		}
	}
	if err := e.setTaskState(ctx, ref, TaskReviewing); err != nil {
		return err
	}
	return e.recordPullRequestBaseline(ctx, event)
}

func (e Engine) HandlePullRequestReadyToMerge(ctx context.Context, event PullRequestEvent) error {
	if err := e.validate(); err != nil {
		return err
	}
	if err := validatePullRequestEvent(event); err != nil {
		return err
	}
	ref := taskRefFromPullRequest(event)
	_, err := e.runMergeGate(ctx, "", JobPayload{
		Repo:        event.Repo,
		Branch:      event.Branch,
		PullRequest: event.PullRequest,
		HeadSHA:     event.HeadSHA,
		GoalID:      event.GoalID,
		TaskID:      event.TaskID,
		TaskTitle:   event.TaskTitle,
		LeadAgent:   event.LeadAgent,
		Reviewers:   compactStrings(append([]string{}, event.RequiredReviewers...)),
	}, ref)
	return err
}

func (e Engine) RunJob(ctx context.Context, jobID string, agent runtime.Agent, adapter DeliveryAdapter) (AgentResult, error) {
	if err := e.validate(); err != nil {
		return AgentResult{}, err
	}
	job, payload, err := e.jobPayload(ctx, jobID)
	if err != nil {
		return AgentResult{}, err
	}
	if err := e.ensureJobExecutorAllowed(ctx, job, payload, taskRefFromPayload(payload)); err != nil {
		return AgentResult{}, err
	}
	result, err := (Mailbox{Store: e.Store}).Run(ctx, jobID, agent, adapter)
	if err != nil {
		return result, err
	}
	if e.PayloadRefresher != nil {
		if err := e.refreshJobPayload(ctx, jobID); err != nil {
			return result, err
		}
	}
	if err := e.AdvanceJob(ctx, jobID); err != nil {
		return result, err
	}
	if err := e.Store.AddJobEvent(ctx, db.JobEvent{JobID: jobID, Kind: "advance_completed", Message: "workflow advancement completed"}); err != nil {
		return result, err
	}
	return result, nil
}

func (e Engine) refreshJobPayload(ctx context.Context, jobID string) error {
	job, payload, err := e.jobPayload(ctx, jobID)
	if err != nil {
		return err
	}
	payload, err = e.PayloadRefresher(ctx, job, payload)
	if err != nil {
		return err
	}
	encoded, err := marshalPayload(payload)
	if err != nil {
		return err
	}
	return e.Store.UpdateJobPayload(ctx, jobID, encoded)
}

func (e Engine) AdvanceJob(ctx context.Context, jobID string) error {
	if err := e.validate(); err != nil {
		return err
	}
	job, payload, err := e.jobPayload(ctx, jobID)
	if err != nil {
		return err
	}
	if payload.Result == nil {
		return fmt.Errorf("job %q has no agent result", jobID)
	}
	ref := taskRefFromPayload(payload)

	// When a delegated child job finishes, advance its parent's delegation DAG
	// before running the child's own advancement: enqueue any now-ready
	// dependent siblings, apply the failed delegation's failure_policy, and
	// enqueue the coordinator continuation job once every top-level sibling is
	// terminal. This runs here (the child's AdvanceJob) because RunJob calls
	// AdvanceJob per finishing child. A child with no parent is unaffected.
	if strings.TrimSpace(payload.ParentJobID) != "" {
		parentJob, parentPayload, err := e.jobPayload(ctx, payload.ParentJobID)
		if err != nil {
			return err
		}
		if parentPayload.Result != nil {
			if err := e.advanceDelegations(ctx, parentJob, parentPayload, parentPayload.Result, taskRefFromPayload(parentPayload)); err != nil {
				return err
			}
			// A child that failed under a continue/escalate failure_policy is
			// handled by the delegation graph (siblings keep running, or the
			// coordinator continuation was enqueued); do not also block the
			// shared parent task via the failed-decision path below.
			if (payload.Result.Decision == "blocked" || payload.Result.Decision == "failed") &&
				delegationFailureHandledByPolicy(parentPayload.Result, payload.DelegationID) {
				return nil
			}
		}
	}

	if job.Type == "review" {
		latest, err := e.latestReviewRound(ctx, payload)
		if err != nil {
			return err
		}
		if latest != "" && strings.TrimSpace(payload.ReviewRound) != latest {
			return nil
		}
	}
	if payload.Result.Decision == "blocked" || payload.Result.Decision == "failed" {
		return e.block(ctx, ref, payload.Result.Summary)
	}
	if job.Type == "review" && payload.Result.Decision == "approved" {
		done, err := e.reviewApprovalAlreadyAdvanced(ctx, ref)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
	}
	if err := e.dispatchDelegations(ctx, job, payload, ref); err != nil {
		return err
	}

	switch job.Type {
	case "implement":
		if payload.Result.Decision != "implemented" {
			return nil
		}
		if e.implementationNeedsFinalizer(ctx, payload) {
			finalized, err := e.ImplementationFinalizer.FinalizeImplementation(ctx, job, payload)
			if err != nil {
				return err
			}
			encoded, err := marshalPayload(finalized)
			if err != nil {
				return err
			}
			if err := e.Store.UpdateJobPayload(ctx, job.ID, encoded); err != nil {
				return err
			}
			payload = finalized
		}
		if payload.PullRequest <= 0 {
			return e.Store.AddJobEvent(ctx, db.JobEvent{JobID: job.ID, Kind: "advance_skipped_no_pr", Message: "no pull request is attached; skipping PR advancement"})
		}
		leadAgent := strings.TrimSpace(payload.LeadAgent)
		if leadAgent == "" {
			leadAgent = job.Agent
			if payload.DelegationReason == "runtime_session_busy" &&
				payload.DelegatedAgent == job.Agent &&
				strings.TrimSpace(payload.OriginalAgent) != "" {
				leadAgent = payload.OriginalAgent
			}
		}
		event := PullRequestEvent{
			Repo:              payload.Repo,
			Branch:            payload.Branch,
			PullRequest:       payload.PullRequest,
			HeadSHA:           payload.HeadSHA,
			GoalID:            payload.GoalID,
			TaskID:            payload.TaskID,
			TaskTitle:         payload.TaskTitle,
			LeadAgent:         leadAgent,
			Sender:            job.Agent,
			RequiredReviewers: e.requiredReviewers(payload),
		}
		return e.HandlePullRequestOpened(ctx, event)
	case "review":
		reviewer := reviewDecisionAgent(job, payload)
		switch payload.Result.Decision {
		case "changes_requested":
			if err := e.setTaskState(ctx, ref, TaskChangesRequested); err != nil {
				return err
			}
			return e.dispatchFix(ctx, reviewer, payload, *payload.Result, ref)
		case "approved":
			ready, err := e.allRequiredReviewersApproved(ctx, reviewer, payload)
			if err != nil {
				return err
			}
			if !ready {
				return e.setReviewingIfNotChangesRequested(ctx, ref)
			}
			_, err = e.runMergeGate(ctx, reviewer, payload, ref)
			return err
		}
	}
	return nil
}

// delegationRequest builds the canonical child JobRequest for a delegation,
// inheriting the parent's repo/branch/PR context and stamping the DAG fields
// (ParentJobID/DelegationID/DelegationDepth/DelegatedBy/Deps). It is shared by
// dispatchDelegations (initial enqueue of ready delegations) and
// advanceDelegations (deferred enqueue once deps clear) so both paths produce
// identical, idempotent requests for the same delegation ID.
func (e Engine) delegationRequest(job db.Job, payload JobPayload, d Delegation) JobRequest {
	return JobRequest{
		ID:              job.ID + "/delegation/" + d.ID,
		Agent:           d.Agent,
		Action:          d.Action,
		Repo:            payload.Repo,
		Branch:          payload.Branch,
		PullRequest:     payload.PullRequest,
		HeadSHA:         payload.HeadSHA,
		GoalID:          payload.GoalID,
		TaskID:          payload.TaskID,
		TaskTitle:       payload.TaskTitle,
		LeadAgent:       payload.LeadAgent,
		Reviewers:       payload.Reviewers,
		ReviewRound:     payload.ReviewRound,
		Sender:          job.Agent,
		Instructions:    strings.TrimSpace(d.Prompt),
		Constraints:     payload.Constraints,
		ParentJobID:     job.ID,
		DelegationID:    d.ID,
		DelegationDepth: payload.DelegationDepth + 1,
		DelegatedBy:     job.Agent,
		Deps:            compactStrings(d.Deps),
	}
}

func (e Engine) dispatchDelegations(ctx context.Context, job db.Job, payload JobPayload, ref taskRef) error {
	if payload.Result == nil || len(payload.Result.Delegations) == 0 {
		return nil
	}

	delegations := payload.Result.Delegations

	// Preflight every delegation (ready and deferred) before enqueueing any
	// child job so a partial failure does not leave the parent half-dispatched
	// and an unknown/uncapable agent on a deferred delegation blocks the parent
	// immediately rather than only when its deps clear. Use a lightweight check
	// that does not acquire branch locks or other execution side effects.
	for _, d := range delegations {
		request := e.delegationRequest(job, payload, d)
		if err := e.preflightDelegation(ctx, request); err != nil {
			return e.block(ctx, ref, fmt.Sprintf("delegation %q preflight failed: %v", request.DelegationID, err))
		}
	}

	// Write the shared coordinator brief and context manifest once, fully,
	// before enqueueing any child job so a child that starts immediately reads a
	// complete directory. Every child of a parent that produced artifacts is
	// pointed at the same directory so its prompt can reference brief.md and
	// context-manifest.json. Writing is skipped when the engine has no artifact
	// root or no delegation requested artifacts.
	artifactDir, err := writeDelegationArtifacts(e.ArtifactRoot, job.ID, payload.Result)
	if err != nil {
		return e.block(ctx, ref, fmt.Sprintf("write delegation artifacts: %v", err))
	}
	if artifactDir != "" {
		_ = e.Store.AddJobEvent(ctx, db.JobEvent{
			JobID:   job.ID,
			Kind:    "delegation_artifacts_written",
			Message: fmt.Sprintf("delegation artifacts written to %s", artifactDir),
		})
	}

	// Only delegations with no unmet deps are enqueued now; deferred ones are
	// enqueued by advanceDelegations once every dep has succeeded. Allocate
	// worktrees/branch locks only for the ready implement delegations so a
	// deferred delegation does not hold a lock before it can run.
	for _, d := range delegations {
		if len(compactStrings(d.Deps)) > 0 {
			continue
		}
		if err := e.enqueueDelegation(ctx, job, payload, d, artifactDir, ref); err != nil {
			return err
		}
	}
	return nil
}

// enqueueDelegation allocates the per-delegation worktree (or branch lock) for
// implement delegations, then enqueues the child job and records a
// delegation_enqueued event on the parent. It is idempotent: a duplicate
// deterministic-ID insert is swallowed by e.enqueue when the existing job
// matches the request, so it is safe to call from both dispatchDelegations and
// advanceDelegations.
func (e Engine) enqueueDelegation(ctx context.Context, job db.Job, payload JobPayload, d Delegation, artifactDir string, ref taskRef) error {
	request := e.delegationRequest(job, payload, d)
	request.DelegationArtifactDir = artifactDir

	if request.Action == "implement" {
		if e.DelegationWorktrees == nil || strings.TrimSpace(e.Home) == "" {
			if err := e.ensureBranchLock(ctx, request.Repo, request.Branch, request.Agent, ref); err != nil {
				return err
			}
		} else {
			result, err := e.AllocateDelegationWorktree(ctx, DelegationWorktreeRequest{
				Home:         e.Home,
				Repo:         request.Repo,
				ParentJobID:  job.ID,
				DelegationID: request.DelegationID,
				Delegation:   d,
				BaseBranch:   payload.Branch,
				Owner:        request.Agent,
				Checkout:     e.DelegationCheckout,
			}, e.DelegationWorktrees)
			if err != nil {
				var blocked BlockedError
				if errors.As(err, &blocked) {
					return e.block(ctx, ref, blocked.Reason)
				}
				return err
			}
			request.Branch = result.Branch
			request.WorktreePath = result.Path
		}
	}

	if err := e.enqueue(ctx, request); err != nil {
		return fmt.Errorf("dispatch delegation %q: %w", request.DelegationID, err)
	}
	_ = e.Store.AddJobEvent(ctx, db.JobEvent{
		JobID:   job.ID,
		Kind:    "delegation_enqueued",
		Message: fmt.Sprintf("delegation %q enqueued as job %s", request.DelegationID, request.ID),
	})
	return nil
}

// advanceDelegations runs on a parent coordinator job each time one of its
// delegated children finishes. It enqueues now-ready dependent siblings,
// applies the failed delegation's failure_policy, and enqueues a single
// coordinator continuation job once every top-level sibling has reached a
// terminal state. It is idempotent: deferred dependents and the continuation
// job use deterministic ids, so concurrent child completions cannot double
// enqueue.
func (e Engine) advanceDelegations(ctx context.Context, parentJob db.Job, parentPayload JobPayload, parentResult *AgentResult, ref taskRef) error {
	if parentResult == nil || len(parentResult.Delegations) == 0 {
		return nil
	}

	children, err := e.childDelegationJobs(ctx, parentJob.ID)
	if err != nil {
		return err
	}

	// Resolve the shared artifact directory the same way dispatchDelegations did
	// so late-running dependents reference the same brief.md/context-manifest.
	artifactDir, err := delegationArtifactDir(e.ArtifactRoot, parentJob.ID, parentResult)
	if err != nil {
		return err
	}

	// Apply failure policies first so a failed dependency stops its dependents
	// (or escalates/blocks) before we try to enqueue anything new.
	escalate := false
	for _, d := range parentResult.Delegations {
		child, ok := children[d.ID]
		if !ok || !isTerminalJobState(child.State) || child.State == string(JobSucceeded) {
			continue
		}
		switch delegationFailurePolicy(d) {
		case "continue":
			// Independent ready siblings still run; only this branch's
			// dependents are skipped (handled by depsSatisfied below).
			continue
		case "escalate":
			escalate = true
		default: // block_parent (also the empty default)
			return e.block(ctx, ref, fmt.Sprintf("delegation %q failed (failure_policy block_parent): %s", d.ID, childFailureReason(child)))
		}
	}

	// Enqueue deferred dependents whose deps have all succeeded. Skip any whose
	// dependency failed under a continue policy (depsSatisfied returns false
	// unless every dep is succeeded). Already-enqueued children are skipped via
	// the children map and e.enqueue's idempotency.
	if !escalate {
		for _, d := range parentResult.Delegations {
			if len(compactStrings(d.Deps)) == 0 {
				continue
			}
			if _, exists := children[d.ID]; exists {
				continue
			}
			if !depsSatisfied(d.Deps, children) {
				continue
			}
			if err := e.enqueueDelegation(ctx, parentJob, parentPayload, d, artifactDir, ref); err != nil {
				return err
			}
			// Re-read children so a second satisfied dependent in the same pass
			// is not mistaken for still-pending.
			children, err = e.childDelegationJobs(ctx, parentJob.ID)
			if err != nil {
				return err
			}
		}
	}

	// Enqueue the coordinator continuation job once every top-level delegation
	// is resolved (terminal child, or permanently gated by a failed dependency
	// under a continue policy), or immediately when a failure escalates.
	if escalate || allDelegationsResolved(parentResult.Delegations, children) {
		if err := e.maybeEnqueueContinuation(ctx, parentJob, parentPayload, parentResult, children); err != nil {
			return err
		}
	}
	return nil
}

// maybeEnqueueContinuation enqueues exactly one coordinator continuation job for
// a parent whose delegations have all finished. Idempotency is enforced by a
// deterministic continuation id plus a one-shot delegation_continuation_enqueued
// event on the parent, so concurrent child completions enqueue it at most once.
func (e Engine) maybeEnqueueContinuation(ctx context.Context, parentJob db.Job, parentPayload JobPayload, parentResult *AgentResult, children map[string]db.Job) error {
	events, err := e.Store.ListJobEvents(ctx, parentJob.ID)
	if err != nil {
		return err
	}
	if continuationEnqueued(events) {
		return nil
	}

	childPayloads := make(map[string]JobPayload, len(children))
	for id, child := range children {
		childPayload, err := unmarshalPayload(child.Payload)
		if err != nil {
			return err
		}
		childPayloads[id] = childPayload
	}

	request := JobRequest{
		ID:              delegationContinuationID(parentJob.ID),
		Agent:           parentJob.Agent,
		Action:          "ask",
		Repo:            parentPayload.Repo,
		Branch:          parentPayload.Branch,
		PullRequest:     parentPayload.PullRequest,
		HeadSHA:         parentPayload.HeadSHA,
		GoalID:          parentPayload.GoalID,
		TaskID:          parentPayload.TaskID,
		TaskTitle:       parentPayload.TaskTitle,
		LeadAgent:       parentPayload.LeadAgent,
		Reviewers:       parentPayload.Reviewers,
		Sender:          parentJob.Agent,
		Instructions:    buildContinuationPrompt(parentResult, children, childPayloads),
		Constraints:     parentPayload.Constraints,
		ParentJobID:     parentJob.ID,
		DelegationDepth: parentPayload.DelegationDepth,
		DelegatedBy:     parentJob.Agent,
	}
	if err := e.enqueue(ctx, request); err != nil {
		return fmt.Errorf("enqueue continuation for %q: %w", parentJob.ID, err)
	}
	_ = e.Store.AddJobEvent(ctx, db.JobEvent{
		JobID:   parentJob.ID,
		Kind:    "delegation_continuation_enqueued",
		Message: fmt.Sprintf("delegation continuation enqueued as job %s", request.ID),
	})
	return nil
}

// childDelegationJobs returns the direct delegation children of a parent job,
// keyed by delegation id. There is no ListJobsByParent store query, so this
// filters ListJobs on ParentJobID (mirroring latestReviewRound's list+filter).
func (e Engine) childDelegationJobs(ctx context.Context, parentJobID string) (map[string]db.Job, error) {
	jobs, err := e.Store.ListJobs(ctx)
	if err != nil {
		return nil, err
	}
	children := make(map[string]db.Job)
	for _, job := range jobs {
		if job.ParentJobID != parentJobID || strings.TrimSpace(job.DelegationID) == "" {
			continue
		}
		children[job.DelegationID] = job
	}
	return children, nil
}

// depsSatisfied reports whether every dependency id maps to a succeeded sibling.
// An unknown dep id (not yet a child, or never created) is never satisfied, so a
// failed or missing dependency keeps the dependent gated rather than enqueuing
// it prematurely.
func depsSatisfied(deps []string, children map[string]db.Job) bool {
	for _, dep := range compactStrings(deps) {
		child, ok := children[dep]
		if !ok || child.State != string(JobSucceeded) {
			return false
		}
	}
	return true
}

// allDelegationsResolved reports whether every top-level delegation has reached
// a final disposition: either a terminal child job, or no child at all because a
// dependency failed under a continue policy and the delegation can never run.
// queued/running children, or a not-yet-enqueued delegation whose deps are still
// in flight, mean the batch is still active and no continuation is enqueued yet.
func allDelegationsResolved(delegations []Delegation, children map[string]db.Job) bool {
	byID := delegationsByID(delegations)
	for _, d := range delegations {
		if !delegationResolved(d, children, byID) {
			return false
		}
	}
	return true
}

func delegationResolved(d Delegation, children map[string]db.Job, byID map[string]Delegation) bool {
	if child, ok := children[d.ID]; ok {
		return isTerminalJobState(child.State)
	}
	// No child job yet: the delegation is resolved only if it can never run
	// because one of its dependencies is permanently unrunnable.
	return delegationPermanentlyBlocked(d, children, byID, map[string]bool{})
}

// delegationPermanentlyBlocked reports whether a not-yet-enqueued delegation can
// never run because a dependency terminally failed (or is itself permanently
// blocked). It guards against cycles via the visiting set, treating a delegation
// caught in a dependency cycle as blocked so the batch cannot deadlock.
func delegationPermanentlyBlocked(d Delegation, children map[string]db.Job, byID map[string]Delegation, visiting map[string]bool) bool {
	if visiting[d.ID] {
		return true
	}
	visiting[d.ID] = true
	defer delete(visiting, d.ID)
	for _, dep := range compactStrings(d.Deps) {
		if child, ok := children[dep]; ok {
			if isTerminalJobState(child.State) && child.State != string(JobSucceeded) {
				return true
			}
			continue
		}
		depDel, ok := byID[dep]
		if !ok {
			// Unknown dependency id can never be satisfied.
			return true
		}
		if delegationPermanentlyBlocked(depDel, children, byID, visiting) {
			return true
		}
	}
	return false
}

func delegationsByID(delegations []Delegation) map[string]Delegation {
	byID := make(map[string]Delegation, len(delegations))
	for _, d := range delegations {
		byID[d.ID] = d
	}
	return byID
}

func isTerminalJobState(state string) bool {
	switch state {
	case string(JobSucceeded), string(JobFailed), string(JobBlocked), string(JobCancelled):
		return true
	default:
		return false
	}
}

func delegationFailurePolicy(d Delegation) string {
	policy := strings.ToLower(strings.TrimSpace(d.FailurePolicy))
	if policy == "" {
		return "block_parent"
	}
	return policy
}

// delegationFailureHandledByPolicy reports whether the named delegation declares
// a continue/escalate failure_policy, meaning a failure of its child is governed
// by the delegation graph rather than blocking the shared parent task.
func delegationFailureHandledByPolicy(parentResult *AgentResult, delegationID string) bool {
	if parentResult == nil || strings.TrimSpace(delegationID) == "" {
		return false
	}
	for _, d := range parentResult.Delegations {
		if d.ID != delegationID {
			continue
		}
		switch delegationFailurePolicy(d) {
		case "continue", "escalate":
			return true
		default:
			return false
		}
	}
	return false
}

func childFailureReason(child db.Job) string {
	payload, err := unmarshalPayload(child.Payload)
	if err == nil && payload.Result != nil && strings.TrimSpace(payload.Result.Summary) != "" {
		return payload.Result.Summary
	}
	return child.State
}

func continuationEnqueued(events []db.JobEvent) bool {
	for _, event := range events {
		if event.Kind == "delegation_continuation_enqueued" {
			return true
		}
	}
	return false
}

func delegationContinuationID(parentJobID string) string {
	return parentJobID + "/continuation"
}

// buildContinuationPrompt inlines each finished child's job id, agent, decision,
// summary, and PR link into the coordinator continuation prompt so the
// coordinator can synthesize the results without re-reading every child job.
func buildContinuationPrompt(parentResult *AgentResult, children map[string]db.Job, childPayloads map[string]JobPayload) string {
	var builder strings.Builder
	builder.WriteString("All delegated jobs have finished. Review the results below and decide the next step.\n\n")
	builder.WriteString("Delegation results:\n")
	for _, d := range parentResult.Delegations {
		child, ok := children[d.ID]
		if !ok {
			fmt.Fprintf(&builder, "- delegation %q (agent %s): not enqueued (dependencies unmet)\n", d.ID, d.Agent)
			continue
		}
		decision := child.State
		summary := ""
		if payload, ok := childPayloads[d.ID]; ok && payload.Result != nil {
			if strings.TrimSpace(payload.Result.Decision) != "" {
				decision = payload.Result.Decision
			}
			summary = strings.TrimSpace(payload.Result.Summary)
		}
		fmt.Fprintf(&builder, "- delegation %q (job %s, agent %s): %s", d.ID, child.ID, child.Agent, decision)
		if summary != "" {
			fmt.Fprintf(&builder, " — %s", summary)
		}
		if link := childPullRequestLink(childPayloads[d.ID]); link != "" {
			fmt.Fprintf(&builder, " (%s)", link)
		}
		builder.WriteString("\n")
	}
	return builder.String()
}

func childPullRequestLink(payload JobPayload) string {
	if payload.PullRequest <= 0 || strings.TrimSpace(payload.Repo) == "" {
		return ""
	}
	return fmt.Sprintf("https://github.com/%s/pull/%d", payload.Repo, payload.PullRequest)
}

func (e Engine) preflightDelegation(ctx context.Context, request JobRequest) error {
	agent, err := e.Store.GetAgent(ctx, request.Agent)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("agent %q is not subscribed", request.Agent)
		}
		return err
	}
	allowed, err := e.Store.AgentCanAccessRepo(ctx, agent.Name, request.Repo)
	if err != nil {
		return err
	}
	if !allowed {
		return fmt.Errorf("agent %q is not allowed on %q", agent.Name, request.Repo)
	}
	if !contains(agent.Capabilities, request.Action) {
		return fmt.Errorf("agent %q lacks %q capability", agent.Name, request.Action)
	}
	return nil
}

func (e Engine) implementationNeedsFinalizer(ctx context.Context, payload JobPayload) bool {
	if e.ImplementationFinalizer == nil {
		return false
	}
	taskID := strings.TrimSpace(payload.TaskID)
	if taskID == "" {
		return false
	}
	task, err := e.Store.GetTask(ctx, taskID)
	if err != nil {
		return false
	}
	return strings.TrimSpace(task.WorktreePath) != ""
}

func reviewDecisionAgent(job db.Job, payload JobPayload) string {
	if job.Type == "review" &&
		payload.DelegationReason == "runtime_session_busy" &&
		payload.DelegatedAgent == job.Agent &&
		strings.TrimSpace(payload.OriginalAgent) != "" {
		return payload.OriginalAgent
	}
	return job.Agent
}

func (e Engine) jobPayload(ctx context.Context, jobID string) (db.Job, JobPayload, error) {
	job, err := e.Store.GetJob(ctx, jobID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return db.Job{}, JobPayload{}, fmt.Errorf("job %q not found", jobID)
		}
		return db.Job{}, JobPayload{}, err
	}
	payload, err := unmarshalPayload(job.Payload)
	if err != nil {
		return db.Job{}, JobPayload{}, err
	}
	return job, payload, nil
}

func (e Engine) validate() error {
	if e.Store == nil {
		return errors.New("workflow engine store is required")
	}
	return nil
}

func validatePullRequestEvent(event PullRequestEvent) error {
	switch {
	case strings.TrimSpace(event.Repo) == "":
		return errors.New("pull request repo is required")
	case strings.TrimSpace(event.Branch) == "":
		return errors.New("pull request branch is required")
	case event.PullRequest <= 0:
		return errors.New("pull request number is required")
	case strings.TrimSpace(event.TaskID) == "":
		return errors.New("pull request task id is required")
	case strings.TrimSpace(event.LeadAgent) == "":
		return errors.New("pull request lead agent is required")
	}
	return nil
}

func (e Engine) dispatchFix(ctx context.Context, reviewer string, payload JobPayload, result AgentResult, ref taskRef) error {
	leadAgent, err := e.leadAgent(ctx, payload)
	if err != nil {
		return err
	}
	request := JobRequest{
		Agent:       leadAgent,
		Action:      "implement",
		Repo:        payload.Repo,
		Branch:      payload.Branch,
		PullRequest: payload.PullRequest,
		HeadSHA:     payload.HeadSHA,
		GoalID:      payload.GoalID,
		TaskID:      payload.TaskID,
		TaskTitle:   payload.TaskTitle,
		LeadAgent:   leadAgent,
		Reviewers:   e.requiredReviewers(payload),
		ReviewRound: payload.ReviewRound,
		Sender:      reviewer,
		Instructions: fmt.Sprintf(
			"Address requested changes from %s: %s",
			reviewer,
			result.Summary,
		),
	}
	return e.dispatch(ctx, request, ref)
}

func (e Engine) leadAgent(ctx context.Context, payload JobPayload) (string, error) {
	leadAgent := strings.TrimSpace(payload.LeadAgent)
	if leadAgent != "" {
		return leadAgent, nil
	}
	lock, err := e.Store.GetBranchLock(ctx, payload.Repo, payload.Branch)
	if err == nil {
		return lock.Owner, nil
	}
	if errors.Is(err, sql.ErrNoRows) {
		return "", errors.New("lead agent is required")
	}
	return "", err
}

func (e Engine) allRequiredReviewersApproved(ctx context.Context, currentReviewer string, payload JobPayload) (bool, error) {
	required := e.requiredReviewers(payload)
	if len(required) == 0 {
		return true, nil
	}

	approved := map[string]bool{}
	if currentReviewer != "" {
		approved[currentReviewer] = true
	}

	jobs, err := e.Store.ListJobs(ctx)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.Type != "review" {
			continue
		}
		jobPayload, err := unmarshalPayload(job.Payload)
		if err != nil {
			return false, err
		}
		if !sameTask(payload, jobPayload) || !sameReviewRound(payload, jobPayload) || jobPayload.Result == nil {
			continue
		}
		if jobPayload.Result.Decision == "approved" {
			approved[reviewDecisionAgent(job, jobPayload)] = true
		}
	}

	for _, reviewer := range required {
		if !approved[reviewer] {
			return false, nil
		}
	}
	return true, nil
}

func (e Engine) setReviewingIfNotChangesRequested(ctx context.Context, ref taskRef) error {
	if strings.TrimSpace(ref.ID) == "" {
		return nil
	}
	task, err := e.Store.GetTask(ctx, ref.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && task.State == string(TaskChangesRequested) {
		return nil
	}
	return e.setTaskState(ctx, ref, TaskReviewing)
}

func (e Engine) latestReviewRound(ctx context.Context, current JobPayload) (string, error) {
	jobs, err := e.Store.ListJobs(ctx)
	if err != nil {
		return "", err
	}
	latestRound := ""
	latestNumber := 0
	for _, job := range jobs {
		if job.Type != "review" {
			continue
		}
		payload, err := unmarshalPayload(job.Payload)
		if err != nil {
			return "", err
		}
		if !sameTask(current, payload) {
			continue
		}
		round := strings.TrimSpace(payload.ReviewRound)
		if round == "" {
			continue
		}
		number, ok := reviewRoundNumber(round)
		if ok && number > latestNumber {
			latestRound = round
			latestNumber = number
			continue
		}
		if !ok && latestNumber == 0 && round > latestRound {
			latestRound = round
		}
	}
	return latestRound, nil
}

func reviewRoundNumber(round string) (int, bool) {
	value, ok := strings.CutPrefix(round, "review-")
	if !ok {
		return 0, false
	}
	number, err := strconv.Atoi(value)
	if err != nil {
		return 0, false
	}
	return number, true
}

func (e Engine) requiredReviewers(payload JobPayload) []string {
	reviewers := compactStrings(append([]string{}, payload.Reviewers...))
	if len(reviewers) == 0 {
		reviewers = compactStrings(append([]string{}, e.RequiredReviewers...))
	}
	return reviewers
}

func sameTask(left JobPayload, right JobPayload) bool {
	if left.Repo != "" && right.Repo != "" && left.Repo != right.Repo {
		return false
	}
	if left.PullRequest > 0 && right.PullRequest > 0 && left.PullRequest != right.PullRequest {
		return false
	}
	if left.TaskID != "" || right.TaskID != "" {
		return left.TaskID != "" && left.TaskID == right.TaskID
	}
	return left.Repo == right.Repo && left.PullRequest == right.PullRequest
}

func sameReviewRound(left JobPayload, right JobPayload) bool {
	leftRound := strings.TrimSpace(left.ReviewRound)
	rightRound := strings.TrimSpace(right.ReviewRound)
	if leftRound == "" {
		return rightRound == ""
	}
	return leftRound == rightRound
}

func (e Engine) dispatch(ctx context.Context, request JobRequest, ref taskRef) error {
	if request.ID == "" {
		request.ID = e.jobID(request)
	}
	if err := e.ensureAgentAllowed(ctx, request, ref); err != nil {
		return err
	}
	return e.enqueue(ctx, request)
}

func (e Engine) enqueue(ctx context.Context, request JobRequest) error {
	if request.ID == "" {
		request.ID = e.jobID(request)
	}
	_, err := (Mailbox{Store: e.Store}).Enqueue(ctx, request)
	if err == nil {
		return nil
	}
	matches, matchErr := e.existingJobMatchesRequest(ctx, request)
	if matchErr != nil {
		return err
	}
	if matches {
		return nil
	}
	return err
}

func (e Engine) existingJobMatchesRequest(ctx context.Context, request JobRequest) (bool, error) {
	job, err := e.Store.GetJob(ctx, request.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	if job.Type != request.Action {
		return false, nil
	}
	payload, err := unmarshalPayload(job.Payload)
	if err != nil {
		return false, err
	}
	if !jobMatchesRequestAgent(job, payload, request.Agent) {
		return false, nil
	}
	return payloadMatchesRequest(payload, request), nil
}

func jobMatchesRequestAgent(job db.Job, payload JobPayload, requestAgent string) bool {
	if job.Agent == requestAgent {
		return true
	}
	return payload.DelegationReason == "runtime_session_busy" &&
		payload.DelegatedAgent == job.Agent &&
		payload.OriginalAgent == requestAgent
}

func payloadMatchesRequest(payload JobPayload, request JobRequest) bool {
	return payload.Repo == request.Repo &&
		payload.Branch == request.Branch &&
		payload.PullRequest == request.PullRequest &&
		payload.HeadSHA == request.HeadSHA &&
		payload.GoalID == request.GoalID &&
		payload.TaskID == request.TaskID &&
		payload.TaskTitle == request.TaskTitle &&
		payload.LeadAgent == request.LeadAgent &&
		payload.ReviewRound == request.ReviewRound &&
		payload.Sender == request.Sender &&
		payload.Instructions == request.Instructions &&
		payload.WorktreePath == request.WorktreePath &&
		payloadDelegationMatchesRequest(payload, request) &&
		equalStrings(payload.Reviewers, compactStrings(request.Reviewers)) &&
		equalStrings(payload.Constraints, compactStrings(request.Constraints))
}

func payloadDelegationMatchesRequest(payload JobPayload, request JobRequest) bool {
	if payload.OriginalAgent == request.OriginalAgent &&
		payload.DelegatedAgent == request.DelegatedAgent &&
		payload.DelegationReason == request.DelegationReason {
		return true
	}
	return request.OriginalAgent == "" &&
		request.DelegatedAgent == "" &&
		request.DelegationReason == "" &&
		payload.DelegationReason == "runtime_session_busy" &&
		payload.OriginalAgent == request.Agent
}

func equalStrings(left []string, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for i := range left {
		if left[i] != right[i] {
			return false
		}
	}
	return true
}

func (e Engine) ensureAgentAllowed(ctx context.Context, request JobRequest, ref taskRef) error {
	return e.ensureAgentAllowedWithBranchOwner(ctx, request, request.Agent, ref, false)
}

func (e Engine) ensureJobExecutorAllowed(ctx context.Context, job db.Job, payload JobPayload, ref taskRef) error {
	branchOwner := job.Agent
	authorizationAgent := job.Agent
	if job.Type == "implement" && payload.DelegationReason == "runtime_session_busy" && payload.DelegatedAgent == job.Agent && strings.TrimSpace(payload.OriginalAgent) != "" {
		branchOwner = payload.OriginalAgent
	}
	if payload.DelegationReason == "runtime_session_busy" && payload.DelegatedAgent == job.Agent && strings.TrimSpace(payload.OriginalAgent) != "" {
		authorizationAgent = payload.OriginalAgent
	}
	allowMissingCapability := job.Type == "ask" &&
		payload.DelegationReason == "temp_worker_merge_back" &&
		payload.OriginalAgent == job.Agent
	return e.ensureAgentAllowedWithBranchOwner(ctx, JobRequest{
		Agent:  authorizationAgent,
		Action: job.Type,
		Repo:   payload.Repo,
		Branch: payload.Branch,
	}, branchOwner, ref, allowMissingCapability)
}

func (e Engine) ensureAgentAllowedWithBranchOwner(ctx context.Context, request JobRequest, branchOwner string, ref taskRef, allowMissingCapability bool) error {
	agent, err := e.Store.GetAgent(ctx, request.Agent)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return e.block(ctx, ref, fmt.Sprintf("agent %q is not subscribed", request.Agent))
		}
		return err
	}
	allowed, err := e.Store.AgentCanAccessRepo(ctx, agent.Name, request.Repo)
	if err != nil {
		return err
	}
	if !allowed {
		return e.block(ctx, ref, fmt.Sprintf("agent %q is not allowed on %q", agent.Name, request.Repo))
	}
	if !contains(agent.Capabilities, request.Action) && !allowMissingCapability {
		return e.block(ctx, ref, fmt.Sprintf("agent %q lacks %q capability", agent.Name, request.Action))
	}
	if request.Action == "implement" {
		if err := e.ensureBranchLock(ctx, request.Repo, request.Branch, branchOwner, ref); err != nil {
			return err
		}
	}
	return nil
}

func (e Engine) ensureBranchLock(ctx context.Context, repo string, branch string, owner string, ref taskRef) error {
	if strings.TrimSpace(branch) == "" {
		return e.block(ctx, ref, "branch lock rejected action: branch is required")
	}
	acquired, err := e.Store.AcquireLock(ctx, db.BranchLock{RepoFullName: repo, Branch: branch, Owner: owner})
	if err != nil {
		return err
	}
	if !acquired {
		return e.block(ctx, ref, fmt.Sprintf("branch lock rejected action for %s", branch))
	}
	return nil
}

func (e Engine) runMergeGate(ctx context.Context, reviewer string, payload JobPayload, ref taskRef) (MergeDecision, error) {
	if e.MergeGate == nil {
		return MergeDecision{Ready: true}, e.setTaskState(ctx, ref, TaskReadyToMerge)
	}
	reviewRequired, err := e.mergeGateReviewRequired(ctx, payload)
	if err != nil {
		return MergeDecision{}, err
	}
	decision, err := e.MergeGate.Evaluate(ctx, MergeRequest{
		Repo:           payload.Repo,
		Branch:         payload.Branch,
		PullRequest:    payload.PullRequest,
		HeadSHA:        payload.HeadSHA,
		TaskID:         payload.TaskID,
		Reviewer:       reviewer,
		ReviewOptional: !reviewRequired,
	})
	if err != nil {
		return MergeDecision{}, err
	}
	if !decision.Ready {
		reason := strings.TrimSpace(decision.Reason)
		if reason == "" {
			reason = "merge gate rejected action"
		}
		return decision, e.block(ctx, ref, reason)
	}
	if decision.Merged {
		return decision, e.setTaskState(ctx, ref, TaskMerged)
	}
	return decision, e.setTaskState(ctx, ref, TaskReadyToMerge)
}

func (e Engine) mergeGateReviewRequired(ctx context.Context, payload JobPayload) (bool, error) {
	if len(e.requiredReviewers(payload)) > 0 {
		return true, nil
	}
	jobs, err := e.Store.ListJobs(ctx)
	if err != nil {
		return false, err
	}
	for _, job := range jobs {
		if job.Type != "review" {
			continue
		}
		jobPayload, err := unmarshalPayload(job.Payload)
		if err != nil {
			return false, err
		}
		if sameTask(payload, jobPayload) {
			return true, nil
		}
	}
	return false, nil
}

func (e Engine) recordPullRequestBaseline(ctx context.Context, event PullRequestEvent) error {
	if event.PullRequest <= 0 {
		return nil
	}
	return e.Store.UpsertPullRequest(ctx, db.PullRequest{
		RepoFullName: event.Repo,
		Number:       int64(event.PullRequest),
		HeadBranch:   event.Branch,
		HeadSHA:      event.HeadSHA,
		State:        "open",
	})
}

func (e Engine) nextReviewRound(ctx context.Context, event PullRequestEvent) (string, error) {
	jobs, err := e.Store.ListJobs(ctx)
	if err != nil {
		return "", err
	}
	current := JobPayload{Repo: event.Repo, PullRequest: event.PullRequest, TaskID: event.TaskID}
	rounds := map[string]bool{}
	existingHeadRound := ""
	for _, job := range jobs {
		if job.Type != "review" {
			continue
		}
		payload, err := unmarshalPayload(job.Payload)
		if err != nil {
			return "", err
		}
		if !sameTask(current, payload) {
			continue
		}
		round := strings.TrimSpace(payload.ReviewRound)
		if round == "" {
			round = job.ID
		}
		if payload.HeadSHA != "" && payload.HeadSHA == event.HeadSHA {
			existingHeadRound = round
		}
		rounds[round] = true
	}
	if existingHeadRound != "" {
		return existingHeadRound, nil
	}
	return "review-" + strconv.Itoa(len(rounds)+1), nil
}

func (e Engine) reviewApprovalAlreadyAdvanced(ctx context.Context, ref taskRef) (bool, error) {
	if strings.TrimSpace(ref.ID) == "" {
		return false, nil
	}
	task, err := e.Store.GetTask(ctx, ref.ID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, err
	}
	return task.State == string(TaskReadyToMerge) || task.State == string(TaskMerged), nil
}

func (e Engine) block(ctx context.Context, ref taskRef, reason string) error {
	if err := e.setTaskState(ctx, ref, TaskBlocked); err != nil {
		return err
	}
	return BlockedError{Reason: reason}
}

func (e Engine) setTaskState(ctx context.Context, ref taskRef, state TaskState) error {
	if strings.TrimSpace(ref.ID) == "" {
		return nil
	}
	task := db.Task{
		ID:           ref.ID,
		RepoFullName: ref.Repo,
		GoalID:       ref.GoalID,
		Title:        ref.Title,
		State:        string(state),
		Branch:       ref.Branch,
	}
	existing, err := e.Store.GetTask(ctx, ref.ID)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil {
		if task.GoalID == "" {
			task.GoalID = existing.GoalID
		}
		if task.RepoFullName == "" {
			task.RepoFullName = existing.RepoFullName
		}
		if task.Title == "" {
			task.Title = existing.Title
		}
		if task.Branch == "" {
			task.Branch = existing.Branch
		}
	}
	return e.Store.UpsertTask(ctx, task)
}

func (e Engine) jobID(request JobRequest) string {
	if e.JobID != nil {
		return e.JobID(request)
	}
	hash := fnv.New64a()
	for _, value := range []string{
		request.Repo,
		request.Branch,
		strconv.Itoa(request.PullRequest),
		request.TaskID,
		request.Agent,
		request.Action,
		request.ReviewRound,
		request.Instructions,
	} {
		_, _ = hash.Write([]byte(value))
		_, _ = hash.Write([]byte{0})
	}
	return "workflow-" + strconv.FormatUint(hash.Sum64(), 36)
}

type taskRef struct {
	ID     string
	Repo   string
	GoalID string
	Title  string
	Branch string
}

func taskRefFromPullRequest(event PullRequestEvent) taskRef {
	return taskRef{
		ID:     event.TaskID,
		Repo:   event.Repo,
		GoalID: event.GoalID,
		Title:  event.TaskTitle,
		Branch: event.Branch,
	}
}

func taskRefFromPayload(payload JobPayload) taskRef {
	return taskRef{
		ID:     payload.TaskID,
		Repo:   payload.Repo,
		GoalID: payload.GoalID,
		Title:  payload.TaskTitle,
		Branch: payload.Branch,
	}
}

func contains(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}
