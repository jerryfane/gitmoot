package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/runtime"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

func TestClassifyOperationalBlocker(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name      string
		err       error
		wantOK    bool
		wantClass blockerClass
	}{
		{
			name:      "codex usage limit without parseable reset",
			err:       errors.New("delivery failed: You've hit your usage limit. try again at Jun 14th: exit status 1"),
			wantOK:    true,
			wantClass: blockerClassRuntimeQuota,
		},
		{
			name:      "http 429 with relative reset",
			err:       errors.New("delivery failed: HTTP 429 Too Many Requests: rate limit reached; try again in 30 seconds: exit status 1"),
			wantOK:    true,
			wantClass: blockerClassRuntimeQuota,
		},
		{
			name:      "typed claude auth sentinel",
			err:       fmt.Errorf("delivery failed: %w", runtime.ErrClaudeAuthFailed),
			wantOK:    true,
			wantClass: blockerClassRuntimeAuth,
		},
		{
			name:      "textual 401 authentication_error",
			err:       errors.New("delivery failed: Failed to authenticate. API Error: 401 authentication_error: invalid x-api-key: exit status 1"),
			wantOK:    true,
			wantClass: blockerClassRuntimeAuth,
		},
		{
			name:   "unclassified runtime error stays terminal",
			err:    errors.New("delivery failed: boom: exit status 2"),
			wantOK: false,
		},
		{
			name:   "nil error",
			err:    nil,
			wantOK: false,
		},
		{
			name:   "context cancellation is never a blocker",
			err:    fmt.Errorf("delivery failed: %w", context.Canceled),
			wantOK: false,
		},
		{
			name:   "run deadline is never a blocker",
			err:    fmt.Errorf("delivery failed: %w", context.DeadlineExceeded),
			wantOK: false,
		},
		{
			// A BlockedError whose reason mentions quota is still an engine-routed
			// outcome, not an operational blocker.
			name:   "engine BlockedError is never a blocker",
			err:    workflow.BlockedError{Reason: "task blocked: quota policy"},
			wantOK: false,
		},
		{
			name:   "escalate-human pause is never a blocker",
			err:    workflow.AwaitingHumanError{Reason: "rate limit decision needs a human"},
			wantOK: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := classifyOperationalBlocker(tc.err, now)
			if ok != tc.wantOK {
				t.Fatalf("classifyOperationalBlocker ok = %v, want %v (got %+v)", ok, tc.wantOK, got)
			}
			if !tc.wantOK {
				return
			}
			if got.Class != tc.wantClass {
				t.Fatalf("class = %q, want %q", got.Class, tc.wantClass)
			}
			if !got.RetryAt.After(now) {
				t.Fatalf("RetryAt %s is not after now %s", got.RetryAt, now)
			}
			if got.Detail == "" {
				t.Fatal("Detail is empty")
			}
		})
	}
}

func TestClassifyOperationalBlockerQuotaUsesParsedReset(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	got, ok := classifyOperationalBlocker(errors.New("delivery failed: rate limit reached; try again in 30 seconds"), now)
	if !ok {
		t.Fatal("expected quota classification")
	}
	min := now.Add(30 * time.Second)
	max := min.Add(30 * time.Second) // parsed delay + max jitter headroom
	if got.RetryAt.Before(min) || got.RetryAt.After(max) {
		t.Fatalf("RetryAt %s outside [%s, %s]", got.RetryAt, min, max)
	}
}

func TestClassifyOperationalBlockerAuthUsesFixedDelay(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	got, ok := classifyOperationalBlocker(fmt.Errorf("delivery failed: %w", runtime.ErrClaudeAuthFailed), now)
	if !ok {
		t.Fatal("expected auth classification")
	}
	if want := now.Add(authBlockerRetryDelay); !got.RetryAt.Equal(want) {
		t.Fatalf("RetryAt = %s, want %s", got.RetryAt, want)
	}
}

