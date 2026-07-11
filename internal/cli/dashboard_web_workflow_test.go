package cli

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	dashboard "github.com/jerryfane/gitmoot-dashboard"

	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/workflow"
)

func TestWebDataSourceStateCarriesRootWorkflowOnly(t *testing.T) {
	unlabelledHome := dashboardTestHome(t)
	seedWebDashboardTree(t, unlabelledHome)
	unlabelled, err := (&webDataSource{home: unlabelledHome}).State(context.Background(), "coord")
	if err != nil {
		t.Fatalf("State(unlabelled): %v", err)
	}
	if unlabelled.Workflow != "" {
		t.Fatalf("unlabelled State.Workflow = %q, want empty", unlabelled.Workflow)
	}

	home := dashboardTestHome(t)
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	payload := workflow.JobPayload{WorkflowID: "release-42", TaskTitle: "release root"}
	mustCreateJob(t, store, db.Job{ID: "release-root", Agent: "lead", Type: "orchestrate", State: "running", Payload: mustJSON(t, payload)}, "", "")
	store.Close()

	state, err := (&webDataSource{home: home}).State(context.Background(), "release-root")
	if err != nil {
		t.Fatalf("State(labelled): %v", err)
	}
	if state.Workflow != "release-42" {
		t.Fatalf("State.Workflow = %q, want release-42", state.Workflow)
	}
}

func TestWebDataSourceGraphWorkflowHubsRespectVisibleJobs(t *testing.T) {
	home := dashboardTestHome(t)
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	seed := func(id, label, repo string, in, out int) {
		t.Helper()
		payload := workflow.JobPayload{WorkflowID: label, Repo: repo, TaskTitle: id}
		mustCreateJob(t, store, db.Job{ID: id, Agent: "worker", Type: "ask", State: "succeeded", Payload: mustJSON(t, payload)}, "", "")
		if err := store.UpdateJobUsage(ctx, id, in, out); err != nil {
			t.Fatalf("UpdateJobUsage(%s): %v", id, err)
		}
	}
	seed("repo-a-release", "release-42", "acme/a", 2, 3)
	seed("repo-b-release", "release-42", "acme/b", 5, 7)
	seed("repo-a-audit", "audit", "acme/a", 11, 13)
	seed("repo-a-legacy", "", "acme/a", 17, 19)
	for _, label := range []string{"release-42", "release-42", "audit"} {
		if _, err := store.InsertWorkflowNote(ctx, db.WorkflowNote{WorkflowID: label, Body: "checkpoint"}); err != nil {
			t.Fatalf("InsertWorkflowNote(%s): %v", label, err)
		}
	}
	store.Close()

	ds := &webDataSource{home: home}
	all, err := ds.Graph(ctx, "")
	if err != nil {
		t.Fatalf("Graph(all): %v", err)
	}
	release, ok := workflowGraphNode(all.Nodes, "release-42")
	if !ok || release.JobCount != 2 || release.NoteCount != 2 || release.TokensIn != 7 || release.TokensOut != 10 {
		t.Fatalf("release workflow hub = %+v ok=%v", release, ok)
	}
	if got := workflowGraphLinkCount(all.Links); got != 3 {
		t.Fatalf("workflow links = %d, want 3 labelled jobs", got)
	}
	for _, link := range all.Links {
		if link.Kind == "workflow" && link.Source == "repo-a-legacy" {
			t.Fatalf("unlabelled job gained workflow link: %+v", link)
		}
	}

	filtered, err := ds.Graph(ctx, "acme/a")
	if err != nil {
		t.Fatalf("Graph(acme/a): %v", err)
	}
	release, ok = workflowGraphNode(filtered.Nodes, "release-42")
	if !ok || release.JobCount != 1 || release.TokensIn != 2 || release.TokensOut != 3 {
		t.Fatalf("filtered release hub = %+v ok=%v", release, ok)
	}
	if got := workflowGraphLinkCount(filtered.Links); got != 2 {
		t.Fatalf("filtered workflow links = %d, want 2 visible labelled jobs", got)
	}
	for _, link := range filtered.Links {
		if link.Kind == "workflow" && link.Source == "repo-b-release" {
			t.Fatalf("repo filter leaked hidden workflow link: %+v", link)
		}
	}
}

