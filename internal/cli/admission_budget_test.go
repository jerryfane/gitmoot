package cli

import (
	"fmt"
	"sync"
	"testing"

	"github.com/jerryfane/gitmoot/internal/config"
)

func TestNewAdmissionBudgetNilWhenOff(t *testing.T) {
	// Both caps 0 (the default) ⇒ nil budget ⇒ feature off / byte-identical.
	if b := newAdmissionBudget(config.DefaultAdmissionPolicy()); b != nil {
		t.Fatalf("default (all-off) policy must yield a nil budget, got %+v", b)
	}
	// A nil budget always admits and never accounts.
	var b *admissionBudget
	if !b.Reserve("job-1", 10) {
		t.Fatalf("nil budget Reserve must always admit")
	}
	b.Release("job-1") // must not panic
	if rc, rm, ms, mm := b.snapshot(); rc != 0 || rm != 0 || ms != 0 || mm != 0 {
		t.Fatalf("nil budget snapshot = (%d, %v, %d, %v), want all zero", rc, rm, ms, mm)
	}
}

func TestAdmissionBudgetSessionCap(t *testing.T) {
	b := newAdmissionBudget(config.AdmissionPolicy{MaxConcurrentSessions: 2})
	if b == nil {
		t.Fatal("policy with a session cap must yield a non-nil budget")
	}
	if !b.Reserve("a", 0) || !b.Reserve("b", 0) {
		t.Fatalf("first %d reservations must be admitted", 2)
	}
	if b.Reserve("c", 0) {
		t.Fatalf("reservation beyond the session cap must be deferred")
	}
}

func TestAdmissionBudgetMemoryCap(t *testing.T) {
	// Memory gate only (no session cap): admit until the summed estimate would
	// exceed the cap, then defer.
	b := newAdmissionBudget(config.AdmissionPolicy{MaxMemoryGB: 1.0})
	if !b.Reserve("a", 0.6) {
		t.Fatalf("0.6 of 1.0GB must be admitted")
	}
	if !b.Reserve("b", 0.4) {
		t.Fatalf("0.6+0.4=1.0 exactly at cap must be admitted")
	}
	if b.Reserve("c", 0.01) {
		t.Fatalf("any further GB above the cap must be deferred")
	}
}

func TestAdmissionBudgetBothGates(t *testing.T) {
	// Count would allow it (1 of 5) but memory would overflow ⇒ defer; a job must
	// fit BOTH gates.
	b := newAdmissionBudget(config.AdmissionPolicy{MaxConcurrentSessions: 5, MaxMemoryGB: 1.0})
	if b.Reserve("big", 2.0) {
		t.Fatalf("a job within the count cap but over the memory cap must be deferred")
	}
	// Inverse: memory fits but the count cap is full ⇒ defer.
	c := newAdmissionBudget(config.AdmissionPolicy{MaxConcurrentSessions: 1, MaxMemoryGB: 100})
	if !c.Reserve("a", 0.1) {
		t.Fatalf("first job must be admitted")
	}
	if c.Reserve("b", 0.1) {
		t.Fatalf("a job within the memory cap but over the count cap must be deferred")
	}
}

func TestAdmissionBudgetReleaseFrees(t *testing.T) {
	b := newAdmissionBudget(config.AdmissionPolicy{MaxConcurrentSessions: 1, MaxMemoryGB: 1.0})
	if !b.Reserve("a", 0.9) {
		t.Fatalf("first job must be admitted")
	}
	if b.Reserve("b", 0.9) {
		t.Fatalf("second job must be deferred while the first holds the budget")
	}
	b.Release("a")
	if !b.Reserve("b", 0.9) {
		t.Fatalf("releasing the first job must re-admit the second")
	}
}

func TestAdmissionBudgetIdempotentRelease(t *testing.T) {
	b := newAdmissionBudget(config.AdmissionPolicy{MaxConcurrentSessions: 2, MaxMemoryGB: 1.0})
	if !b.Reserve("a", 0.9) {
		t.Fatalf("first job must be admitted")
	}
	// Double-release (e.g. pool reap + a panic/shutdown path) plus a release of a
	// never-reserved id must not credit the budget twice — exactly one slot frees.
	b.Release("a")
	b.Release("a")
	b.Release("never-reserved")
	if rc, rm, _, _ := b.snapshot(); rc != 0 || rm != 0 {
		t.Fatalf("after release(s) reserved = (%d, %v), want (0, 0)", rc, rm)
	}
	// The budget must not be permanently shrunk: both the count (2) and memory
	// (1.0GB) headroom are fully available again.
	if !b.Reserve("a", 0.9) || !b.Reserve("b", 0.05) {
		t.Fatalf("budget must not be permanently shrunk by a double-release")
	}
}

func TestAdmissionBudgetReReserveIsNoOp(t *testing.T) {
	// Re-reserving an already-in-flight job ID is an idempotent admit (no
	// double-counting), so a re-dispatch of the same job is safe.
	b := newAdmissionBudget(config.AdmissionPolicy{MaxConcurrentSessions: 1})
	if !b.Reserve("a", 0) {
		t.Fatalf("first reserve must admit")
	}
	if !b.Reserve("a", 0) {
		t.Fatalf("re-reserving the same in-flight job ID must be a no-op admit")
	}
	if rc, _, _, _ := b.snapshot(); rc != 1 {
		t.Fatalf("re-reserve must not double-count: reservedCount = %d, want 1", rc)
	}
}

func TestAdmissionBudgetConcurrent(t *testing.T) {
	const cap = 4
	b := newAdmissionBudget(config.AdmissionPolicy{MaxConcurrentSessions: cap, MaxMemoryGB: float64(cap)})

	var mu sync.Mutex
	live := 0
	maxLive := 0

	var wg sync.WaitGroup
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			id := fmt.Sprintf("job-%d", n)
			for attempt := 0; attempt < 50; attempt++ {
				if !b.Reserve(id, 1.0) {
					continue // deferred — retry, never over-admit
				}
				mu.Lock()
				live++
				if live > maxLive {
					maxLive = live
				}
				mu.Unlock()

				mu.Lock()
				live--
				mu.Unlock()
				b.Release(id)
				return
			}
		}(i)
	}
	wg.Wait()

	if maxLive > cap {
		t.Fatalf("observed %d concurrent admissions, exceeds cap %d (over-admission)", maxLive, cap)
	}
	if rc, rm, _, _ := b.snapshot(); rc != 0 || rm != 0 {
		t.Fatalf("after all releases reserved = (%d, %v), want (0, 0) — leaked reservation", rc, rm)
	}
}
