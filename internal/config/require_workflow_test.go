package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadRequireWorkflow(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("[workflow]\nrequire_workflow = true\nrequire_workflow_mode = \"strict\"\n[repos.\"a/b\"]\nrequire_workflow = false\n[repos.\"c/d\"]\nrequire_workflow_mode = \"auto\"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRequireWorkflow(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.For("none/repo"); !got.Enabled || got.Mode != "strict" {
		t.Fatalf("global=%+v", got)
	}
	if got := cfg.For("a/b"); got.Enabled || got.Mode != "strict" {
		t.Fatalf("a/b=%+v", got)
	}
	if got := cfg.For("c/d"); !got.Enabled || got.Mode != "auto" {
		t.Fatalf("c/d=%+v", got)
	}
}

func TestLoadRequireWorkflowDefaultsAndRejectsBadMode(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatal(err)
	}
	cfg, err := LoadRequireWorkflow(paths)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.For("a/b"); !got.Enabled || got.Mode != "auto" {
		t.Fatalf("default=%+v", got)
	}
	for _, body := range []string{
		"[workflow]\nrequire_workflow_mode = \"bad\"\n",
		"[workflow]\nrequire_workflow = true\nrequire_workflow_mode = \"bad\"\n",
	} {
		if err := os.WriteFile(paths.ConfigFile, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := LoadRequireWorkflow(paths); err == nil || !strings.Contains(err.Error(), "require_workflow_mode") {
			t.Fatalf("LoadRequireWorkflow(%q) error=%v", body, err)
		}
	}
}

func TestRequireWorkflowModeRequiresExplicitEnable(t *testing.T) {
	tests := []struct {
		name        string
		body        string
		repo        string
		wantEnabled bool
		wantMode    string
	}{
		{name: "mode only stays auto", body: "[workflow]\nrequire_workflow_mode = \"strict\"\n", repo: "owner/repo", wantEnabled: true, wantMode: "auto"},
		{name: "explicit global enable honors strict", body: "[workflow]\nrequire_workflow = true\nrequire_workflow_mode = \"strict\"\n", repo: "owner/repo", wantEnabled: true, wantMode: "strict"},
		{name: "global enable activates repo mode", body: "[workflow]\nrequire_workflow = true\n[repos.\"owner/repo\"]\nrequire_workflow_mode = \"strict\"\n", repo: "owner/repo", wantEnabled: true, wantMode: "strict"},
		{name: "explicit disable remains inert", body: "[workflow]\nrequire_workflow = false\nrequire_workflow_mode = \"strict\"\n", repo: "owner/repo", wantEnabled: false, wantMode: "strict"},
		{name: "defaults stay enabled auto", body: "[workflow]\n", repo: "owner/repo", wantEnabled: true, wantMode: "auto"},
		{name: "repo mode only stays auto", body: "[workflow]\n[repos.\"owner/repo\"]\nrequire_workflow_mode = \"strict\"\n", repo: "owner/repo", wantEnabled: true, wantMode: "auto"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			paths := PathsForHome(t.TempDir())
			if err := Initialize(paths); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(paths.ConfigFile, []byte(test.body), 0600); err != nil {
				t.Fatal(err)
			}
			cfg, err := LoadRequireWorkflow(paths)
			if err != nil {
				t.Fatal(err)
			}
			if got := cfg.For(test.repo); got.Enabled != test.wantEnabled || got.Mode != test.wantMode {
				t.Fatalf("For(%q) = %+v, want Enabled=%v Mode=%q", test.repo, got, test.wantEnabled, test.wantMode)
			}
		})
	}
}
