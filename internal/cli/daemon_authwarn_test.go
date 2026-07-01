package cli

import (
	"bytes"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/runtime"
)

// withClaudeAuthLookup swaps the injectable auth-readiness seam for the duration
// of a test so the #559 warning can be exercised without real host credentials.
func withClaudeAuthLookup(t *testing.T, env map[string]string) {
	t.Helper()
	prev := claudeAuthEnvLookup
	claudeAuthEnvLookup = func(key string) (string, bool) {
		v, ok := env[key]
		return v, ok
	}
	t.Cleanup(func() { claudeAuthEnvLookup = prev })
}

func TestWarnIfDaemonStartLosesClaudeAuth_WarnsWhenNotReady(t *testing.T) {
	withClaudeAuthLookup(t, map[string]string{}) // no Claude auth in the inherited env

	for _, restart := range []bool{false, true} {
		var buf bytes.Buffer
		warnIfDaemonStartLosesClaudeAuth(&buf, restart)
		out := buf.String()
		if !strings.Contains(out, "WARNING") {
			t.Fatalf("restart=%v: expected a WARNING, got %q", restart, out)
		}
		if !strings.Contains(out, "CLAUDE_CODE_OAUTH_TOKEN") {
			t.Fatalf("restart=%v: warning should name the token, got %q", restart, out)
		}
		if !strings.Contains(out, "ALL subscribed repos") {
			t.Fatalf("restart=%v: warning should note it affects all repos, got %q", restart, out)
		}
	}
}

func TestWarnIfDaemonStartLosesClaudeAuth_RestartWordingReflectsDrop(t *testing.T) {
	withClaudeAuthLookup(t, map[string]string{})

	var start, restart bytes.Buffer
	warnIfDaemonStartLosesClaudeAuth(&start, false)
	warnIfDaemonStartLosesClaudeAuth(&restart, true)

	if strings.Contains(start.String(), "DROP") {
		t.Fatalf("plain-start wording should not claim a DROP, got %q", start.String())
	}
	if !strings.Contains(restart.String(), "DROP") || !strings.Contains(restart.String(), "LOSE") {
		t.Fatalf("restart wording should reflect the auth being dropped/lost, got %q", restart.String())
	}
}

func TestWarnIfDaemonStartLosesClaudeAuth_SilentWhenReady(t *testing.T) {
	// Any single Claude credential makes auth ready; the warning must stay silent.
	for _, key := range []string{
		runtime.ClaudeOAuthTokenEnv,
		runtime.AnthropicAPIKeyEnv,
		runtime.AnthropicAuthTokenEnv,
	} {
		withClaudeAuthLookup(t, map[string]string{key: "x"})
		for _, restart := range []bool{false, true} {
			var buf bytes.Buffer
			warnIfDaemonStartLosesClaudeAuth(&buf, restart)
			if buf.Len() != 0 {
				t.Fatalf("key=%s restart=%v: expected silence when auth ready, got %q", key, restart, buf.String())
			}
		}
	}
}

func TestDaemonRestartEnvCaveat_MentionsEnvReinheritance(t *testing.T) {
	if !strings.Contains(daemonRestartEnvCaveat, "CLAUDE_CODE_OAUTH_TOKEN") {
		t.Fatalf("footgun caveat should name the token: %q", daemonRestartEnvCaveat)
	}
	if !strings.Contains(daemonRestartEnvCaveat, "RE-INHERITS") {
		t.Fatalf("footgun caveat should note env re-inheritance: %q", daemonRestartEnvCaveat)
	}
	if !strings.Contains(daemonRestartEnvCaveat, "scheduler state") {
		t.Fatalf("footgun caveat should note scheduler-state reset: %q", daemonRestartEnvCaveat)
	}
}

func TestDaemonUsage_ClarifiesRepoScope(t *testing.T) {
	var buf bytes.Buffer
	printDaemonUsage(&buf)
	out := buf.String()
	if !strings.Contains(out, "LAUNCH CONTEXT") {
		t.Fatalf("usage should clarify --repo sets the launch context, got:\n%s", out)
	}
	if !strings.Contains(out, "does NOT") || !strings.Contains(out, "ALL subscribed repos") {
		t.Fatalf("usage should clarify --repo does not scope supervision, got:\n%s", out)
	}
}
