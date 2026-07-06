package cli

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	gitutil "github.com/jerryfane/gitmoot/internal/git"
	"github.com/jerryfane/gitmoot/internal/runtime"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// This file is a TEST-ONLY full-chain E2E proving #696 (PR #699): top-level
// read-only same-repo jobs (ask/review) are parallelized by the pool's
// auto-detached-worktree isolation, while mutating (implement) jobs still
// serialize on the shared repo:<repo> checkout key. Everything runs through the
// REAL pool scheduler (runQueuedJobsForRepo → runQueuedJobsForRepoPool),
// the REAL checkout-key function (queuedJobCheckoutKey), and the REAL isolation
// allocator (jobWorker.allocatePoolIsolationWorktree) — no hand-rolled dispatch.
//
// It reuses the daemon_test.go harness (daemonWorkerStore / seedDaemonWorkerRepo
// / seedDaemonWorkerAgent / enqueueDaemonWorkerJob / poolSchedulerWorker /
// cliWorkerFakeAdapter / createDaemonWorkerGitCheckout), the same pattern the
// existing #394-part-2 tests use (TestRunQueuedJobsPoolIsolatesContendedReadJob,
// TestPoolIsolationAppendsCommittedTipNote).
//
// Determinism: concurrency is proven by a rendezvous barrier (a fake adapter
// whose Deliver blocks until the test releases it) that records peak concurrent
// in-flight — NOT by sleeps. The bounded time.After guards fire only on the
// failing (pre-#696) path so a regression fails fast instead of hanging.

// deliverBarrier records peak concurrent in-flight Deliver calls and lets the
// test hold every worker inside Deliver at once (so a true concurrency snapshot
// can be taken) before releasing them. Wired into the existing
// cliWorkerFakeAdapter via its onDeliver hook, exactly as poolConcurrencyTracker
// is — but with an explicit rendezvous instead of a sleep.
type deliverBarrier struct {
	mu      sync.Mutex
	active  int
	peak    int
	entered chan struct{} // each Deliver announces entry (buffered ≥ #jobs)
	release chan struct{} // each Deliver receives one token before returning
}

func newDeliverBarrier(capacity int) *deliverBarrier {
	return &deliverBarrier{
		entered: make(chan struct{}, capacity),
		release: make(chan struct{}, capacity),
	}
}

// hook is the onDeliver callback: bump the in-flight count (updating peak),
// announce entry, then block until the test releases exactly one worker.
func (b *deliverBarrier) hook() {
	b.mu.Lock()
	b.active++
	if b.active > b.peak {
		b.peak = b.active
	}
	b.mu.Unlock()
	b.entered <- struct{}{}
	<-b.release
	b.mu.Lock()
	b.active--
	b.mu.Unlock()
}

func (b *deliverBarrier) peakInflight() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.peak
}

// waitEntered blocks until one worker has entered Deliver, failing (rather than
// hanging forever) if none arrives — the bounded guard is only hit on the broken
// pre-#696 serialization path, never on the passing concurrent path.
func waitEntered(t *testing.T, b *deliverBarrier, msg string) {
	t.Helper()
	select {
	case <-b.entered:
	case <-time.After(10 * time.Second):
		t.Fatal(msg)
	}
}

