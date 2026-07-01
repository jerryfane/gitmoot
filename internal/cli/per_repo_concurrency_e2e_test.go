package cli

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/runtime"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

// Per-repo concurrency full-chain E2E (#576).
//
// The existing tests cover the two halves of the feature SEPARATELY: the
// loader (internal/config: TestLoadRepoConcurrency*) proves a config file
// parses into a RepoConcurrency, and TestJobWorkerResolveRepoSchedulerFalls...
// proves the resolver maps an in-memory override onto the scheduler limit.
// What neither pins is the WHOLE chain end to end: a real config FILE on disk
// -> live jobWorker config load -> runDaemonWorkerTick -> resolveRepoScheduler
// reading that file -> the pool scheduler actually capping in-flight deliveries
// for the named repo, while an unconfigured repo keeps the global limit.
//
// This E2E drives that chain with a real --home config file and asserts the
// peak concurrent deliveries split by repo. It is deterministic and offline
// (seeded store, shell runtime, no daemon process, no network). The peak is
// observed inside the fake adapter's Deliver, so ONLY a real concurrency cap
// can move it — the seeded jobs carry DISTINCT worktree keys, so nothing but
// the scheduler's limit can serialize them.

// repoConcurrencyE2EPeak writes N parallelizable queued jobs for repo (each with
// a distinct worktree key so their checkout keys differ) and drives the REAL
// daemon run path — runDaemonWorkerTick -> resolveRepoScheduler(configHome) ->
// runQueuedJobsForRepoPool — with a global workers limit of globalWorkers. It
// returns the observed peak concurrent deliveries. With no [repos.*] override
// for repo the peak rises to globalWorkers; a matching max_parallel caps it.
func repoConcurrencyE2EPeak(t *testing.T, configHome, repo string, jobs, globalWorkers int) int {
	t.Helper()
	ctx := context.Background()
	store := daemonWorkerStore(t)
	seedDaemonWorkerRepo(t, store, repo, t.TempDir())
	seedDaemonWorkerAgent(t, store, "audit", runtime.ShellRuntime, "unused", []string{"ask"}, repo)

	ids := make([]string, 0, jobs)
	for i := 0; i < jobs; i++ {
		id := "job-" + string(rune('a'+i))
		ids = append(ids, id)
		enqueueDaemonWorkerJob(t, store, workflow.JobRequest{
			ID:           id,
			Agent:        "audit",
			Action:       "ask",
			Repo:         repo,
			Branch:       "main",
			PullRequest:  i + 1,
			WorktreePath: filepath.Join(t.TempDir(), "wt-"+id),
		})
	}

	tracker := &poolConcurrencyTracker{}
	adapter := &cliWorkerFakeAdapter{output: poolSchedulerAskResult}
	adapter.onDeliver = tracker.span
	// Global scheduler: pool, workers=globalWorkers (would run all jobs at once).
	worker := poolSchedulerWorker(t, store, adapter, true)
	worker.ConfigHome = configHome
	worker.ConfigHomeExplicit = true

	if err := runDaemonWorkerTick(ctx, store, worker, globalWorkers, false, repo, "", io.Discard, time.Now().UTC()); err != nil {
		t.Fatalf("runDaemonWorkerTick(%s): %v", repo, err)
	}
	for _, id := range ids {
		job, err := store.GetJob(ctx, id)
		if err != nil {
			t.Fatalf("GetJob %s: %v", id, err)
		}
		if job.State != string(workflow.JobSucceeded) {
			t.Fatalf("%s state = %q, want succeeded", id, job.State)
		}
	}
	return tracker.peak()
}

// writePerRepoConcurrencyHome writes a real config file into a fresh temp daemon
// home that caps ONE repo via a TOML quoted key and leaves a second repo
// unconfigured, returning the raw --home. This is the on-disk artifact the whole
// chain must honor.
func writePerRepoConcurrencyHome(t *testing.T, cappedRepo string) string {
	t.Helper()
	home := t.TempDir()
	paths := config.PathsForHome(home)
	if err := config.Initialize(paths); err != nil {
		t.Fatalf("Initialize: %v", err)
	}
	body := "\n[repos.\"" + cappedRepo + "\"]\nmax_parallel = 1\n"
	if err := os.WriteFile(paths.ConfigFile, []byte(config.DefaultConfig(paths)+body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return home
}

// TestPerRepoConcurrencyFileCapsDaemonRepoEndToEnd is the #576 full-chain E2E:
// a config FILE on disk with [repos."owner/repo-capped"] max_parallel = 1 must,
// through the live daemon run path, serialize that repo's deliveries (peak 1)
// while a SECOND repo with no section keeps the global --workers/--parallel
// limit (peak 3). Same home, same job shape, distinct worktree keys — the ONLY
// thing that can make the capped repo serial is resolveRepoScheduler having read
// the file, so the peak split proves the whole chain.
//
// Guard against a false pass: if the pool cannot fan out at all (e.g. worktree
// keys collide, or the tracker never overlaps), the uncapped repo would ALSO
// read peak 1 and the split would be meaningless. So we assert the uncapped repo
// reaches peak 3 first — that establishes the mechanism CAN parallelize — and
// only then attribute the capped repo's peak 1 to the file override.
func TestPerRepoConcurrencyFileCapsDaemonRepoEndToEnd(t *testing.T) {
	const cappedRepo = "owner/repo-capped"
	const openRepo = "owner/repo-open"
	const globalWorkers = 3

	configHome := writePerRepoConcurrencyHome(t, cappedRepo)

	// Unconfigured repo: proves the global pool CAN run all jobs in parallel
	// under this exact home/config, so a low peak elsewhere is a real cap and
	// not a broken fan-out.
	if peak := repoConcurrencyE2EPeak(t, configHome, openRepo, globalWorkers, globalWorkers); peak != globalWorkers {
		t.Fatalf("unconfigured repo peak = %d, want %d (no [repos.*] section ⇒ global pool must fan out)", peak, globalWorkers)
	}

	// Configured repo: the on-disk max_parallel = 1 must serialize it.
	if peak := repoConcurrencyE2EPeak(t, configHome, cappedRepo, globalWorkers, globalWorkers); peak != 1 {
		t.Fatalf("capped repo peak = %d, want 1 (config-file max_parallel=1 must serialize via resolveRepoScheduler)", peak)
	}
}