func TestParseQuotaResetDelay(t *testing.T) {
	tests := []struct {
		name string
		text string
		want time.Duration
	}{
		{"codex relative seconds", "Rate limit reached. Please try again in 32 seconds.", 32 * time.Second},
		{"relative minutes", "usage limit; retry in 5 minutes", 5 * time.Minute},
		{"retry-after header style", "429 Too Many Requests. Retry-After: 120", 120 * time.Second},
		{"unparseable absolute date falls back", "You've hit your usage limit. try again at Jun 14th", quotaBlockerFallbackDelay},
		{"no hint falls back", "quota exceeded", quotaBlockerFallbackDelay},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseQuotaResetDelay(tc.text); got != tc.want {
				t.Fatalf("parseQuotaResetDelay(%q) = %s, want %s", tc.text, got, tc.want)
			}
		})
	}
}

func TestParseQuotaResetDelayClaudeEpoch(t *testing.T) {
	// The Claude CLI usage-limit shape carries a unix epoch after a pipe:
	// "Claude AI usage limit reached|<epoch>". Use a future epoch and accept the
	// small elapsed-time skew of time.Until.
	epoch := time.Now().Add(90 * time.Second).Unix()
	got := parseQuotaResetDelay(fmt.Sprintf("Claude AI usage limit reached|%d", epoch))
	if got < 80*time.Second || got > 90*time.Second {
		t.Fatalf("parseQuotaResetDelay epoch = %s, want ~90s", got)
	}
	// A past epoch must never yield a non-positive hold.
	if got := parseQuotaResetDelay("Claude AI usage limit reached|1000000000"); got != quotaBlockerFallbackDelay {
		t.Fatalf("past epoch = %s, want fallback %s", got, quotaBlockerFallbackDelay)
	}
}

func TestQueuedJobBlockerHeld(t *testing.T) {
	now := time.Date(2026, 7, 1, 12, 0, 0, 0, time.UTC)
	job := func(payload string) db.Job {
		return db.Job{ID: "job-1", State: string(workflow.JobQueued), Payload: payload}
	}
	if queuedJobBlockerHeld(job(`{"repo":"o/r","task_id":"","task_title":"","sender":"","instructions":"","constraints":null,"branch":"main","pull_request":1}`), now) {
		t.Fatal("job without blocker fields must never be held")
	}
	future := now.Add(time.Minute).Format(time.RFC3339Nano)
	if !queuedJobBlockerHeld(job(fmt.Sprintf(`{"repo":"o/r","task_id":"","task_title":"","sender":"","instructions":"","constraints":null,"branch":"main","pull_request":1,"blocker_retry_at":%q}`, future)), now) {
		t.Fatal("job inside its hold window must be held")
	}
	past := now.Add(-time.Minute).Format(time.RFC3339Nano)
	if queuedJobBlockerHeld(job(fmt.Sprintf(`{"repo":"o/r","task_id":"","task_title":"","sender":"","instructions":"","constraints":null,"branch":"main","pull_request":1,"blocker_retry_at":%q}`, past)), now) {
		t.Fatal("job past its hold window must be released")
	}
	if queuedJobBlockerHeld(job(fmt.Sprintf(`{"repo":"o/r","task_id":"","task_title":"","sender":"","instructions":"","constraints":null,"branch":"main","pull_request":1,"blocker_retry_at":%q}`, "not-a-time")), now) {
		t.Fatal("malformed retry-at must never strand a job")
	}
	if queuedJobBlockerHeld(db.Job{ID: "job-1", State: string(workflow.JobQueued), Payload: "{"}, now) {
		t.Fatal("malformed payload must never strand a job")
	}
}

