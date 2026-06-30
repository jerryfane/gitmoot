package cli

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/runtime"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// heartbeatLoopE2EHome builds an isolated home with an Initialized config + an
// open Store on that home's DB, so the write-side CLI (`agent heartbeat add`,
// which edits the config file), the production heartbeat enqueuer
// (newHeartbeatEnqueuer(store, home)), and the daemon worker tick all share the
// SAME config + store — exactly as the live daemon wires them. Never touches a
// real home.
func heartbeatLoopE2EHome(t *testing.T) (string, config.Paths, *db.Store) {
	t.Helper()
	home := t.TempDir()
	paths := config.PathsForHome(home)
	if err := config.Initialize(paths); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	store, err := db.Open(paths.Database)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return home, paths, store
}

// heartbeatShellResultScript is the SHELL-runtime session body the worker runs as
// `sh -c <script> gitmoot <prompt>`. It ignores its input and echoes a valid
// gitmoot_result with decision "approved" so the ask job runs to a TERMINAL
// succeeded state with NO LLM and NO network — fully deterministic offline.
const heartbeatShellResultScript = `printf '%s' '{"gitmoot_result":{"decision":"approved","summary":"heartbeat ran","findings":[],"changes_made":[],"tests_run":[],"needs":[],"delegations":[]}}'`

// countHeartbeatJobs returns every persisted job whose id carries the
// heartbeatJobID prefix, i.e. jobs the heartbeat scan actually enqueued (not the
// recording-fake jobs the unit tests use). The full-chain assertions count these
// to prove "exactly one enqueue per due window".
func countHeartbeatJobs(t *testing.T, store *db.Store) []db.Job {
	t.Helper()
	jobs, err := store.ListJobs(context.Background())
	if err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	out := make([]db.Job, 0, len(jobs))
	for _, j := range jobs {
		if strings.HasPrefix(j.ID, "heartbeat-") {
			out = append(out, j)
		}
	}
	return out
}