func TestWebDataSourceGraphUnlabelledLegacyJSONUnchanged(t *testing.T) {
	home := dashboardTestHome(t)
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustCreateJob(t, store, db.Job{ID: "legacy", Type: "ask", State: "running", Payload: `{"task_title":"legacy","workflow_id":""}`}, "", "")
	store.Close()

	got, err := (&webDataSource{home: home}).Graph(context.Background(), "")
	if err != nil {
		t.Fatalf("Graph: %v", err)
	}
	want := dashboard.Graph{
		Nodes: []dashboard.GraphNode{{ID: "legacy", Type: "job", Label: "legacy", State: "running", Run: "legacy"}},
		Links: []dashboard.GraphLink{}, Repos: []string{},
	}
	gotJSON, _ := json.Marshal(got)
	wantJSON, _ := json.Marshal(want)
	if string(gotJSON) != string(wantJSON) {
		t.Fatalf("legacy Graph changed\ngot  %s\nwant %s", gotJSON, wantJSON)
	}
}

func TestWebDashboardUnlabelledLegacyHTTPGolden(t *testing.T) {
	home := dashboardTestHome(t)
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	mustCreateJob(t, store, db.Job{ID: "legacy", Type: "ask", State: "running", Payload: `{"task_title":"legacy","workflow_id":""}`}, "", "")
	store.Close()
	setJobTimes(t, home, "legacy", "2026-01-02 03:04:05", "2026-01-02 03:04:05")

	handler := dashboard.Serve(&webDataSource{home: home})
	assertDashboardHTTPGolden(t, handler, "/api/state?run=legacy", legacyUnlabelledStateGolden)
	assertDashboardHTTPGolden(t, handler, "/api/graph", legacyUnlabelledGraphGolden)
}

const legacyUnlabelledStateGolden = `{
  "runId": "legacy",
  "title": "legacy",
  "nodes": [
    {
      "id": "legacy",
      "title": "legacy",
      "agent": "",
      "runtime": "",
      "state": "running",
      "depth": 0,
      "startedAt": 1767323045000,
      "events": []
    }
  ]
}
`

const legacyUnlabelledGraphGolden = `{
  "nodes": [
    {
      "id": "legacy",
      "type": "job",
      "label": "legacy",
      "state": "running",
      "run": "legacy"
    }
  ],
  "links": [],
  "repos": []
}
`

func assertDashboardHTTPGolden(t *testing.T, handler http.Handler, target, want string) {
	t.Helper()
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, target, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET %s status = %d, want 200; body=%q", target, recorder.Code, recorder.Body.String())
	}
	if got := recorder.Body.String(); got != want {
		t.Fatalf("GET %s legacy JSON changed\ngot:\n%s\nwant:\n%s", target, got, want)
	}
}

