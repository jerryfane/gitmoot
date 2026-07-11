package db

import (
	"context"
	"strings"
	"testing"
)

func TestGroomStaleVerdictCacheRoundTrip(t *testing.T) {
	ctx := context.Background()
	store := openMemTestStore(t)
	if _, ok, err := store.GetGroomStaleVerdict(ctx, "missing"); err != nil || ok {
		t.Fatalf("missing verdict: ok=%v err=%v", ok, err)
	}
	if err := store.StoreGroomStaleVerdict(ctx, GroomStaleVerdict{
		ContentHash: "hash-expired", Verdict: "expired", Model: "codex/default",
	}); err != nil {
		t.Fatalf("store expired verdict: %v", err)
	}
	expired, ok, err := store.GetGroomStaleVerdict(ctx, "hash-expired")
	if err != nil || !ok || expired.Verdict != "expired" || expired.Model != "codex/default" || expired.CreatedAt == "" {
		t.Fatalf("expired round trip = %+v ok=%v err=%v", expired, ok, err)
	}
	const residue = "only one submission may be in flight per team"
	if err := store.StoreGroomStaleVerdict(ctx, GroomStaleVerdict{
		ContentHash: "hash-residue", Verdict: "contains_durable_residue", Residue: residue, Model: "claude/sonnet",
	}); err != nil {
		t.Fatalf("store residue verdict: %v", err)
	}
	got, ok, err := store.GetGroomStaleVerdict(ctx, "hash-residue")
	if err != nil || !ok || got.Residue != residue || got.Verdict != "contains_durable_residue" {
		t.Fatalf("residue round trip = %+v ok=%v err=%v", got, ok, err)
	}
	if err := store.StoreGroomStaleVerdict(ctx, GroomStaleVerdict{ContentHash: "hash-expired", Verdict: "still_relevant", Model: "changed"}); err != nil {
		t.Fatalf("repeat store: %v", err)
	}
	unchanged, _, _ := store.GetGroomStaleVerdict(ctx, "hash-expired")
	if unchanged.Verdict != "expired" || unchanged.Model != "codex/default" {
		t.Fatalf("first verdict was replaced: %+v", unchanged)
	}
}

func TestStoreGroomStaleVerdictValidation(t *testing.T) {
	store := openMemTestStore(t)
	for _, verdict := range []GroomStaleVerdict{
		{Verdict: "expired"},
		{ContentHash: "h", Verdict: "unknown"},
		{ContentHash: "h", Verdict: "contains_durable_residue"},
		{ContentHash: "h", Verdict: "still_relevant", Residue: "unexpected"},
	} {
		if err := store.StoreGroomStaleVerdict(context.Background(), verdict); err == nil {
			t.Fatalf("expected invalid verdict %+v to fail", verdict)
		}
	}
}

func TestGroomStaleExpiredRetiresThroughHousePath(t *testing.T) {
	ctx := context.Background()
	store := openMemTestStore(t)
	id, err := store.UpsertConfirmedMemory(ctx, ConfirmedMemory{
		Owner: MemoryOwner{Kind: "agent", Ref: "lead"}, Repo: "acme/widget", Scope: "repo",
		Key: "stale-baton", Content: "## SUBMITTED 2026-06-10 — AWAITING SCORE",
	})
	if err != nil {
		t.Fatalf("seed stale baton: %v", err)
	}
	result, err := store.ApplyGroomRetirements(ctx, []GroomRetire{{ID: id, Reason: "groom-stale:2026-07-11"}})
	if err != nil || len(result.Retired) != 1 || result.Retired[0] != id {
		t.Fatalf("retire result = %+v err=%v", result, err)
	}
	var retiredAt, reason string
	if err := store.db.QueryRowContext(ctx, `SELECT retired_at, retired_reason FROM confirmed_memories WHERE id = ?`, id).Scan(&retiredAt, &reason); err != nil {
		t.Fatalf("read retired row: %v", err)
	}
	if retiredAt == "" || reason != "groom-stale:2026-07-11" {
		t.Fatalf("retired_at=%q reason=%q", retiredAt, reason)
	}
	var ftsCount int
	if err := store.db.QueryRowContext(ctx, `SELECT count(*) FROM confirmed_memories_fts WHERE rowid = ?`, id).Scan(&ftsCount); err != nil || ftsCount != 0 {
		t.Fatalf("fts count=%d err=%v", ftsCount, err)
	}
	second, err := store.ApplyGroomRetirements(ctx, []GroomRetire{{ID: id, Reason: "groom-stale:2026-07-11"}})
	if err != nil || len(second.Skipped) != 1 || !strings.Contains(reason, "groom-stale") {
		t.Fatalf("idempotent retire = %+v err=%v", second, err)
	}
}