// TestReadOnlySameRepoJobsRunConcurrently is assertion (A): the #696 win.
//
// Two top-level READ-ONLY (ask) same-repo jobs with no branch/task worktree,
// enqueued through the production mailbox and run through the real pool
// scheduler, must dispatch CONCURRENTLY — one in the shared checkout, the other
// auto-isolated into a detached committed-tip worktree so their checkout keys
// diverge. peak concurrent in-flight == 2 and the two jobs carry DISTINCT
// checkout keys (repo:<repo> vs worktree:<path>).
//
// LOAD-BEARING: this is the assertion that pins #696's new behavior. On pre-#696
// main both read jobs shared the "repo:<repo>" key and serialized, so only one
// would ever be in Deliver at a time — the second waitEntered would time out and
// peakInflight() would be 1, failing this test.
func TestReadOnlySameRepoJobsRunConcurrently(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	checkout := createDaemonWorkerGitCheckout(t, "main")
	store := daemonWorkerStore(t)
	seedDaemonWorkerRepo(t, store, "owner/repo", checkout)
	seedDaemonWorkerAgent(t, store, "audit", runtime.ShellRuntime, "unused", []string{"ask"}, "owner/repo")
	enqueueDaemonWorkerJob(t, store, workflow.JobRequest{ID: "read-1", Agent: "audit", Action: "ask", Repo: "owner/repo", Branch: "main", PullRequest: 1, Instructions: "audit"})
	enqueueDaemonWorkerJob(t, store, workflow.JobRequest{ID: "read-2", Agent: "audit", Action: "ask", Repo: "owner/repo", Branch: "main", PullRequest: 2, Instructions: "audit"})

	barrier := newDeliverBarrier(2)
	adapter := &cliWorkerFakeAdapter{output: poolSchedulerAskResult, onDeliver: barrier.hook}
	worker := poolSchedulerWorker(t, store, adapter, true)
	worker.ConfigHome = home // enable auto-detached-worktree isolation

	runErr := make(chan error, 1)
	go func() { runErr <- runQueuedJobsForRepo(ctx, worker, 2, "", "") }()

	// Both read jobs must be inside Deliver at the same instant. Reading two
	// "entered" signals with neither released proves they overlap; peak is then 2.
	waitEntered(t, barrier, "first read job never reached Deliver")
	waitEntered(t, barrier, "SECOND read job never dispatched concurrently — same-repo read jobs serialized (pre-#696 regression: this assertion pins #696)")
	if peak := barrier.peakInflight(); peak != 2 {
		t.Fatalf("peak concurrent read jobs = %d, want 2 (both must be in-flight at once)", peak)
	}

	// Release both and let the pool drain.
	barrier.release <- struct{}{}
	barrier.release <- struct{}{}
	select {
	case err := <-runErr:
		if err != nil {
			t.Fatalf("pool run: %v", err)
		}
	case <-time.After(20 * time.Second):
		t.Fatal("pool did not drain after releasing both read jobs")
	}

	// Both succeeded, and they carried DISTINCT checkout keys (the auto-detached
	// worktree, not the shared repo:<repo> key). Compute the keys through the REAL
	// production queuedJobCheckoutKey on the final stored jobs.
	sharedKey := "repo:owner/repo"
	keys := map[string]string{}
	isolated := 0
	for _, id := range []string{"read-1", "read-2"} {
		job, err := store.GetJob(ctx, id)
		if err != nil {
			t.Fatalf("GetJob %s: %v", id, err)
		}
		if job.State != string(workflow.JobSucceeded) {
			t.Fatalf("%s state = %q, want succeeded", id, job.State)
		}
		key := queuedJobCheckoutKey(ctx, store, job)
		keys[id] = key
		payload, err := daemonJobPayload(job)
		if err != nil {
			t.Fatalf("payload %s: %v", id, err)
		}
		if payload.WorktreePath != "" {
			isolated++
			if key == sharedKey || !strings.HasPrefix(key, "worktree:") {
				t.Fatalf("%s isolated but checkout key = %q, want a distinct worktree:<path> key", id, key)
			}
		}
	}
	if isolated != 1 {
		t.Fatalf("auto-isolated read jobs = %d, want exactly 1 (the contended sibling)", isolated)
	}
	if keys["read-1"] == keys["read-2"] {
		t.Fatalf("both read jobs shared checkout key %q — they must be DISTINCT to run concurrently (pre-#696 both were %q)", keys["read-1"], sharedKey)
	}
	if keys["read-1"] != sharedKey && keys["read-2"] != sharedKey {
		t.Fatalf("neither read job kept the shared %q key (keys=%v); exactly one stays shared + one is isolated", sharedKey, keys)
	}
}

