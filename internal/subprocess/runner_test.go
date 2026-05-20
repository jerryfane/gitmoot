package subprocess

import (
	"context"
	"runtime"
	"strings"
	"testing"
)

func TestRunCapturesStdout(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("shell command differs on windows")
	}

	result, err := Run(context.Background(), "", "sh", "-c", "printf gitmoot")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if strings.TrimSpace(result.Stdout) != "gitmoot" {
		t.Fatalf("stdout = %q", result.Stdout)
	}
}