func TestWebDataSourceWorkflowGroupsTreesAndPaginates(t *testing.T) {
	home := dashboardTestHome(t)
	store, err := db.Open(config.PathsForHome(home).Database)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	ctx := context.Background()
	for _, agent := range []db.Agent{{Name: "lead", Runtime: "codex"}, {Name: "worker", Runtime: "claude"}} {
		if err := store.UpsertAgent(ctx, agent); err != nil {
			t.Fatalf("UpsertAgent(%s): %v", agent.Name, err)
		}
	}
	rootPayload := workflow.JobPayload{
		WorkflowID: "release-42", TaskTitle: "ship release", Model: "gpt-root",
		Result: &workflow.AgentResult{Delegations: []workflow.Delegation{
			{ID: "prep", Agent: "worker", Action: "prepare"},
			{ID: "ship", Agent: "worker", Action: "ship", Deps: []string{"prep"}},
		}},
	}
	mustCreateJob(t, store, db.Job{ID: "root", Agent: "lead", Type: "orchestrate", State: "succeeded", Payload: mustJSON(t, rootPayload)}, "", "")
	prepPayload := workflow.JobPayload{WorkflowID: "release-42", RootJobID: "root", ParentJobID: "root", DelegationID: "prep", TaskTitle: "prepare release"}
	mustCreateJob(t, store, db.Job{ID: "child-prep", Agent: "worker", Type: "ask", State: "succeeded", Payload: mustJSON(t, prepPayload), ParentJobID: "root", DelegationID: "prep", DelegationDepth: 1}, "", "")
	shipPayload := workflow.JobPayload{WorkflowID: "release-42", RootJobID: "root", ParentJobID: "root", DelegationID: "ship", Deps: []string{"prep"}, Model: "sonnet"}
	mustCreateJob(t, store, db.Job{ID: "child-ship", Agent: "worker", Type: "implement", State: "running", Payload: mustJSON(t, shipPayload), ParentJobID: "root", DelegationID: "ship", DelegationDepth: 1}, "", "")
	singlePayload := workflow.JobPayload{WorkflowID: "release-42", TaskTitle: "standalone audit"}
	mustCreateJob(t, store, db.Job{ID: "single", Agent: "worker", Type: "review", State: "succeeded", Payload: mustJSON(t, singlePayload)}, "", "")
	for id, usage := range map[string][2]int{"root": {2, 3}, "child-prep": {5, 7}, "child-ship": {11, 13}, "single": {17, 19}} {
		if err := store.UpdateJobUsage(ctx, id, usage[0], usage[1]); err != nil {
			t.Fatalf("UpdateJobUsage(%s): %v", id, err)
		}
	}
	for _, body := range []string{"one", "two", "three"} {
		if _, err := store.InsertWorkflowNote(ctx, db.WorkflowNote{WorkflowID: "release-42", Author: "operator", Body: body, Repo: "acme/widget"}); err != nil {
			t.Fatalf("InsertWorkflowNote: %v", err)
		}
	}
	store.Close()

	ds := &webDataSource{home: home}
	first, err := ds.Workflow(ctx, "release-42", dashboard.WorkflowQuery{MaxRuns: 1, MaxNotes: 2})
	if err != nil {
		t.Fatalf("Workflow(first): %v", err)
	}
	if first.Summary.Jobs != 4 || first.Summary.TokensIn != 35 || first.Summary.TokensOut != 42 || first.Summary.Notes != 3 {
		t.Fatalf("summary = %+v", first.Summary)
	}
	if len(first.Runs) != 1 || first.Runs[0].RunID != "root" || len(first.Runs[0].Nodes) != 3 {
		t.Fatalf("first run page = %+v", first.Runs)
	}
	ship, ok := workflowNodeByID(first.Runs[0].Nodes, "child-ship")
	if !ok || len(ship.Deps) != 1 || ship.Deps[0] != "child-prep" || ship.Runtime != "claude" || ship.Model != "sonnet" {
		t.Fatalf("compact child-ship = %+v ok=%v", ship, ok)
	}
	if first.NextRunCursor == "" || first.NextNoteCursor == "" || !first.Truncated || len(first.Notes) != 2 {
		t.Fatalf("first pagination = %+v", first)
	}

	second, err := ds.Workflow(ctx, "release-42", dashboard.WorkflowQuery{
		RunCursor: first.NextRunCursor, NoteCursor: first.NextNoteCursor, MaxRuns: 1, MaxNotes: 2,
	})
	if err != nil {
		t.Fatalf("Workflow(second): %v", err)
	}
	if len(second.Runs) != 1 || second.Runs[0].RunID != "single" || len(second.Runs[0].Nodes) != 1 {
		t.Fatalf("second run page = %+v", second.Runs)
	}
	if len(second.Notes) != 1 || second.Notes[0].Body != "three" || second.Truncated || second.NextRunCursor != "" || second.NextNoteCursor != "" {
		t.Fatalf("second pagination = %+v", second)
	}
}

func TestWebDataSourceWorkflowNotFound(t *testing.T) {
	home := dashboardTestHome(t)
	ds := &webDataSource{home: home}
	if _, err := ds.Workflow(context.Background(), "missing", dashboard.WorkflowQuery{}); !errors.Is(err, dashboard.ErrWorkflowNotFound) {
		t.Fatalf("Workflow(missing) err = %v, want ErrWorkflowNotFound", err)
	}
}

func TestCappedWorkflowLimitDefaultsAndCaps(t *testing.T) {
	for _, tc := range []struct {
		name             string
		value, cap, want int
	}{
		{name: "zero-defaults", value: 0, cap: dashboardWorkflowMaxRuns, want: dashboardWorkflowMaxRuns},
		{name: "negative-defaults", value: -1, cap: dashboardWorkflowMaxNotes, want: dashboardWorkflowMaxNotes},
		{name: "oversized-caps", value: dashboardWorkflowMaxRuns + 1, cap: dashboardWorkflowMaxRuns, want: dashboardWorkflowMaxRuns},
		{name: "in-range", value: 7, cap: dashboardWorkflowMaxNotes, want: 7},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := cappedWorkflowLimit(tc.value, tc.cap); got != tc.want {
				t.Fatalf("cappedWorkflowLimit(%d, %d) = %d, want %d", tc.value, tc.cap, got, tc.want)
			}
		})
	}
}

func workflowGraphNode(nodes []dashboard.GraphNode, label string) (dashboard.GraphNode, bool) {
	for _, node := range nodes {
		if node.ID == "workflow::"+label {
			return node, true
		}
	}
	return dashboard.GraphNode{}, false
}

func workflowGraphLinkCount(links []dashboard.GraphLink) int {
	var count int
	for _, link := range links {
		if link.Kind == "workflow" {
			count++
		}
	}
	return count
}

func workflowNodeByID(nodes []dashboard.WorkflowNode, id string) (dashboard.WorkflowNode, bool) {
	for _, node := range nodes {
		if node.ID == id {
			return node, true
		}
	}
	return dashboard.WorkflowNode{}, false
}
