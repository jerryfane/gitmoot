package workflow

import (
	"context"
	"strings"
	"testing"

	"github.com/gitmoot/gitmoot/internal/db"
)

func seedWorkflowJournalLifecycle(t *testing.T, store *db.Store, state TaskState) PullRequestEvent {
	t.Helper()
	const (
		repo       = "owner/repo"
		branch     = "feat/lifecycle"
		workflowID = "release/lifecycle"
	)
	ctx := context.Background()
	if err := store.UpsertTask(ctx, db.Task{ID: "task-lifecycle", RepoFullName: repo, Title: "Lifecycle", State: string(state), Branch: branch}); err != nil {
		t.Fatalf("UpsertTask: %v", err)
	}
	if err := store.CreateJob(ctx, db.Job{ID: "job-lifecycle", Agent: "worker", Type: "implement", State: "succeeded",
		Payload: `{"workflow_id":"release/lifecycle","repo":"owner/repo","branch":"feat/lifecycle","pull_request":958}`}); err != nil {
		t.Fatalf("CreateJob: %v", err)
	}
	if err := store.SetWorkflowDescription(ctx, workflowID, "Human stable description"); err != nil {
		t.Fatalf("SetWorkflowDescription: %v", err)
	}
	return PullRequestEvent{Repo: repo, Branch: branch, PullRequest: 958, HeadSHA: "head", TaskID: "task-lifecycle", TaskTitle: "Lifecycle", LeadAgent: "lead", Sender: "github"}
}

func TestHandlePullRequestReadyToMergeJournalsWorkflowOnce(t *testing.T) {
	store := openEngineStore(t)
	event := seedWorkflowJournalLifecycle(t, store, TaskReviewing)
	engine := Engine{Store: store}
	for i := 0; i < 2; i++ {
		if err := engine.HandlePullRequestReadyToMerge(context.Background(), event); err != nil {
			t.Fatalf("HandlePullRequestReadyToMerge pass %d: %v", i+1, err)
		}
	}
	if inserted, err := RecordPullRequestWorkflowTransition(context.Background(), store, event, PullRequestJournalOpened); err != nil || inserted {
		t.Fatalf("late PR-open backfill = inserted %v, err=%v; must not regress ready status", inserted, err)
	}
	assertWorkflowJournalTransition(t, store, "[auto:pr:958:ready] PR #958 checks green — ready to merge", "PR #958 checks green — ready to merge")
}

func TestHandleReviewPullRequestClosedJournalsWorkflowOnce(t *testing.T) {
	for _, tc := range []struct {
		name       string
		merged     bool
		wantBody   string
		wantStatus string
	}{
		{name: "merged", merged: true, wantBody: "[auto:pr:958:merged] PR #958 merged", wantStatus: "PR #958 merged"},
		{name: "closed without merging", merged: false, wantBody: "[auto:pr:958:closed] PR #958 closed without merging", wantStatus: "PR #958 closed without merging"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openEngineStore(t)
			event := seedWorkflowJournalLifecycle(t, store, TaskReviewing)
			engine := Engine{Store: store}
			for i := 0; i < 2; i++ {
				if err := engine.HandleReviewPullRequestClosed(context.Background(), event, tc.merged); err != nil {
					t.Fatalf("HandleReviewPullRequestClosed pass %d: %v", i+1, err)
				}
			}
			assertWorkflowJournalTransition(t, store, tc.wantBody, tc.wantStatus)
			if !tc.merged && strings.Contains(tc.wantStatus, " merged") {
				t.Fatalf("closed-unmerged status implies success: %q", tc.wantStatus)
			}
		})
	}
}

func TestRecordPullRequestWorkflowTransitionSummaryTracksMergedReceipt(t *testing.T) {
	for _, tc := range []struct {
		name       string
		transition PullRequestJournalTransition
		wantMerged bool
	}{
		{name: "merged", transition: PullRequestJournalMerged, wantMerged: true},
		{name: "opened", transition: PullRequestJournalOpened},
	} {
		t.Run(tc.name, func(t *testing.T) {
			store := openEngineStore(t)
			ctx := context.Background()
			event := seedWorkflowJournalLifecycle(t, store, TaskReviewing)
			inserted, err := RecordPullRequestWorkflowTransition(ctx, store, event, tc.transition)
			if err != nil || !inserted {
				t.Fatalf("RecordPullRequestWorkflowTransition = (inserted=%v, err=%v)", inserted, err)
			}
			summary, err := store.WorkflowSummary(ctx, "release/lifecycle")
			if err != nil {
				t.Fatalf("WorkflowSummary: %v", err)
			}
			if got := summary.LastMergedReceiptAt != ""; got != tc.wantMerged {
				t.Fatalf("LastMergedReceiptAt = %q, want merged=%v", summary.LastMergedReceiptAt, tc.wantMerged)
			}
		})
	}
}

func assertWorkflowJournalTransition(t *testing.T, store *db.Store, wantBody, wantStatus string) {
	t.Helper()
	ctx := context.Background()
	notes, err := store.ListWorkflowNotes(ctx, "release/lifecycle", 0)
	if err != nil || len(notes) != 1 {
		t.Fatalf("notes = %+v, err=%v; want exactly one", notes, err)
	}
	if notes[0].Author != db.WorkflowAutoNoteAuthor || notes[0].Body != wantBody {
		t.Fatalf("auto note = %+v, want body %q", notes[0], wantBody)
	}
	meta, err := store.GetWorkflowMeta(ctx, "release/lifecycle")
	if err != nil || meta.Status != wantStatus || meta.Description != "Human stable description" {
		t.Fatalf("meta = %+v, err=%v", meta, err)
	}
}
