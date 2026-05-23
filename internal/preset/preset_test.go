package preset

import (
	"context"
	"encoding/base64"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/subprocess"
)

func TestBuiltinsIncludesOnlyThermoPreset(t *testing.T) {
	definitions := Builtins()
	if len(definitions) != 1 {
		t.Fatalf("builtin count = %d, want 1", len(definitions))
	}
	definition := definitions[0]
	if definition.ID != ThermoNuclearCodeQualityReviewID || definition.Mutation {
		t.Fatalf("definition = %+v", definition)
	}
	if !reflect.DeepEqual(definition.DefaultCapabilities, []string{"ask", "review"}) {
		t.Fatalf("capabilities = %+v", definition.DefaultCapabilities)
	}
}

func TestGHFetcherUsesGitHubAPIAndDecodesContent(t *testing.T) {
	runner := &fakeRunner{}
	fetcher := GHFetcher{Runner: runner}

	sha, err := fetcher.ResolveRef(context.Background(), "cursor/plugins", "main")
	if err != nil {
		t.Fatalf("ResolveRef returned error: %v", err)
	}
	if sha != "abc123" {
		t.Fatalf("sha = %q, want abc123", sha)
	}
	file, err := fetcher.FetchFile(context.Background(), "cursor/plugins", sha, "cursor-team-kit/skills/thermo-nuclear-code-quality-review/SKILL.md")
	if err != nil {
		t.Fatalf("FetchFile returned error: %v", err)
	}
	if file.Content != "preset body" {
		t.Fatalf("content = %q", file.Content)
	}
	if len(runner.calls) != 2 {
		t.Fatalf("calls = %+v", runner.calls)
	}
	if !strings.Contains(strings.Join(runner.calls[1].args, " "), "-X GET repos/cursor/plugins/contents/cursor-team-kit/skills/thermo-nuclear-code-quality-review/SKILL.md -f ref=abc123") {
		t.Fatalf("fetch args = %+v", runner.calls[1].args)
	}
}

func TestDiffReportsChangedContent(t *testing.T) {
	diff := Diff("same\nold\nend\n", "same\nnew\nend\n")
	for _, want := range []string{"--- cached", "+++ upstream", "-old", "+new"} {
		if !strings.Contains(diff, want) {
			t.Fatalf("diff missing %q:\n%s", want, diff)
		}
	}
}

type fakeRunner struct {
	calls []fakeCall
}

type fakeCall struct {
	command string
	args    []string
}

func (f *fakeRunner) Run(_ context.Context, _ string, command string, args ...string) (subprocess.Result, error) {
	f.calls = append(f.calls, fakeCall{command: command, args: append([]string{}, args...)})
	joined := strings.Join(args, " ")
	switch {
	case strings.Contains(joined, "/git/ref/heads/main"):
		return subprocess.Result{Command: command, Args: args, Stdout: "abc123\n"}, nil
	case strings.Contains(joined, "/contents/"):
		return subprocess.Result{Command: command, Args: args, Stdout: `{"encoding":"base64","content":"` + base64.StdEncoding.EncodeToString([]byte("preset body")) + `"}`}, nil
	default:
		return subprocess.Result{Command: command, Args: args, Stderr: "unexpected call"}, errors.New("unexpected call")
	}
}

func (f *fakeRunner) LookPath(file string) (string, error) {
	return file, nil
}
