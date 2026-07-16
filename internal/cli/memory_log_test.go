package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/gitmoot/gitmoot/internal/config"
	"github.com/gitmoot/gitmoot/internal/db"
)

func TestMemoryLogFiltersBiographyAndJSONShape(t *testing.T) {
	home, store := memoryTestHome(t)
	ctx := context.Background()
	owner := db.MemoryOwner{Kind: "agent", Ref: "builder"}
	id, err := store.UpsertConfirmedMemory(ctx, db.ConfirmedMemory{Owner: owner, Repo: "acme/widget", Scope: "repo",
		Key: "runner", Content: "before", SourceJob: "job-1"}, db.WithConfirmedMemoryEvent(db.MemoryEventConfirmed, "job-1"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertConfirmedMemory(ctx, db.ConfirmedMemory{Owner: owner, Repo: "acme/widget", Scope: "repo",
		Key: "runner", Content: "after", SourceJob: "job-2"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.UpsertConfirmedMemory(ctx, db.ConfirmedMemory{Owner: db.MemoryOwner{Kind: "agent", Ref: "other"},
		Repo: "acme/other", Scope: "repo", Key: "other", Content: "other"}); err != nil {
		t.Fatal(err)
	}

	code, out, errOut := runMemoryCapture(t, "log", "--home", home, "--agent", "builder", "--repo", "acme/widget", "--kind", "updated", "--json")
	if code != 0 {
		t.Fatalf("filtered log exit=%d stderr=%s", code, errOut)
	}
	var filtered []memoryLogEntry
	if err := json.Unmarshal([]byte(out), &filtered); err != nil {
		t.Fatal(err)
	}
	if len(filtered) != 1 || filtered[0].Kind != db.MemoryEventUpdated || filtered[0].MemoryID != id || filtered[0].Actor != "job-2" {
		t.Fatalf("filtered events = %+v", filtered)
	}
	var detail map[string]any
	if err := json.Unmarshal(filtered[0].Detail, &detail); err != nil || detail["before"] != "before" {
		t.Fatalf("updated detail = %s err=%v", filtered[0].Detail, err)
	}

	code, out, errOut = runMemoryCapture(t, "log", "--home", home, "--id", strconv.FormatInt(id, 10), "--json")
	if code != 0 {
		t.Fatalf("biography exit=%d stderr=%s", code, errOut)
	}
	var biography []memoryLogEntry
	if err := json.Unmarshal([]byte(out), &biography); err != nil {
		t.Fatal(err)
	}
	if len(biography) != 2 || biography[0].Kind != db.MemoryEventConfirmed || biography[1].Kind != db.MemoryEventUpdated || biography[0].ID >= biography[1].ID {
		t.Fatalf("biography = %+v", biography)
	}
}

func TestMemoryLogBackfillCLIIdempotent(t *testing.T) {
	home, store := memoryTestHome(t)
	ctx := context.Background()
	id, err := store.UpsertConfirmedMemory(ctx, db.ConfirmedMemory{Owner: db.MemoryOwner{Kind: "agent", Ref: "builder"}, Key: "legacy", Content: "legacy"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := sql.Open("sqlite", config.PathsForHome(home).Database)
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	if _, err := raw.ExecContext(ctx, `UPDATE confirmed_memories SET retired_at='2026-07-15T00:00:00Z', retired_reason='legacy reason' WHERE id=?; DELETE FROM memory_events`, id); err != nil {
		t.Fatal(err)
	}
	code, out, errOut := runMemoryCapture(t, "log", "backfill", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("backfill exit=%d stderr=%s", code, errOut)
	}
	var first memoryLogBackfillOutput
	if err := json.Unmarshal([]byte(out), &first); err != nil || first.Created != 2 {
		t.Fatalf("first=%+v err=%v", first, err)
	}
	code, out, errOut = runMemoryCapture(t, "log", "backfill", "--home", home, "--json")
	if code != 0 {
		t.Fatalf("second exit=%d stderr=%s", code, errOut)
	}
	var second memoryLogBackfillOutput
	if err := json.Unmarshal([]byte(out), &second); err != nil || second.Created != 0 || second.Skipped != 1 {
		t.Fatalf("second=%+v err=%v", second, err)
	}
}

func TestMemoryLogLifecycleE2E(t *testing.T) {
	home, store := memoryTestHome(t)
	sourceDir := t.TempDir()
	path := filepath.Join(sourceDir, "runbook.md")
	write := func(content string) {
		t.Helper()
		if err := os.WriteFile(path, []byte("# Runner\n\n"+content+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("The build runner uses the first queue and retries transient failures.")
	if code, _, errOut := runMemoryCapture(t, "ingest", path, "--home", home, "--agent", "builder", "--repo", "acme/widget", "--json"); code != 0 {
		t.Fatalf("ingest 1 exit=%d stderr=%s", code, errOut)
	}
	observations, err := store.ListMemoryObservationsWithConfirmation(context.Background(), "builder", "ingest:")
	if err != nil || len(observations) == 0 {
		t.Fatalf("observations=%+v err=%v", observations, err)
	}
	firstObs := observations[0].ID
	if code, _, errOut := runMemoryCapture(t, "confirm", "--home", home, "--yes", "--json", strconv.FormatInt(firstObs, 10)); code != 0 {
		t.Fatalf("confirm 1 exit=%d stderr=%s", code, errOut)
	}

	write("The build runner uses the second queue and retries transient failures safely.")
	if code, _, errOut := runMemoryCapture(t, "ingest", path, "--home", home, "--agent", "builder", "--repo", "acme/widget", "--json"); code != 0 {
		t.Fatalf("ingest 2 exit=%d stderr=%s", code, errOut)
	}
	observations, err = store.ListMemoryObservationsWithConfirmation(context.Background(), "builder", "ingest:")
	if err != nil || len(observations) < 2 {
		t.Fatalf("observations after edit=%+v err=%v", observations, err)
	}
	secondObs := observations[0].ID
	if code, _, errOut := runMemoryCapture(t, "confirm", "--home", home, "--yes", "--json", strconv.FormatInt(secondObs, 10)); code != 0 {
		t.Fatalf("confirm 2 exit=%d stderr=%s", code, errOut)
	}
	if code, _, errOut := runMemoryCapture(t, "retire", "--home", home, "--provenance-prefix", "ingest:", "--yes", "--json"); code != 0 {
		t.Fatalf("retire exit=%d stderr=%s", code, errOut)
	}
	if code, _, errOut := runMemoryCapture(t, "confirm", "--home", home, "--yes", "--json", strconv.FormatInt(secondObs, 10)); code != 0 {
		t.Fatalf("unretire exit=%d stderr=%s", code, errOut)
	}
	code, out, errOut := runMemoryCapture(t, "log", "--home", home, "--key", observations[0].Key, "--json")
	if code != 0 {
		t.Fatalf("log exit=%d stderr=%s", code, errOut)
	}
	var events []memoryLogEntry
	if err := json.Unmarshal([]byte(out), &events); err != nil {
		t.Fatal(err)
	}
	kinds := make([]string, 0, len(events))
	for i := len(events) - 1; i >= 0; i-- {
		kinds = append(kinds, events[i].Kind)
	}
	if got := strings.Join(kinds, ","); got != "confirmed,updated,retired,unretired" {
		t.Fatalf("journal kinds=%s events=%+v", got, events)
	}
}