// TestImplementSameRepoJobsStillSerialize is assertion (B): the guard.
//
// Two top-level same-repo IMPLEMENT jobs (no branch/task worktree) must NOT be
// parallelized: implement is excluded from auto-isolation (poolIsolationEligible)
// so both share the real "repo:<repo>" checkout key and serialize. peak
// concurrent in-flight == 1 — the #696 read-only parallelization did not leak to
// mutating jobs.
//
// This is an INVARIANT guard (it holds on pre- and post-#696 main alike, since
// implement always serialized); it exists to prove the #696 change is scoped to
// read-only actions. Its load-bearing contrast is with assertion (A): with the
// same barrier + same pool, read jobs reach peak 2 while implement jobs reach
// peak 1. Isolation is deliberately ENABLED here (ConfigHome + a real checkout),
// so peak 1 is because implement is ineligible, not because isolation was off.
func TestImplementSameRepoJobsStillSerialize(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	checkout := createDaemonWorkerGitCheckout(t, "main")
	store := daemonWorkerStore(t)
	seedDaemonWorkerRepo(t, store, "owner/repo", checkout)
	// shell runtime has no runtime-session lock (runtimeSessionResourceKey → false),
	// so the ONLY thing that can serialize these two jobs is the checkout key —
	// making this a load-bearing test of checkout-key serialization, not session
	// serialization. workspace-write policy clears the implement fail-closed gate.
	seedDaemonWorkerAgent(t, store, "lead", runtime.ShellRuntime, "unused", []string{"implement"}, "owner/repo")
	// decision "failed" makes each job terminal immediately after Deliver (no
	// finalize/PR path), so the shared checkout key frees and the next job runs.
	const implementResult = `{"gitmoot_result":{"decision":"failed","summary":"stop","findings":[],"changes_made":[],"tests_run":[],"needs":[],"delegations":[]}}`
	enqueueDaemonWorkerJob(t, store, workflow.JobRequest{ID: "impl-1", Agent: "lead", Action: "implement", Repo: "owner/repo", Branch: "main", PullRequest: 1, Instructions: "do"})
	enqueueDaemonWorkerJob(t, store, workflow.JobRequest{ID: "impl-2", Agent: "lead", Action: "implement", Repo: "owner/repo", Branch: "main", PullRequest: 2, Instructions: "do"})

	barrier := newDeliverBarrier(2)
	adapter := &cliWorkerFakeAdapter{output: implementResult, onDeliver: barrier.hook}
	worker := poolSchedulerWorker(t, store, adapter, true)
	worker.ConfigHome = home // isolation enabled — but implement is not eligible

	runErr := make(chan error, 1)
	go func() { runErr <- runQueuedJobsForRepo(ctx, worker, 2, "", "") }()

	// Serialization drives the rendezvous: job1 enters Deliver alone; only after we
	// release it (freeing repo:<repo>) can job2 be dispatched and enter. If
	// parallelization had leaked, BOTH would enter before either release and peak
	// would be 2 — caught by the final assertion.
	waitEntered(t, barrier, "first implement job never reached Deliver")
	barrier.release <- struct{}{}
	waitEntered(t, barrier, "second implement job never ran after the first freed the checkout key")
	barrier.release <- struct{}{}
	select {
	case <-runErr:
	case <-time.After(20 * time.Second):
		t.Fatal("pool did not drain after releasing both implement jobs")
	}

	if peak := barrier.peakInflight(); peak != 1 {
		t.Fatalf("peak concurrent implement jobs = %d, want 1 (same-repo implement MUST serialize on repo:<repo>; #696 parallelization must not leak to mutating jobs)", peak)
	}
	// Both jobs shared the real checkout key — the reason they serialized.
	for _, id := range []string{"impl-1", "impl-2"} {
		job, err := store.GetJob(ctx, id)
		if err != nil {
			t.Fatalf("GetJob %s: %v", id, err)
		}
		if key := queuedJobCheckoutKey(ctx, store, job); key != "repo:owner/repo" {
			t.Fatalf("%s checkout key = %q, want the shared repo:owner/repo (implement is never auto-isolated)", id, key)
		}
	}
}

