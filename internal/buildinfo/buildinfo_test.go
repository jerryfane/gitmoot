package buildinfo

import "testing"

func TestCurrentUsesDevelopmentDefaults(t *testing.T) {
	info := Current()
	if info.Version != "dev" {
		t.Fatalf("version = %q, want dev", info.Version)
	}
	if info.Commit != "unknown" {
		t.Fatalf("commit = %q, want unknown", info.Commit)
	}
	if info.Date != "unknown" {
		t.Fatalf("date = %q, want unknown", info.Date)
	}
	if info.Go == "" {
		t.Fatal("go version is empty")
	}
}
