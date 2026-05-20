package config

import (
	"path/filepath"
	"testing"
)

func TestPathsForHome(t *testing.T) {
	paths := PathsForHome("/tmp/example")

	if paths.ConfigFile != filepath.Join("/tmp/example", ".gitmoot", "config.toml") {
		t.Fatalf("ConfigFile = %q", paths.ConfigFile)
	}
	if paths.Database != filepath.Join("/tmp/example", ".gitmoot", "gitmoot.db") {
		t.Fatalf("Database = %q", paths.Database)
	}
}