func TestRestorePreIsolationPayloadForDeferredJob(t *testing.T) {
	ctx := context.Background()
	store := daemonWorkerStore(t)
	seedDaemonWorkerAgent(t, store, "iso", runtime.ShellRuntime, "true", []string{"ask"}, "owner/repo")

	seed := func(t *testing.T, id string, mutate func(*workflow.JobPayload)) (string, workflow.JobPayload) {
		t.Helper()
		enqueueDaemonWorkerJob(t, store, workflow.JobRequest{
			ID: id, Agent: "iso", Action: "ask", Repo: "owner/repo", Branch: "main", PullRequest: 1,
		})
		job, err := store.GetJob(ctx, id)
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		before := job.Payload
		payload, err := daemonJobPayload(job)
		if err != nil {
			t.Fatalf("parse payload: %v", err)
		}
		mutate(&payload)
		encoded, err := json.Marshal(payload)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		if err := store.UpdateJobPayload(ctx, id, string(encoded)); err != nil {
			t.Fatalf("UpdateJobPayload: %v", err)
		}
		return before, payload
	}

	t.Run("deferred isolation job restores worktree and keeps hold", func(t *testing.T) {
		hold := time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
		before, _ := seed(t, "job-iso-deferred", func(p *workflow.JobPayload) {
			p.WorktreePath = "/reaped/isolation/path"
			p.BlockerClass = string(blockerClassRuntimeQuota)
			p.BlockerAttempts = 1
			p.BlockerRetryAt = hold
		})
		restorePreIsolationPayloadForDeferredJob(ctx, store, "job-iso-deferred", before)
		_, got := func() (db.Job, workflow.JobPayload) {
			job, err := store.GetJob(ctx, "job-iso-deferred")
			if err != nil {
				t.Fatalf("GetJob: %v", err)
			}
			payload, err := daemonJobPayload(job)
			if err != nil {
				t.Fatalf("parse payload: %v", err)
			}
			return job, payload
		}()
		if got.WorktreePath != "" {
			t.Fatalf("worktree path = %q, want restored empty pre-isolation value", got.WorktreePath)
		}
		if got.BlockerClass != string(blockerClassRuntimeQuota) || got.BlockerAttempts != 1 || got.BlockerRetryAt != hold {
			t.Fatalf("blocker hold fields were not carried over: %+v", got)
		}
	})

	t.Run("terminal job is untouched", func(t *testing.T) {
		before, mutated := seed(t, "job-iso-failed", func(p *workflow.JobPayload) {
			p.WorktreePath = "/reaped/isolation/path"
			p.BlockerRetryAt = time.Now().UTC().Add(time.Minute).Format(time.RFC3339Nano)
		})
		if _, err := store.TransitionJobStateWithEvent(ctx, "job-iso-failed", string(workflow.JobQueued), string(workflow.JobFailed), db.JobEvent{JobID: "job-iso-failed", Kind: "failed", Message: "boom"}); err != nil {
			t.Fatalf("transition: %v", err)
		}
		restorePreIsolationPayloadForDeferredJob(ctx, store, "job-iso-failed", before)
		job, err := store.GetJob(ctx, "job-iso-failed")
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		payload, err := daemonJobPayload(job)
		if err != nil {
			t.Fatalf("parse payload: %v", err)
		}
		if payload.WorktreePath != mutated.WorktreePath {
			t.Fatalf("terminal job payload was rewritten: %+v", payload)
		}
	})

	t.Run("queued job without hold is untouched", func(t *testing.T) {
		before, mutated := seed(t, "job-iso-nohold", func(p *workflow.JobPayload) {
			p.WorktreePath = "/reaped/isolation/path"
		})
		restorePreIsolationPayloadForDeferredJob(ctx, store, "job-iso-nohold", before)
		job, err := store.GetJob(ctx, "job-iso-nohold")
		if err != nil {
			t.Fatalf("GetJob: %v", err)
		}
		payload, err := daemonJobPayload(job)
		if err != nil {
			t.Fatalf("parse payload: %v", err)
		}
		if payload.WorktreePath != mutated.WorktreePath {
			t.Fatalf("hold-less queued job payload was rewritten: %+v", payload)
		}
	})
}
