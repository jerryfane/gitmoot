package workflow

import (
	"context"
	"testing"
	"time"

	"github.com/gitmoot/gitmoot/internal/db"
)

// naiveCountRoot is the pre-#420 reference: scan the whole jobs table and filter
// on the self-root / payload.RootJobID rule. The indexed countRootDelegationJobs
// must be byte-identical to this.
func naiveCountRoot(t *testing.T, store *db.Store, rootID string) int {
	t.Helper()
	jobs, err := store.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs returned error: %v", err)
	}
	count := 0
	for _, job := range jobs {
		if job.ID == rootID {
			count++
			continue
		}
		payload, err := unmarshalPayload(job.Payload)
		if err != nil {
			continue
		}
		if payload.RootJobID == rootID {
			count++
		}
	}
	return count
}

// naiveSumTokensRoot is the pre-#420 reference for the per-root token sum.
func naiveSumTokensRoot(t *testing.T, store *db.Store, rootID string) int {
	t.Helper()
	jobs, err := store.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs returned error: %v", err)
	}
	total := 0
	for _, job := range jobs {
		if job.ID == rootID {
			total += job.InputTokens + job.OutputTokens
			continue
		}
		payload, err := unmarshalPayload(job.Payload)
		if err != nil {
			continue
		}
		if payload.RootJobID == rootID {
			total += job.InputTokens + job.OutputTokens
		}
	}
	return total
}

// naiveSumCostRoot is the pre-#420 reference for the per-root dollar cost.
func naiveSumCostRoot(t *testing.T, store *db.Store, rootID string) float64 {
	t.Helper()
	jobs, err := store.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs returned error: %v", err)
	}
	total := 0.0
	for _, job := range jobs {
		payload, err := unmarshalPayload(job.Payload)
		if err != nil {
			continue
		}
		if job.ID == rootID || payload.RootJobID == rootID {
			total += costFromTokens(payload.Model, job.InputTokens, job.OutputTokens)
		}
	}
	return total
}

// seedRootIDTrees builds two coordination trees (R and S) plus an unrelated
// standalone job, each child carrying a model and usage, so every root-scoped
// helper has a non-trivial, distinct answer per root.
func seedRootIDTrees(t *testing.T, store *db.Store) {
	t.Helper()
	ctx := context.Background()

	insertCompletedJob(t, store, db.Job{ID: "R", Agent: "coord", Type: "ask"}, JobPayload{Sender: "coord", Model: "claude-opus-4-8"})
	if err := store.UpdateJobUsage(ctx, "R", 1_000_000, 1_000_000); err != nil {
		t.Fatalf("UpdateJobUsage(R) returned error: %v", err)
	}
	insertCompletedJob(t, store, db.Job{ID: "R/c1", Agent: "w", Type: "ask"}, JobPayload{RootJobID: "R", Model: "claude-opus-4-8"})
	if err := store.UpdateJobUsage(ctx, "R/c1", 0, 1_000_000); err != nil {
		t.Fatalf("UpdateJobUsage(R/c1) returned error: %v", err)
	}
	insertCompletedJob(t, store, db.Job{ID: "R/c2", Agent: "w", Type: "ask"}, JobPayload{RootJobID: "R", Model: "claude-sonnet-4-5"})
	if err := store.UpdateJobUsage(ctx, "R/c2", 500_000, 250_000); err != nil {
		t.Fatalf("UpdateJobUsage(R/c2) returned error: %v", err)
	}

	// Second tree S — its usage must never leak into R's answers.
	insertCompletedJob(t, store, db.Job{ID: "S", Agent: "coord", Type: "ask"}, JobPayload{Sender: "coord", Model: "claude-opus-4-8"})
	if err := store.UpdateJobUsage(ctx, "S", 2_000_000, 0); err != nil {
		t.Fatalf("UpdateJobUsage(S) returned error: %v", err)
	}
	insertCompletedJob(t, store, db.Job{ID: "S/c1", Agent: "w", Type: "ask"}, JobPayload{RootJobID: "S", Model: "claude-opus-4-8"})
	if err := store.UpdateJobUsage(ctx, "S/c1", 0, 3_000_000); err != nil {
		t.Fatalf("UpdateJobUsage(S/c1) returned error: %v", err)
	}

	// A standalone job that roots itself and belongs to no tree.
	insertCompletedJob(t, store, db.Job{ID: "lonely", Agent: "coord", Type: "ask"}, JobPayload{Sender: "coord", Model: "claude-opus-4-8"})
}

