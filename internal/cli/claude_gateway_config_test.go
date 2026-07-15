package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gitmoot/gitmoot/internal/runtime"
)

// The child config dir must mirror the real Claude config so the agent keeps its
// settings and skills, but must never carry the cached credential — otherwise
// Claude authenticates from it and ignores the gateway placeholder (#936).
func TestBuildClaudeGatewayConfigDirMirrorsEverythingButTheCredential(t *testing.T) {
	source := t.TempDir()
	t.Setenv(runtime.ClaudeConfigDirEnv, source)
	mustWrite(t, filepath.Join(source, claudeCredentialsFile), "REAL-SECRET-CREDENTIAL")
	mustWrite(t, filepath.Join(source, "settings.json"), `{"theme":"dark"}`)
	mustWrite(t, filepath.Join(source, "CLAUDE.md"), "user memory")
	if err := os.MkdirAll(filepath.Join(source, "skills", "gitmoot"), 0o700); err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(source, "skills", "gitmoot", "SKILL.md"), "skill body")

	dest, err := buildClaudeGatewayConfigDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}

	// The credential must be absent, and reachable through no path.
	if _, err := os.Lstat(filepath.Join(dest, claudeCredentialsFile)); !os.IsNotExist(err) {
		t.Fatalf("credential present in child config dir: err=%v", err)
	}
	// Settings and skills must be reachable (read THROUGH the mirror).
	if got := readThrough(t, filepath.Join(dest, "settings.json")); got != `{"theme":"dark"}` {
		t.Fatalf("settings not mirrored: %q", got)
	}
	if got := readThrough(t, filepath.Join(dest, "CLAUDE.md")); got != "user memory" {
		t.Fatalf("CLAUDE.md not mirrored: %q", got)
	}
	if got := readThrough(t, filepath.Join(dest, "skills", "gitmoot", "SKILL.md")); got != "skill body" {
		t.Fatalf("skills not mirrored: %q", got)
	}
	// And the real credential must not be findable anywhere under dest.
	assertNoCredentialUnder(t, dest, "REAL-SECRET-CREDENTIAL")
}

// If Claude wrote a .credentials.json into the child dir on a prior run (e.g. a
// refresh attempt), a rebuild must scrub it — the invariant is "no credential
// here", regardless of who wrote it.
func TestBuildClaudeGatewayConfigDirScrubsAWrittenCredential(t *testing.T) {
	source := t.TempDir()
	t.Setenv(runtime.ClaudeConfigDirEnv, source)
	home := t.TempDir()

	dest, err := buildClaudeGatewayConfigDir(home)
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, filepath.Join(dest, claudeCredentialsFile), "child-wrote-this")

	dest2, err := buildClaudeGatewayConfigDir(home)
	if err != nil {
		t.Fatal(err)
	}
	if dest2 != dest {
		t.Fatalf("config dir path changed across rebuilds: %q vs %q", dest, dest2)
	}
	if _, err := os.Lstat(filepath.Join(dest, claudeCredentialsFile)); !os.IsNotExist(err) {
		t.Fatal("rebuild did not scrub a written credential")
	}
}

// A rebuild is idempotent and must not clobber real runtime state the child wrote
// into the config dir (projects, todos, …).
func TestBuildClaudeGatewayConfigDirIsIdempotentAndKeepsChildWrites(t *testing.T) {
	source := t.TempDir()
	t.Setenv(runtime.ClaudeConfigDirEnv, source)
	mustWrite(t, filepath.Join(source, "settings.json"), "s1")
	home := t.TempDir()

	dest, err := buildClaudeGatewayConfigDir(home)
	if err != nil {
		t.Fatal(err)
	}
	// The child writes real runtime state that the source does not have.
	mustWrite(t, filepath.Join(dest, "projects", "p1.json"), "child project state")

	if _, err := buildClaudeGatewayConfigDir(home); err != nil {
		t.Fatal(err)
	}
	if got := readThrough(t, filepath.Join(dest, "projects", "p1.json")); got != "child project state" {
		t.Fatalf("rebuild clobbered child runtime state: %q", got)
	}
	if got := readThrough(t, filepath.Join(dest, "settings.json")); got != "s1" {
		t.Fatalf("settings link lost on rebuild: %q", got)
	}
}

// No real config dir at all still yields a usable, credential-free dir.
func TestBuildClaudeGatewayConfigDirWithNoSource(t *testing.T) {
	t.Setenv(runtime.ClaudeConfigDirEnv, filepath.Join(t.TempDir(), "does-not-exist"))
	dest, err := buildClaudeGatewayConfigDir(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if info, err := os.Stat(dest); err != nil || !info.IsDir() {
		t.Fatalf("dest not a directory: err=%v", err)
	}
	if _, err := os.Lstat(filepath.Join(dest, claudeCredentialsFile)); !os.IsNotExist(err) {
		t.Fatal("empty-source dir somehow has a credential")
	}
}

func mustWrite(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func readThrough(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %q: %v", path, err)
	}
	return string(data)
}

func assertNoCredentialUnder(t *testing.T, root, secret string) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		data, readErr := os.ReadFile(path)
		if readErr == nil && string(data) == secret {
			t.Fatalf("real credential reachable at %q", path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
}
