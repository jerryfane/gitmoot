package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/preset"
)

func TestPresetUpdateInstallsThermoPreset(t *testing.T) {
	restore := replacePresetFetcher(fakePresetFetcher{
		commit:  "abc123",
		content: "Review deeply.",
	})
	defer restore()
	var stdout, stderr bytes.Buffer
	home := t.TempDir()

	code := Run([]string{"preset", "update", "--home", home, "thermo-nuclear-code-quality-review"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("preset update exit code = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "updated thermo-nuclear-code-quality-review at abc123") {
		t.Fatalf("stdout = %s", stdout.String())
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"preset", "show", "--home", home, "thermo-nuclear-code-quality-review"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("preset show exit code = %d, stderr=%s", code, stderr.String())
	}
	for _, want := range []string{"installed: yes", "resolved commit: abc123", "Review deeply."} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("show output missing %q:\n%s", want, stdout.String())
		}
	}
}

func TestPresetDiffDoesNotMutateCachedPreset(t *testing.T) {
	restore := replacePresetFetcher(fakePresetFetcher{
		commit:  "abc123",
		content: "old body",
	})
	defer restore()
	var stdout, stderr bytes.Buffer
	home := t.TempDir()
	if code := Run([]string{"preset", "update", "--home", home, "thermo-nuclear-code-quality-review"}, &stdout, &stderr); code != 0 {
		t.Fatalf("preset update exit code = %d, stderr=%s", code, stderr.String())
	}

	restore()
	restore = replacePresetFetcher(fakePresetFetcher{
		commit:  "def456",
		content: "new body",
	})
	defer restore()
	stdout.Reset()
	stderr.Reset()
	code := Run([]string{"preset", "diff", "--home", home, "thermo-nuclear-code-quality-review"}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("preset diff exit code = %d, stderr=%s", code, stderr.String())
	}
	for _, want := range []string{"cached:   abc123", "upstream: def456", "-old body", "+new body"} {
		if !strings.Contains(stdout.String(), want) {
			t.Fatalf("diff output missing %q:\n%s", want, stdout.String())
		}
	}

	stdout.Reset()
	stderr.Reset()
	code = Run([]string{"preset", "show", "--home", home, "thermo-nuclear-code-quality-review"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("preset show exit code = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "resolved commit: abc123") || strings.Contains(stdout.String(), "def456") {
		t.Fatalf("diff mutated cached preset:\n%s", stdout.String())
	}
}

func TestPresetListShowsAvailableBuiltin(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"preset", "list", "--home", t.TempDir()}, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("preset list exit code = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "thermo-nuclear-code-quality-review") || !strings.Contains(stdout.String(), "available") {
		t.Fatalf("stdout = %s", stdout.String())
	}
}

func replacePresetFetcher(fetcher preset.Fetcher) func() {
	previous := newPresetFetcher
	newPresetFetcher = func() preset.Fetcher {
		return fetcher
	}
	return func() {
		newPresetFetcher = previous
	}
}

type fakePresetFetcher struct {
	commit  string
	content string
}

func (f fakePresetFetcher) ResolveRef(context.Context, string, string) (string, error) {
	return f.commit, nil
}

func (f fakePresetFetcher) FetchFile(context.Context, string, string, string) (preset.File, error) {
	return preset.File{Content: f.content}, nil
}