// TestReadOnlyIsolationWorktreeIsCommittedTip is assertion (C): correctness.
//
// The auto-detached worktree the pool allocates for a contended read-only job is
// a `git worktree add --detach` at the COMMITTED TIP of the base ref (#699). It
// must reflect the repo's committed HEAD — NOT the operator's dirty working tree
// (uncommitted edits, untracked files). Drives the REAL production allocator
// (jobWorker.allocatePoolIsolationWorktree) on a repo whose working tree has been
// dirtied, then inspects the worktree it created.
//
// Mirrors PR #699's committed-tip note validation: an isolated read-only worktree
// sees the committed ref, so gitignored/uncommitted paths are absent there (which
// is exactly why #699 appends the canonical-checkout note).
func TestReadOnlyIsolationWorktreeIsCommittedTip(t *testing.T) {
	ctx := context.Background()
	home := t.TempDir()
	checkout := createDaemonWorkerGitCheckout(t, "main")

	// Commit a marker file (this is the committed tip the worktree must reflect).
	const committed = "committed-v1\n"
	if err := os.WriteFile(filepath.Join(checkout, "feature.txt"), []byte(committed), 0o644); err != nil {
		t.Fatalf("write feature.txt: %v", err)
	}
	runDaemonWorkerGit(t, checkout, "add", "feature.txt")
	runDaemonWorkerGit(t, checkout, "commit", "-m", "add feature")

	// Dirty the working tree AFTER the commit: an uncommitted edit + an untracked
	// file. The committed-tip worktree must see NEITHER.
	if err := os.WriteFile(filepath.Join(checkout, "feature.txt"), []byte("DIRTY-uncommitted\n"), 0o644); err != nil {
		t.Fatalf("dirty feature.txt: %v", err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "untracked.txt"), []byte("scratch\n"), 0o644); err != nil {
		t.Fatalf("write untracked.txt: %v", err)
	}

	store := daemonWorkerStore(t)
	seedDaemonWorkerRepo(t, store, "owner/repo", checkout)
	seedDaemonWorkerAgent(t, store, "audit", runtime.ShellRuntime, "unused", []string{"ask"}, "owner/repo")
	enqueueDaemonWorkerJob(t, store, workflow.JobRequest{ID: "read-c", Agent: "audit", Action: "ask", Repo: "owner/repo", Branch: "main", PullRequest: 1, Instructions: "audit"})

	worker := defaultJobWorker(store, os.Stdout)
	worker.ConfigHome = home

	job, err := store.GetJob(ctx, "read-c")
	if err != nil {
		t.Fatalf("GetJob: %v", err)
	}
	payload, err := daemonJobPayload(job)
	if err != nil {
		t.Fatalf("payload: %v", err)
	}
	iso, ok := worker.allocatePoolIsolationWorktree(ctx, job, payload)
	if !ok {
		t.Fatal("allocatePoolIsolationWorktree returned ok=false; the isolation worktree was not created")
	}
	t.Cleanup(func() {
		_ = gitutil.Client{Dir: iso.repoCheckout}.RemoveWorktreeForce(context.WithoutCancel(ctx), iso.worktreePath)
	})

	// The isolated job's checkout key is the detached worktree, not repo:<repo>.
	if iso.checkoutKey == "repo:owner/repo" || !strings.HasPrefix(iso.checkoutKey, "worktree:") {
		t.Fatalf("isolation checkout key = %q, want a distinct worktree:<path> key", iso.checkoutKey)
	}

	// The worktree reflects the COMMITTED tip: feature.txt is the committed content
	// (not the dirty edit), and the untracked file is absent.
	got, err := os.ReadFile(filepath.Join(iso.worktreePath, "feature.txt"))
	if err != nil {
		t.Fatalf("read feature.txt from isolation worktree: %v", err)
	}
	if string(got) != committed {
		t.Fatalf("feature.txt in isolation worktree = %q, want committed tip %q (must NOT see the dirty working tree)", string(got), committed)
	}
	if _, err := os.Stat(filepath.Join(iso.worktreePath, "untracked.txt")); !os.IsNotExist(err) {
		t.Fatalf("untracked.txt present in isolation worktree (err=%v); the committed tip must omit untracked working-tree files", err)
	}

	// Sanity: the allocator also rewrote the job payload to point at the worktree,
	// and (per #699) appended the committed-tip note referencing the canonical
	// checkout — the same note read-only delegation fan-out carries.
	var rewritten workflow.JobPayload
	updated, err := store.GetJob(ctx, "read-c")
	if err != nil {
		t.Fatalf("GetJob after isolation: %v", err)
	}
	if err := json.Unmarshal([]byte(updated.Payload), &rewritten); err != nil {
		t.Fatalf("unmarshal rewritten payload: %v", err)
	}
	if rewritten.WorktreePath != iso.worktreePath {
		t.Fatalf("payload WorktreePath = %q, want %q", rewritten.WorktreePath, iso.worktreePath)
	}
	if !strings.Contains(rewritten.Instructions, "COMMITTED TIP") || !strings.Contains(rewritten.Instructions, checkout) {
		t.Fatalf("isolated payload missing committed-tip note pointing at %q: %q", checkout, rewritten.Instructions)
	}
}
