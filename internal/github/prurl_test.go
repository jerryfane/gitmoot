package github

import "testing"

func TestParsePullRequestURL(t *testing.T) {
	ref, err := ParsePullRequestURL("https://github.com/jerryfane/gitmoot/pull/12")
	if err != nil {
		t.Fatalf("ParsePullRequestURL returned error: %v", err)
	}
	if ref.Repository() != "jerryfane/gitmoot" {
		t.Fatalf("repository = %q", ref.Repository())
	}
	if ref.Number != 12 {
		t.Fatalf("number = %d, want 12", ref.Number)
	}
}
