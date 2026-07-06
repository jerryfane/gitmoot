package workflow

import (
	"context"
	"testing"

	"github.com/jerryfane/gitmoot/internal/runtime"
)

const delegatingResult = `{"gitmoot_result":{"decision":"approved","summary":"done","findings":[],"changes_made":[],"tests_run":[],"needs":[],"delegations":[{"id":"d","agent":"a","action":"review","prompt":"go"}]}}`

// TestMailboxRunStripsPipelineStageDelegations proves a #681 pipeline stage is a
// LEAF: a stage-sender job whose shell command emits delegations[] has them
// stripped before the result is stored, so the engine's dispatchDelegations can
// never spawn phantom top-level children from a stage (whose ParentJobID is empty).
func TestMailboxRunStripsPipelineStageDelegations(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	mailbox := Mailbox{Store: store}
	agent := runtime.Agent{Name: "pipeline-x-runner", Runtime: runtime.ShellRuntime, RuntimeRef: "printf ok", RepoScope: "jerryfane/gitmoot", Role: "pipeline-runner"}
	adapter := &fakeDelivery{outputs: []string{delegatingResult}}

	if _, err := mailbox.Enqueue(ctx, JobRequest{ID: "stage-1", Agent: "pipeline-x-runner", Action: "ask", Repo: "jerryfane/gitmoot", Sender: PipelineJobSender}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := mailbox.Run(ctx, "stage-1", agent, adapter); err != nil {
		t.Fatalf("Run: %v", err)
	}

	job, err := store.GetJob(ctx, "stage-1")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	payload, err := ParseJobPayload(job.Payload)
	if err != nil {
		t.Fatalf("ParseJobPayload: %v", err)
	}
	if payload.Result == nil {
		t.Fatalf("stage job has no result")
	}
	if len(payload.Result.Delegations) != 0 {
		t.Fatalf("pipeline stage delegations = %+v, want stripped (leaf)", payload.Result.Delegations)
	}
}

// TestMailboxRunKeepsNonPipelineDelegations is the control: a normal (non-pipeline)
// sender's delegations survive, so the strip is scoped strictly to pipeline stages.
func TestMailboxRunKeepsNonPipelineDelegations(t *testing.T) {
	ctx := context.Background()
	store := openTestStore(t)
	mailbox := Mailbox{Store: store}
	agent := runtime.Agent{Name: "coord", Runtime: runtime.ShellRuntime, RuntimeRef: "printf ok", RepoScope: "jerryfane/gitmoot", Role: "coordinator"}
	adapter := &fakeDelivery{outputs: []string{delegatingResult}}

	if _, err := mailbox.Enqueue(ctx, JobRequest{ID: "job-1", Agent: "coord", Action: "ask", Repo: "jerryfane/gitmoot", Sender: "user"}); err != nil {
		t.Fatalf("Enqueue: %v", err)
	}
	if _, err := mailbox.Run(ctx, "job-1", agent, adapter); err != nil {
		t.Fatalf("Run: %v", err)
	}

	job, err := store.GetJob(ctx, "job-1")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	payload, err := ParseJobPayload(job.Payload)
	if err != nil {
		t.Fatalf("ParseJobPayload: %v", err)
	}
	if payload.Result == nil || len(payload.Result.Delegations) != 1 {
		t.Fatalf("non-pipeline delegations = %+v, want preserved (1)", payload.Result)
	}
}
