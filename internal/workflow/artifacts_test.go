package workflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWriteDelegationArtifactsWritesBriefAndManifest(t *testing.T) {
	root := t.TempDir()
	result := &AgentResult{
		Decision:     "approved",
		Summary:      "coordinated",
		ArtifactBody: "# Brief\n\nShared context for the batch.\n",
		Delegations: []Delegation{
			{ID: "api", Agent: "builder-a", Action: "implement", Worktree: "api-work", Artifacts: []string{"brief.md"}},
			{ID: "ui", Agent: "builder-b", Action: "implement"},
		},
	}

	dir, err := writeDelegationArtifacts(root, "parent-job", result)
	if err != nil {
		t.Fatalf("writeDelegationArtifacts returned error: %v", err)
	}
	wantDir := filepath.Join(root, "delegations", "parent-job")
	if dir != wantDir {
		t.Fatalf("dir = %q, want %q", dir, wantDir)
	}

	brief, err := os.ReadFile(filepath.Join(dir, "brief.md"))
	if err != nil {
		t.Fatalf("read brief.md: %v", err)
	}
	if string(brief) != result.ArtifactBody {
		t.Fatalf("brief.md = %q, want %q", string(brief), result.ArtifactBody)
	}

	raw, err := os.ReadFile(filepath.Join(dir, "context-manifest.json"))
	if err != nil {
		t.Fatalf("read context-manifest.json: %v", err)
	}
	var manifest delegationManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if manifest.ParentJobID != "parent-job" {
		t.Fatalf("manifest parent_job_id = %q, want %q", manifest.ParentJobID, "parent-job")
	}
	if len(manifest.Delegations) != 2 {
		t.Fatalf("manifest delegations = %d, want 2", len(manifest.Delegations))
	}
	first := manifest.Delegations[0]
	if first.ID != "api" || first.Agent != "builder-a" || first.Action != "implement" || first.Worktree != "api-work" || len(first.Deps) != 0 {
		t.Fatalf("manifest entry[0] = %+v", first)
	}
	second := manifest.Delegations[1]
	if second.ID != "ui" || second.Agent != "builder-b" || second.Action != "implement" || second.Worktree != "" || len(second.Deps) != 0 {
		t.Fatalf("manifest entry[1] = %+v", second)
	}
}

func TestWriteDelegationArtifactsManifestOnlyHasReducedFields(t *testing.T) {
	root := t.TempDir()
	result := &AgentResult{
		ArtifactBody: "brief",
		Delegations: []Delegation{
			{ID: "api", Agent: "builder", Action: "implement", Worktree: "w", Deps: []string{"x"},
				Prompt: "secret prompt", Timeout: "5m", Retry: 3, FailurePolicy: "escalate", Artifacts: []string{"brief.md"}},
		},
	}

	dir, err := writeDelegationArtifacts(root, "parent-job", result)
	if err != nil {
		t.Fatalf("writeDelegationArtifacts returned error: %v", err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, "context-manifest.json"))
	if err != nil {
		t.Fatalf("read context-manifest.json: %v", err)
	}
	// The manifest must expose only the coordination fields, never the prompt
	// or lifecycle controls.
	var generic struct {
		Delegations []map[string]json.RawMessage `json:"delegations"`
	}
	if err := json.Unmarshal(raw, &generic); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if len(generic.Delegations) != 1 {
		t.Fatalf("manifest delegations = %d, want 1", len(generic.Delegations))
	}
	entry := generic.Delegations[0]
	for _, want := range []string{"id", "agent", "action", "worktree", "deps"} {
		if _, ok := entry[want]; !ok {
			t.Fatalf("manifest entry missing %q: %v", want, entry)
		}
	}
	for got := range entry {
		switch got {
		case "id", "agent", "action", "worktree", "deps":
		default:
			t.Fatalf("manifest entry leaked field %q: %v", got, entry)
		}
	}
}

func TestWriteDelegationArtifactsEmptyRootWritesNothing(t *testing.T) {
	dir, err := writeDelegationArtifacts("", "parent-job", &AgentResult{
		ArtifactBody: "brief",
		Delegations:  []Delegation{{ID: "api", Agent: "a", Action: "implement", Artifacts: []string{"brief.md"}}},
	})
	if err != nil {
		t.Fatalf("writeDelegationArtifacts returned error: %v", err)
	}
	if dir != "" {
		t.Fatalf("dir = %q, want empty", dir)
	}
}

func TestWriteDelegationArtifactsNoArtifactsWritesNothing(t *testing.T) {
	root := t.TempDir()
	dir, err := writeDelegationArtifacts(root, "parent-job", &AgentResult{
		ArtifactBody: "brief",
		Delegations: []Delegation{
			{ID: "api", Agent: "a", Action: "implement"},
			{ID: "ui", Agent: "b", Action: "review"},
		},
	})
	if err != nil {
		t.Fatalf("writeDelegationArtifacts returned error: %v", err)
	}
	if dir != "" {
		t.Fatalf("dir = %q, want empty", dir)
	}
	if entries, err := os.ReadDir(root); err != nil {
		t.Fatalf("read root: %v", err)
	} else if len(entries) != 0 {
		t.Fatalf("root has %d entries, want none", len(entries))
	}
}

func TestWriteDelegationArtifactsSanitizesUnsafeParentID(t *testing.T) {
	root := t.TempDir()
	result := &AgentResult{
		ArtifactBody: "brief",
		Delegations:  []Delegation{{ID: "api", Agent: "a", Action: "implement", Artifacts: []string{"brief.md"}}},
	}

	dir, err := writeDelegationArtifacts(root, "parent-job/delegation/del-1", result)
	if err != nil {
		t.Fatalf("writeDelegationArtifacts returned error: %v", err)
	}

	delegationsRoot := filepath.Join(root, "delegations")
	rel, err := filepath.Rel(delegationsRoot, dir)
	if err != nil {
		t.Fatalf("filepath.Rel: %v", err)
	}
	if strings.Contains(rel, string(filepath.Separator)) || rel == ".." || strings.HasPrefix(rel, "..") {
		t.Fatalf("sanitized dir %q escapes delegations root %q (rel=%q)", dir, delegationsRoot, rel)
	}
	if _, err := os.Stat(filepath.Join(dir, "brief.md")); err != nil {
		t.Fatalf("brief.md not written into sanitized dir: %v", err)
	}
	// The manifest must still carry the original (unsanitized) parent job id.
	raw, err := os.ReadFile(filepath.Join(dir, "context-manifest.json"))
	if err != nil {
		t.Fatalf("read context-manifest.json: %v", err)
	}
	var manifest delegationManifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("manifest is not valid JSON: %v", err)
	}
	if manifest.ParentJobID != "parent-job/delegation/del-1" {
		t.Fatalf("manifest parent_job_id = %q, want original id", manifest.ParentJobID)
	}
}
