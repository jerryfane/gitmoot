package runtime

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/subprocess"
)

func TestInspectClaudeAuthEnvMasksReadiness(t *testing.T) {
	auth := InspectClaudeAuthEnv(func(name string) (string, bool) {
		switch name {
		case ClaudeOAuthTokenEnv:
			return "secret-token", true
		default:
			return "", false
		}
	})

	if !auth.Ready() {
		t.Fatal("auth env was not ready despite OAuth token")
	}
	detail := auth.MaskedDetail()
	if !strings.Contains(detail, ClaudeOAuthTokenEnv+"=set") || strings.Contains(detail, "secret-token") {
		t.Fatalf("masked detail = %q", detail)
	}
	if warning := auth.Warning(); warning != "" {
		t.Fatalf("warning = %q, want none", warning)
	}
}

func TestInspectClaudeAuthEnvWarnsForMissingCredentials(t *testing.T) {
	auth := InspectClaudeAuthEnv(func(string) (string, bool) { return "", false })

	if auth.Ready() {
		t.Fatal("auth env is ready despite no credentials")
	}
	if !strings.Contains(auth.Warning(), "claude setup-token") {
		t.Fatalf("warning = %q, want setup-token guidance", auth.Warning())
	}
}

func TestInspectClaudeAuthEnvWarnsForAPIKeyPrecedence(t *testing.T) {
	auth := InspectClaudeAuthEnv(func(name string) (string, bool) {
		if name == AnthropicAPIKeyEnv {
			return "secret-key", true
		}
		return "", false
	})

	if !auth.Ready() {
		t.Fatal("auth env was not ready despite API key")
	}
	if !strings.Contains(auth.Warning(), "API-key billing") {
		t.Fatalf("warning = %q, want API key warning", auth.Warning())
	}
}

func TestClaudeLiveCheckRunsPrintModeSmoke(t *testing.T) {
	runner := &fakeRunner{results: []subprocess.Result{{Stdout: `{"result":"OK"}`}}}

	if err := ClaudeLiveCheck(context.Background(), runner, "/repo"); err != nil {
		t.Fatalf("ClaudeLiveCheck returned error: %v", err)
	}

	runner.want(t, 0, "claude", "-p", "--output-format", "json", "--", ClaudeLiveCheckPrompt)
}

func TestClaudeLiveCheckClassifiesAuthFailure(t *testing.T) {
	runner := &fakeRunner{
		results: []subprocess.Result{{Stderr: "401 Invalid authentication credentials"}},
		errs:    []error{errors.New("exit 1")},
	}

	err := ClaudeLiveCheck(context.Background(), runner, "/repo")

	if err == nil {
		t.Fatal("ClaudeLiveCheck accepted auth failure")
	}
	for _, want := range []string{"Claude Code authentication failed", "claude setup-token", "restart the Gitmoot daemon"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error missing %q:\n%s", want, err)
		}
	}
}

func TestClaudeLiveCheckFallsBackToText(t *testing.T) {
	runner := &fakeRunner{
		results: []subprocess.Result{
			{Stderr: "unknown option '--output-format'"},
			{Stdout: "OK\n"},
		},
		errs: []error{errors.New("exit 1"), nil},
	}

	if err := ClaudeLiveCheck(context.Background(), runner, "/repo"); err != nil {
		t.Fatalf("ClaudeLiveCheck returned error: %v", err)
	}

	runner.want(t, 0, "claude", "-p", "--output-format", "json", "--", ClaudeLiveCheckPrompt)
	runner.want(t, 1, "claude", "-p", "--", ClaudeLiveCheckPrompt)
}

func TestClaudeLiveCheckFallbackRejectsStderrOnlySuccess(t *testing.T) {
	runner := &fakeRunner{
		results: []subprocess.Result{
			{Stderr: "unknown option '--output-format'"},
			{Stderr: "diagnostic only"},
		},
		errs: []error{errors.New("exit 1"), nil},
	}

	err := ClaudeLiveCheck(context.Background(), runner, "/repo")

	if err == nil {
		t.Fatal("ClaudeLiveCheck accepted stderr-only fallback output")
	}
	if !strings.Contains(err.Error(), "no stdout response") {
		t.Fatalf("error = %q, want no stdout response", err)
	}
}

func TestClaudeLiveCheckRejectsStderrOnlySuccess(t *testing.T) {
	runner := &fakeRunner{results: []subprocess.Result{{Stderr: "diagnostic only"}}}

	err := ClaudeLiveCheck(context.Background(), runner, "/repo")

	if err == nil {
		t.Fatal("ClaudeLiveCheck accepted stderr-only output")
	}
	if !strings.Contains(err.Error(), "no stdout response") {
		t.Fatalf("error = %q, want no stdout response", err)
	}
}
