package pathutil

import "testing"

func TestCleanExpandHome(t *testing.T) {
	got := CleanExpandHome("~/repo/../gitmoot", "/Users/example")
	want := "/Users/example/gitmoot"
	if got != want {
		t.Fatalf("CleanExpandHome = %q, want %q", got, want)
	}
}