// TestHeartbeatLoopFullChainE2E is the full-chain, NO-LLM, deterministic E2E for
// agent heartbeat schedules (#533/#558 MVP + #564 finalize). The existing
// daemon_heartbeat_test.go calls runHeartbeatScanOnce DIRECTLY with a recording
// fake enqueuer; nothing drives the LIVE daemon chain end-to-end. This drives the
// REAL chain a daemon supervisor iteration runs:
//
//	write-side CLI (`agent heartbeat add`) writes the config section
//	  -> runHeartbeatScanOnce with the PRODUCTION enqueuer (real Mailbox) ENQUEUES a real queued job
//	    -> the REAL worker tick (runEnabledRepoWorkerTicks -> runQueuedJobsForRepo -> worker.run)
//	       CLAIMS + RUNS the job through the REAL shell adapter to a TERMINAL succeeded state
//	      -> heartbeat_state.next_due advances + last_status is recorded
//	        -> a daemon RESTART (fresh in-memory scan/enqueuer, SAME persisted store)
//	           does NOT re-fire the same due window (restart-safe dedup lives in the persisted next_due)
//
// The clock is INJECTED (now is the scan's parameter) so "due" is deterministic;
// the shell runtime keeps it offline (no real LLM / GitHub). It MUST go red if any
// link breaks: scan-not-enqueued, worker-didn't-run, next_due-not-advanced, or a
// restart double-fire.
func TestHeartbeatLoopFullChainE2E(t *testing.T) {
	ctx := context.Background()
	home, paths, store := heartbeatLoopE2EHome(t)

	// A real, managed (enabled + checked-out) repo whose origin matches owner/repo,
	// so the worker's REAL checkout validation (preflightDaemonRepoCheckout) passes
	// and the heartbeat repo guard treats it as runnable.
	checkout := createDaemonWorkerGitCheckout(t, "main")
	seedDaemonWorkerRepo(t, store, "owner/repo", checkout)

	// A SHELL-runtime agent whose session echoes a valid approved gitmoot_result.
	seedDaemonWorkerAgent(t, store, "maintainer", runtime.ShellRuntime, heartbeatShellResultScript, []string{"ask"}, "owner/repo")

	// Configure the heartbeat through the NEW write-side finalize CLI so the
	// SaveHeartbeat config writer is exercised (not a hand-written TOML block).
	var out, errBuf bytes.Buffer
	code := Run([]string{
		"agent", "heartbeat", "add", "maintainer", "beat",
		"--home", home,
		"--repo", "owner/repo",
		"--interval", "1h",
		"--prompt", "Review open issues and PRs.",
		"--max-concurrent", "1",
		"--enabled",
	}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("agent heartbeat add exit = %d, stderr=%s", code, errBuf.String())
	}
	// Sanity: the CLI actually wrote a loadable, enabled heartbeat section.
	heartbeats, err := config.LoadHeartbeats(paths)
	if err != nil || len(heartbeats) != 1 || !heartbeats[0].Enabled {
		t.Fatalf("heartbeat config not written by CLI: heartbeats=%+v err=%v", heartbeats, err)
	}

	interval := time.Hour
	now0 := time.Date(2026, 6, 30, 9, 0, 0, 0, time.UTC)

	// --- Tick 1: due (first-ever scan, zero next_due) ---------------------------
	// The PRODUCTION enqueuer (real Mailbox bound to the store), NOT a recording fake.
	enqueue := newHeartbeatEnqueuer(store, home)
	if err := runHeartbeatScanOnce(ctx, paths, store, enqueue, now0); err != nil {
		t.Fatalf("scan tick 1: %v", err)
	}
	jobs := countHeartbeatJobs(t, store)
	if len(jobs) != 1 {
		t.Fatalf("due heartbeat must enqueue exactly 1 job, got %d: %+v", len(jobs), jobs)
	}
	firstJobID := jobs[0].ID
	if jobs[0].State != string(workflow.JobQueued) {
		t.Fatalf("enqueued heartbeat job state = %q, want queued", jobs[0].State)
	}

	// next_due advanced exactly one interval + last_status recorded (#564 state).
	state, found, err := store.GetHeartbeatState(ctx, "maintainer", "beat")
	if err != nil || !found {
		t.Fatalf("expected heartbeat_state after enqueue: found=%v err=%v", found, err)
	}
	if !state.NextDueAt.Equal(now0.Add(interval)) {
		t.Fatalf("next_due = %s, want %s (one interval past now)", state.NextDueAt, now0.Add(interval))
	}
	if state.LastStatus != "enqueued" || state.LastJobID != firstJobID {
		t.Fatalf("heartbeat_state not recorded: %+v (want last_status=enqueued last_job=%s)", state, firstJobID)
	}

	// The REAL worker tick claims + runs the queued job through the shell adapter.
	worker := defaultJobWorker(store, io.Discard, home)
	if err := runEnabledRepoWorkerTicks(ctx, store, worker, 1, io.Discard, now0); err != nil {
		t.Fatalf("worker tick 1: %v", err)
	}
	ranJob, err := store.GetJob(ctx, firstJobID)
	if err != nil {
		t.Fatalf("GetJob(%s): %v", firstJobID, err)
	}
	if ranJob.State != string(workflow.JobSucceeded) {
		t.Fatalf("heartbeat job state = %q, want succeeded (worker did not run it to terminal)", ranJob.State)
	}

	// --- Restart-safe dedup: fresh in-memory scan/enqueuer, SAME store ----------
	// A "daemon restart" within the SAME due window must NOT re-fire: the dedup
	// lives in the PERSISTED next_due, which is still one interval ahead of now0.
	restartEnqueue := newHeartbeatEnqueuer(store, home)
	restartWorker := defaultJobWorker(store, io.Discard, home)
	if err := runHeartbeatScanOnce(ctx, paths, store, restartEnqueue, now0); err != nil {
		t.Fatalf("scan after restart: %v", err)
	}
	if err := runEnabledRepoWorkerTicks(ctx, store, restartWorker, 1, io.Discard, now0); err != nil {
		t.Fatalf("worker tick after restart: %v", err)
	}
	if got := countHeartbeatJobs(t, store); len(got) != 1 {
		t.Fatalf("restart double-fired the same due window: %d heartbeat jobs, want 1: %+v", len(got), got)
	}

	// --- Tick 2: due again at now0+interval; the recurring loop fires once more --
	// Proves next_due was advanced CORRECTLY (not stuck firing every tick, not
	// wedged never firing): a single new job is enqueued and run to terminal.
	now1 := now0.Add(interval)
	if err := runHeartbeatScanOnce(ctx, paths, store, restartEnqueue, now1); err != nil {
		t.Fatalf("scan tick 2: %v", err)
	}
	jobs2 := countHeartbeatJobs(t, store)
	if len(jobs2) != 2 {
		t.Fatalf("second due window must enqueue one more job (total 2), got %d: %+v", len(jobs2), jobs2)
	}
	if err := runEnabledRepoWorkerTicks(ctx, store, restartWorker, 1, io.Discard, now1); err != nil {
		t.Fatalf("worker tick 2: %v", err)
	}
	state2, _, err := store.GetHeartbeatState(ctx, "maintainer", "beat")
	if err != nil {
		t.Fatalf("GetHeartbeatState tick 2: %v", err)
	}
	if !state2.NextDueAt.Equal(now1.Add(interval)) {
		t.Fatalf("next_due after tick 2 = %s, want %s", state2.NextDueAt, now1.Add(interval))
	}
	// Both heartbeat jobs reached terminal succeeded via the real worker.
	for _, j := range jobs2 {
		ran, err := store.GetJob(ctx, j.ID)
		if err != nil {
			t.Fatalf("GetJob(%s): %v", j.ID, err)
		}
		if ran.State != string(workflow.JobSucceeded) {
			t.Fatalf("heartbeat job %s state = %q, want succeeded", j.ID, ran.State)
		}
	}
}

