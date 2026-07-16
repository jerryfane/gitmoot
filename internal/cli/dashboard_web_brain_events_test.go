package cli

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gitmoot/gitmoot/internal/db"
)

func TestWebDataSourceBrainEventsPagingEmptyAndChangeCursor(t *testing.T) {
	home, store := memoryTestHome(t)
	ds := &webDataSource{home: home}
	ctx := context.Background()
	empty, err := ds.BrainEvents(ctx, 0, 2)
	if err != nil || empty.Events == nil || len(empty.Events) != 0 || empty.NextCursor != 0 {
		t.Fatalf("empty brain events = %+v err=%v", empty, err)
	}
	before, err := ds.ChangeCursor(ctx)
	if err != nil {
		t.Fatal(err)
	}
	for i, key := range []string{"one", "two", "three"} {
		if _, err := store.UpsertConfirmedMemory(ctx, db.ConfirmedMemory{Owner: db.MemoryOwner{Kind: "agent", Ref: "builder"},
			Repo: "acme/widget", Scope: "repo", Key: key, Content: "fact " + key, SourceJob: "job-" + key}); err != nil {
			t.Fatalf("seed %d: %v", i, err)
		}
	}
	after, err := ds.ChangeCursor(ctx)
	if err != nil || before == after || !strings.HasSuffix(after, ".3") {
		t.Fatalf("cursor before=%q after=%q err=%v", before, after, err)
	}
	first, err := ds.BrainEvents(ctx, 0, 2)
	if err != nil || len(first.Events) != 2 || first.Events[0].Key != "three" || first.Events[1].Key != "two" || first.NextCursor != first.Events[1].ID {
		t.Fatalf("first page = %+v err=%v", first, err)
	}
	second, err := ds.BrainEvents(ctx, first.NextCursor, 2)
	if err != nil || len(second.Events) != 1 || second.Events[0].Key != "one" || second.NextCursor != 0 {
		t.Fatalf("second page = %+v err=%v", second, err)
	}
}

func TestBrainEventsHTTPAPIShape(t *testing.T) {
	home, store := memoryTestHome(t)
	if _, err := store.UpsertConfirmedMemory(context.Background(), db.ConfirmedMemory{
		Owner: db.MemoryOwner{Kind: "agent", Ref: "builder"}, Repo: "acme/widget", Scope: "repo",
		Key: "api", Content: "api fact", SourceJob: "job-api"}); err != nil {
		t.Fatal(err)
	}
	handler := newDashboardWebHandler(&webDataSource{home: home})
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/brain/events?limit=1", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s", recorder.Code, recorder.Body.String())
	}
	var body struct {
		Events []map[string]any `json:"events"`
		Next   int64            `json:"nextCursor"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.Events == nil || len(body.Events) != 1 || body.Events[0]["memoryId"] == nil || body.Events[0]["ownerKind"] != "agent" {
		t.Fatalf("body=%s", recorder.Body.String())
	}
}
