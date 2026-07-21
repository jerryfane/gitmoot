package pipeline

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/gitmoot/gitmoot/internal/config"
	"github.com/gitmoot/gitmoot/internal/workflow"
)

func TestDefaultPipelineResultLiteralsMatchContract(t *testing.T) {
	paths := config.Paths{Home: t.TempDir()}
	definitions := []defaultPipelineDefinition{
		renderMemoryIngestSweepPipeline(config.MemoryPipelineSettings{}, paths, "/tmp/gitmoot-home", "owner/repo"),
		renderMemoryGroomProposePipeline(config.MemoryPipelineSettings{}, paths, "/tmp/gitmoot-home", "owner/repo"),
	}

	var literals []string
	for _, definition := range definitions {
		for _, stage := range definition.spec.Stages {
			literals = append(literals, pipelineResultLiterals(stage.Cmd)...)
		}
	}
	if len(literals) != 8 {
		t.Fatalf("default pipeline result literals = %d, want 8", len(literals))
	}

	allowed := workflow.AllowedAgentResultFields()
	for i, literal := range literals {
		literal = normalizePipelineShellInterpolation(literal)
		var envelope map[string]json.RawMessage
		if err := json.Unmarshal([]byte(literal), &envelope); err != nil {
			t.Fatalf("literal %d is not JSON after shell interpolation normalization: %v\n%s", i, err, literal)
		}
		var result map[string]json.RawMessage
		if err := json.Unmarshal(envelope["gitmoot_result"], &result); err != nil {
			t.Fatalf("literal %d result is not a JSON object: %v", i, err)
		}
		for field := range result {
			if _, ok := allowed[field]; !ok {
				t.Fatalf("literal %d uses unsupported gitmoot_result field %q", i, field)
			}
		}
		var decision string
		if err := json.Unmarshal(result["decision"], &decision); err != nil {
			t.Fatalf("literal %d decision is not a string: %v", i, err)
		}
		if !stringInSlice(decision, workflow.ResultDecisions) {
			t.Fatalf("literal %d decision %q is outside workflow.ResultDecisions", i, decision)
		}
	}
}

func pipelineResultLiterals(command string) []string {
	const prefix = `{"gitmoot_result":`
	var literals []string
	for {
		start := strings.Index(command, prefix)
		if start < 0 {
			return literals
		}
		depth := 0
		end := -1
		for i := start; i < len(command); i++ {
			switch command[i] {
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					end = i + 1
				}
			}
			if end >= 0 {
				break
			}
		}
		if end < 0 {
			return literals
		}
		literals = append(literals, command[start:end])
		command = command[end:]
	}
}

// normalizePipelineShellInterpolation replaces the single-double-single quote
// handoff used by the shell fragments (for example '"$summary"') with a safe
// JSON string value. The test checks their static contract keys and decisions,
// not the dynamic summary contents.
func normalizePipelineShellInterpolation(literal string) string {
	for {
		start := strings.Index(literal, `'"$`)
		if start < 0 {
			return literal
		}
		end := strings.Index(literal[start+3:], `"'`)
		if end < 0 {
			return literal
		}
		end += start + 5
		literal = literal[:start] + "pipeline-value" + literal[end:]
	}
}

func stringInSlice(value string, values []string) bool {
	for _, candidate := range values {
		if value == candidate {
			return true
		}
	}
	return false
}
