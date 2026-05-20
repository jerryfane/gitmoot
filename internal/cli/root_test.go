package cli

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsUsage(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run(nil, &stdout, &stderr)

	if code != 0 {
		t.Fatalf("Run(nil) exit code = %d, want 0", code)
	}
	if !strings.Contains(stdout.String(), "gitmoot <command>") {
		t.Fatalf("usage output missing command help:\n%s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("stderr = %q, want empty", stderr.String())
	}
}

func TestRunRejectsUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer

	code := Run([]string{"nope"}, &stdout, &stderr)

	if code != 2 {
		t.Fatalf("unknown command exit code = %d, want 2", code)
	}
	if !strings.Contains(stderr.String(), `unknown command "nope"`) {
		t.Fatalf("stderr missing unknown command message:\n%s", stderr.String())
	}
}