// TestHeartbeatLoopOffByDefaultE2E is the off-by-default invariant on the LIVE
// chain: with NO heartbeat configured, a full supervisor iteration (production
// scan + real worker tick) enqueues nothing and writes no heartbeat_state — the
// daemon loop is byte-for-byte inert for users without heartbeats.
func TestHeartbeatLoopOffByDefaultE2E(t *testing.T) {
	ctx := context.Background()
	home, paths, store := heartbeatLoopE2EHome(t)
	checkout := createDaemonWorkerGitCheckout(t, "main")
	seedDaemonWorkerRepo(t, store, "owner/repo", checkout)
	seedDaemonWorkerAgent(t, store, "maintainer", runtime.ShellRuntime, heartbeatShellResultScript, []string{"ask"}, "owner/repo")
	// NOTE: no `agent heartbeat add` — the config has zero heartbeat sections.

	now := time.Date(2026, 6, 30, 9, 0, 0, 0, time.UTC)
	enqueue := newHeartbeatEnqueuer(store, home)
	if err := runHeartbeatScanOnce(ctx, paths, store, enqueue, now); err != nil {
		t.Fatalf("off-by-default scan: %v", err)
	}
	worker := defaultJobWorker(store, io.Discard, home)
	if err := runEnabledRepoWorkerTicks(ctx, store, worker, 1, io.Discard, now); err != nil {
		t.Fatalf("off-by-default worker tick: %v", err)
	}
	if jobs := countHeartbeatJobs(t, store); len(jobs) != 0 {
		t.Fatalf("off-by-default enqueued %d heartbeat jobs, want 0: %+v", len(jobs), jobs)
	}
	if _, found, err := store.GetHeartbeatState(ctx, "maintainer", "beat"); err != nil || found {
		t.Fatalf("off-by-default wrote heartbeat_state: found=%v err=%v", found, err)
	}
}