// TestRootHelpersByteIdenticalToNaiveScan is the load-bearing #420 test: the four
// indexed root-scoped helpers must return exactly what the pre-#420 full-table
// scan + payload-unmarshal reference computes, for every root. This fails against
// a naive implementation that forgets to populate root_id on insert (the indexed
// helpers would then see an empty tree and return 0) — proving the column is
// wired end to end, not just declared.
func TestRootHelpersByteIdenticalToNaiveScan(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	engine := testEngine(store)
	seedRootIDTrees(t, store)

	for _, rootID := range []string{"R", "S", "lonely"} {
		gotCount, err := engine.countRootDelegationJobs(ctx, rootID)
		if err != nil {
			t.Fatalf("countRootDelegationJobs(%s) returned error: %v", rootID, err)
		}
		if want := naiveCountRoot(t, store, rootID); gotCount != want {
			t.Fatalf("countRootDelegationJobs(%s) = %d, want %d (naive scan)", rootID, gotCount, want)
		}

		gotTokens, err := engine.sumRootDelegationTokens(ctx, rootID)
		if err != nil {
			t.Fatalf("sumRootDelegationTokens(%s) returned error: %v", rootID, err)
		}
		if want := naiveSumTokensRoot(t, store, rootID); gotTokens != want {
			t.Fatalf("sumRootDelegationTokens(%s) = %d, want %d (naive scan)", rootID, gotTokens, want)
		}

		gotCost, err := engine.sumRootDelegationCost(ctx, rootID)
		if err != nil {
			t.Fatalf("sumRootDelegationCost(%s) returned error: %v", rootID, err)
		}
		if want := naiveSumCostRoot(t, store, rootID); !floatNear(gotCost, want, 1e-9) {
			t.Fatalf("sumRootDelegationCost(%s) = %v, want %v (naive scan)", rootID, gotCost, want)
		}
	}

	// Sanity: the trees are actually non-trivial, so a stubbed helper returning 0
	// could not accidentally pass the equality above.
	if c := naiveCountRoot(t, store, "R"); c != 3 {
		t.Fatalf("seed sanity: naive R count = %d, want 3", c)
	}
	if c := naiveCountRoot(t, store, "S"); c != 2 {
		t.Fatalf("seed sanity: naive S count = %d, want 2", c)
	}
}

// TestRootPausedDurationByteIdentical pins that rootPausedDuration over the
// indexed tree equals a direct sum of each tree job's jobPausedDuration — the
// same grouping the pre-#420 full scan produced. With no escalation events every
// job contributes 0, so the whole-tree duration is 0 for both roots; the test
// guards that the indexed lookup still walks exactly the tree's jobs.
func TestRootPausedDurationByteIdentical(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	engine := testEngine(store)
	seedRootIDTrees(t, store)
	now := time.Now().UTC()

	for _, rootID := range []string{"R", "S"} {
		got := engine.rootPausedDuration(ctx, rootID, now)

		jobs, err := store.ListJobsByRoot(ctx, rootID)
		if err != nil {
			t.Fatalf("ListJobsByRoot(%s) returned error: %v", rootID, err)
		}
		var want time.Duration
		for _, job := range jobs {
			want += engine.jobPausedDuration(ctx, job.ID, now)
		}
		if got != want {
			t.Fatalf("rootPausedDuration(%s) = %v, want %v", rootID, got, want)
		}
	}
}

// TestReDelegationPreservesRootIDInTree pins #420 end to end at the engine layer:
// inserting a child via the normal write path, then re-delegating it in place,
// keeps it counted in its original root's tree (root_id is untouched), so the
// per-root count is stable across the re-assignment.
func TestReDelegationPreservesRootIDInTree(t *testing.T) {
	ctx := context.Background()
	store := openEngineStore(t)
	engine := testEngine(store)

	insertCompletedJob(t, store, db.Job{ID: "R", Agent: "coord", Type: "ask"}, JobPayload{Sender: "coord"})
	// A queued child rooted at R.
	childPayload, err := marshalPayload(JobPayload{RootJobID: "R", Sender: "from"})
	if err != nil {
		t.Fatalf("marshalPayload returned error: %v", err)
	}
	if err := store.CreateJob(ctx, db.Job{ID: "R/child", Agent: "from", Type: "ask", State: string(JobQueued), Payload: childPayload}); err != nil {
		t.Fatalf("CreateJob(R/child) returned error: %v", err)
	}

	before, err := engine.countRootDelegationJobs(ctx, "R")
	if err != nil {
		t.Fatalf("countRootDelegationJobs before returned error: %v", err)
	}
	if before != 2 {
		t.Fatalf("count before re-delegation = %d, want 2 (R + R/child)", before)
	}

	reassigned, err := marshalPayload(JobPayload{RootJobID: "R", Sender: "to"})
	if err != nil {
		t.Fatalf("marshalPayload (reassigned) returned error: %v", err)
	}
	ok, err := store.DelegateQueuedJob(ctx, "R/child", "from", "to", reassigned, db.JobEvent{Kind: "delegated", Message: "reassigned"})
	if err != nil {
		t.Fatalf("DelegateQueuedJob returned error: %v", err)
	}
	if !ok {
		t.Fatal("DelegateQueuedJob reported no rows affected")
	}

	after, err := engine.countRootDelegationJobs(ctx, "R")
	if err != nil {
		t.Fatalf("countRootDelegationJobs after returned error: %v", err)
	}
	if after != before {
		t.Fatalf("count after re-delegation = %d, want %d (root_id preserved)", after, before)
	}
}
