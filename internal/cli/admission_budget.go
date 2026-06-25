package cli

import (
	"sync"

	"github.com/jerryfane/gitmoot/internal/config"
)

// admissionBudget is the process-global, memory-aware concurrency gate the daemon
// consults at the single dispatch decision in each scheduler (issue #365). It is
// a stricter SECOND gate layered on --workers/pool and the per-repo checkout /
// runtime-session locks: a job is admitted only if it fits BOTH the session-count
// cap AND the summed-RAM cap; otherwise it is left queued (deferred), to be
// retried on a later dispatch pass/tick.
//
// A nil *admissionBudget means the feature is OFF — callers MUST treat a nil
// receiver as "always admit, never accounts", so default (no [admission] config)
// scheduling is byte-identical. Both Reserve and Release are no-ops on a nil
// receiver.
//
// Concurrency: reserve must check-and-add under ONE lock so the count cap and the
// memory cap are enforced atomically together (two independent atomics cannot).
// A single Mutex around a tiny critical section is race-clean and negligible at
// daemon tick cadence. Reservations are keyed by job ID so Release is idempotent
// (a double release — e.g. pool reap plus a panic-recovery path — never
// double-credits the budget) and a re-reserve of the same in-flight job ID is a
// no-op success.
type admissionBudget struct {
	mu            sync.Mutex
	maxSessions   int     // 0 = session-count gate off
	maxMemoryGB   float64 // 0 = memory gate off
	reservedCount int
	reservedMemGB float64
	reservations  map[string]float64 // jobID -> reserved GB (idempotency)
}

// newAdmissionBudget returns an *admissionBudget for the policy, or nil when both
// caps are 0/unset (the feature is off ⇒ byte-identical default behavior). The
// returned budget enforces only the caps that are set (>0); a 0 cap is ignored.
func newAdmissionBudget(policy config.AdmissionPolicy) *admissionBudget {
	if !policy.Enabled() {
		return nil
	}
	return &admissionBudget{
		maxSessions:  policy.MaxConcurrentSessions,
		maxMemoryGB:  policy.MaxMemoryGB,
		reservations: map[string]float64{},
	}
}

// Reserve atomically admits the job, reserving one session slot and estGB of RAM,
// and reports whether it was admitted. A nil budget (feature off) always admits
// without accounting. A job ID already reserved (in flight) is admitted again as
// a no-op so a re-dispatch is safe. Otherwise the job is admitted only if BOTH
// the session-count cap and the memory cap (each, when set) would still hold; a
// job that does not fit is NOT reserved and false is returned (the caller leaves
// it queued).
func (b *admissionBudget) Reserve(jobID string, estGB float64) bool {
	if b == nil {
		return true
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if _, ok := b.reservations[jobID]; ok {
		return true
	}
	if b.maxSessions > 0 && b.reservedCount+1 > b.maxSessions {
		return false
	}
	if b.maxMemoryGB > 0 && b.reservedMemGB+estGB > b.maxMemoryGB {
		return false
	}
	b.reservedCount++
	b.reservedMemGB += estGB
	b.reservations[jobID] = estGB
	return true
}

// Release frees the session slot and RAM a prior Reserve held for jobID. It is
// idempotent: releasing a job ID that is not currently reserved (already
// released, or never admitted) is a no-op, so a double release from the pool reap
// plus a panic/shutdown path can never shrink the budget below its true in-flight
// usage. A nil budget is a no-op.
func (b *admissionBudget) Release(jobID string) {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	estGB, ok := b.reservations[jobID]
	if !ok {
		return
	}
	b.reservedCount--
	b.reservedMemGB -= estGB
	delete(b.reservations, jobID)
}

// snapshot returns the currently reserved session count and RAM (GB) plus the
// configured caps, for daemon status observability. A nil budget reports all
// zeros (feature off).
func (b *admissionBudget) snapshot() (reservedCount int, reservedMemGB float64, maxSessions int, maxMemoryGB float64) {
	if b == nil {
		return 0, 0, 0, 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.reservedCount, b.reservedMemGB, b.maxSessions, b.maxMemoryGB
}
