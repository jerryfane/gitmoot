package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitmoot/gitmoot/internal/workflow"
)

func TestContractDocsEnumsMatchWorkflow(t *testing.T) {
	skill := filepath.Join("..", "..", "skills", "gitmoot", "SKILL.md")
	assertDocEnumNear(t, skill, "gitmoot job close", workflow.ResultDecisions)
	assertDocEnumNear(t, skill, "locks, commits, pushes", workflow.DelegationActions)
	assertDocEnumNear(t, skill, "gitmoot chat task", workflow.DelegationActions)

	contract := filepath.Join("..", "..", "skills", "gitmoot", "references", "RESULT_CONTRACT.md")
	assertDocEnumNear(t, contract, `"decision"`, workflow.ResultDecisions)
	assertDocEnumNear(t, contract, "failure_policy", workflow.DelegationFailurePolicies)
	assertDocEnumNear(t, contract, "synthesis_rule", workflow.DelegationSynthesisRules)
}

func assertDocEnumNear(t *testing.T, path, marker string, values []string) {
	t.Helper()
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	lines := strings.Split(string(contents), "\n")
	for i, line := range lines {
		if !strings.Contains(line, marker) {
			continue
		}
		end := min(i+3, len(lines))
		block := strings.Join(lines[i:end], " ")
		for _, value := range values {
			if !strings.Contains(block, value) {
				t.Fatalf("%s enum near %q omits canonical value %q", path, marker, value)
			}
		}
		return
	}
	t.Fatalf("%s has no enum context containing %q", path, marker)
}
