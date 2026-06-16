package workflow

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// delegationManifest is the on-disk context-manifest.json written for a parent
// job that produced delegations carrying artifacts. It gives each child a
// machine-readable view of its sibling delegations.
type delegationManifest struct {
	ParentJobID string                    `json:"parent_job_id"`
	Delegations []delegationManifestEntry `json:"delegations"`
}

// delegationManifestEntry is the reduced view of a single delegation surfaced in
// context-manifest.json. It intentionally exposes only the coordination fields
// children need to reason about the wider batch.
type delegationManifestEntry struct {
	ID       string   `json:"id"`
	Agent    string   `json:"agent"`
	Action   string   `json:"action"`
	Worktree string   `json:"worktree,omitempty"`
	Deps     []string `json:"deps,omitempty"`
}

// writeDelegationArtifacts persists the coordinator brief and a context manifest
// for a parent job when at least one of its delegations requests artifacts. It
// mirrors writeAgentTemplateDraft's MkdirAll(0o755)+WriteFile(0o644) pattern and
// returns the directory the artifacts were written to, or "" when nothing was
// written (empty root or no delegation requested artifacts) so callers can skip
// wiring the directory into child prompts. Artifacts live under
// <root>/delegations/<parent-job-id>/, with the parent job id sanitized into a
// single safe path segment because job ids may contain slashes.
func writeDelegationArtifacts(root string, parentJobID string, result *AgentResult) (string, error) {
	if strings.TrimSpace(root) == "" {
		return "", nil
	}
	if result == nil || !delegationsRequestArtifacts(result.Delegations) {
		return "", nil
	}
	segment, err := safeDelegationPathSegment(parentJobID, "parent job id")
	if err != nil {
		return "", err
	}
	dir := filepath.Join(root, "delegations", segment)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create delegation artifact directory: %w", err)
	}
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(briefPath, []byte(result.ArtifactBody), 0o644); err != nil {
		return "", fmt.Errorf("write delegation brief %s: %w", briefPath, err)
	}
	manifest := delegationManifest{
		ParentJobID: parentJobID,
		Delegations: make([]delegationManifestEntry, 0, len(result.Delegations)),
	}
	for _, d := range result.Delegations {
		manifest.Delegations = append(manifest.Delegations, delegationManifestEntry{
			ID:       d.ID,
			Agent:    d.Agent,
			Action:   d.Action,
			Worktree: d.Worktree,
			Deps:     d.Deps,
		})
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return "", fmt.Errorf("encode delegation manifest: %w", err)
	}
	manifestPath := filepath.Join(dir, "context-manifest.json")
	if err := os.WriteFile(manifestPath, encoded, 0o644); err != nil {
		return "", fmt.Errorf("write delegation manifest %s: %w", manifestPath, err)
	}
	return dir, nil
}

func delegationsRequestArtifacts(delegations []Delegation) bool {
	for _, d := range delegations {
		if len(d.Artifacts) > 0 {
			return true
		}
	}
	return false
}

// safeDelegationPathSegment reduces a value (typically a parent job id, which may
// contain slashes such as "parent-job/delegation/del-1") to a single path
// segment that cannot escape its parent directory. Unsafe characters are
// replaced with '-' rather than rejected so artifacts can always be written;
// the only failure is an empty or wholly unsafe value.
func safeDelegationPathSegment(value string, label string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("%s is required", label)
	}
	var builder strings.Builder
	for _, char := range value {
		switch {
		case char >= 'a' && char <= 'z':
			builder.WriteRune(char)
		case char >= 'A' && char <= 'Z':
			builder.WriteRune(char)
		case char >= '0' && char <= '9':
			builder.WriteRune(char)
		case char == '-' || char == '_':
			builder.WriteRune(char)
		default:
			builder.WriteByte('-')
		}
	}
	segment := strings.Trim(builder.String(), "-")
	if segment == "" || segment == "." || segment == ".." {
		return "", fmt.Errorf("%s %q has no safe path representation", label, value)
	}
	return segment, nil
}
