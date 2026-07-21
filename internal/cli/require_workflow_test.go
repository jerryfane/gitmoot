package cli

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/gitmoot/gitmoot/internal/config"
)

func TestRequireWorkflowPolicyResolverUsesCanonicalHome(t *testing.T) {
	writeStrict := func(t *testing.T, home string) {
		t.Helper()
		paths := config.PathsForHome(home)
		if err := config.Initialize(paths); err != nil {
			t.Fatal(err)
		}
		content := config.DefaultConfig(paths) + "\n[workflow]\nrequire_workflow = true\nrequire_workflow_mode = \"strict\"\n"
		if err := os.WriteFile(paths.ConfigFile, []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	defaultHome := t.TempDir()
	t.Setenv("HOME", defaultHome)
	writeStrict(t, defaultHome)
	cwd := t.TempDir()
	if err := os.MkdirAll(filepath.Join(cwd, ".gitmoot"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(cwd, ".gitmoot", "config.toml"), "[workflow]\nrequire_workflow = false\n")
	t.Chdir(cwd)
	if got := requireWorkflowPolicyResolver("")("owner/repo"); !got.Enabled || got.Mode != "strict" {
		t.Fatalf("empty home policy = %+v, want default-home strict policy", got)
	}

	rawHome := filepath.Join(t.TempDir(), ".gitmoot")
	writeStrict(t, rawHome)
	if err := os.WriteFile(filepath.Join(rawHome, config.ConfigName), []byte("[workflow]\nrequire_workflow = false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := requireWorkflowPolicyResolver(rawHome)("owner/repo"); !got.Enabled || got.Mode != "strict" {
		t.Fatalf("raw .gitmoot home policy = %+v, want nested default strict policy", got)
	}
}
