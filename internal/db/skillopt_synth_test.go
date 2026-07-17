package db

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
)

func TestSynthReviewItemRoundTripAuditFields(t *testing.T) {
	ctx := context.Background()
	store := openMemTestStore(t)
	want := SynthReviewItem{
		ID: "synth-planner-1", TemplateID: "planner", Repo: "acme/widget",
		Status: SynthItemStatusPending, Kind: "diversity", InjectedMemoryKey: "runbook#42",
		Context: "ctx", Question: "question", Rubric: "rubric",
		WeakAgent: "weak", StrongAgent: "strong", JudgeAgent: "judge",
		WeakAnswer: "weak answer", StrongAnswer: "strong answer",
		WeakScore: 0.7, StrongScore: 0.9, Gap: 0.2, Rounds: 1,
		Diagnostic: "too_easy", OutPath: "/tmp/item.json",
	}
	if err := store.CreateSynthReviewItem(ctx, want); err != nil {
		t.Fatalf("CreateSynthReviewItem: %v", err)
	}
	got, ok, err := store.GetSynthReviewItem(ctx, want.ID)
	if err != nil || !ok {
		t.Fatalf("GetSynthReviewItem: ok=%v err=%v", ok, err)
	}
	if got.Kind != want.Kind || got.InjectedMemoryKey != want.InjectedMemoryKey || got.Diagnostic != want.Diagnostic {
		t.Fatalf("audit fields = kind %q memory %q diagnostic %q", got.Kind, got.InjectedMemoryKey, got.Diagnostic)
	}
	items, err := store.ListSynthReviewItems(ctx, SynthItemStatusPending)
	if err != nil {
		t.Fatalf("ListSynthReviewItems: %v", err)
	}
	if len(items) != 1 || items[0].Kind != want.Kind || items[0].InjectedMemoryKey != want.InjectedMemoryKey {
		t.Fatalf("listed items = %+v", items)
	}
}

func TestSynthReviewItemAuditMigrationDefaults(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "pre-synth-audit.db")
	raw, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("sql.Open: %v", err)
	}
	raw.SetMaxOpenConns(1)
	raw.SetMaxIdleConns(1)
	if err := configureWritableSQLite(ctx, raw); err != nil {
		t.Fatalf("configure sqlite: %v", err)
	}
	store := &Store{db: raw}
	t.Cleanup(func() { _ = store.Close() })

	migrationIndex := -1
	for i, migration := range migrations {
		if strings.Contains(migration, "skillopt_synth_items ADD COLUMN kind") &&
			strings.Contains(migration, "ADD COLUMN injected_memory_key") {
			migrationIndex = i
			break
		}
	}
	if migrationIndex < 0 {
		t.Fatal("synth audit migration not found")
	}
	for i, migration := range migrations[:migrationIndex] {
		if err := store.applyMigration(ctx, i+1, migration); err != nil {
			t.Fatalf("apply pre-audit migration %d: %v", i+1, err)
		}
	}
	if _, err := raw.ExecContext(ctx, `INSERT INTO skillopt_synth_items(id) VALUES ('legacy')`); err != nil {
		t.Fatalf("seed legacy synth item: %v", err)
	}
	if err := store.applyMigration(ctx, migrationIndex+1, migrations[migrationIndex]); err != nil {
		t.Fatalf("apply synth audit migration: %v", err)
	}
	got, ok, err := store.GetSynthReviewItem(ctx, "legacy")
	if err != nil || !ok {
		t.Fatalf("GetSynthReviewItem legacy: ok=%v err=%v", ok, err)
	}
	if got.Kind != "" || got.InjectedMemoryKey != "" {
		t.Fatalf("legacy defaults = kind %q memory %q, want blanks", got.Kind, got.InjectedMemoryKey)
	}
}

func TestListSharedActiveConfirmedMemoriesVisibility(t *testing.T) {
	ctx := context.Background()
	store := openMemTestStore(t)
	shared := MemoryOwner{Kind: memoryOwnerKindShared, Ref: memorySharedOwnerRef}
	generalID := mustUpsert(t, store, ConfirmedMemory{
		Owner: shared, Scope: "general", Key: "general", Content: "shared general fact",
	})
	repoID := mustUpsert(t, store, ConfirmedMemory{
		Owner: shared, Repo: "acme/widget", Scope: "repo", Key: "repo", Content: "shared repo fact",
	})
	mustUpsert(t, store, ConfirmedMemory{
		Owner: shared, Repo: "acme/other", Scope: "repo", Key: "wrong-repo", Content: "wrong repo fact",
	})
	mustUpsert(t, store, ConfirmedMemory{
		Owner: agentOwner("builder"), Repo: "acme/widget", Scope: "repo", Key: "private", Content: "private fact",
	})
	supersededID := mustUpsert(t, store, ConfirmedMemory{
		Owner: shared, Repo: "acme/widget", Scope: "repo", Key: "superseded", Content: "superseded fact",
	})
	if _, err := store.db.ExecContext(ctx, `UPDATE confirmed_memories SET superseded_by = 999 WHERE id = ?`, supersededID); err != nil {
		t.Fatalf("mark superseded: %v", err)
	}
	retiredID := mustUpsert(t, store, ConfirmedMemory{
		Owner: shared, Repo: "acme/widget", Scope: "repo", Key: "retired", Content: "retired fact",
	})
	if err := store.RetireConfirmedMemory(ctx, retiredID, "stale"); err != nil {
		t.Fatalf("retire memory: %v", err)
	}

	got, err := store.ListSharedActiveConfirmedMemories(ctx, "acme/widget")
	if err != nil {
		t.Fatalf("ListSharedActiveConfirmedMemories: %v", err)
	}
	if len(got) != 2 || got[0].ID != generalID || got[1].ID != repoID {
		t.Fatalf("visible rows = %+v, want ids [%d %d]", got, generalID, repoID)
	}
}
