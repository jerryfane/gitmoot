package git

import "testing"

func TestParseGitHubRemote(t *testing.T) {
	tests := []struct {
		name   string
		remote string
		want   string
	}{
		{name: "https without suffix", remote: "https://github.com/jerryfane/gitmoot", want: "jerryfane/gitmoot"},
		{name: "https with suffix", remote: "https://github.com/jerryfane/gitmoot.git", want: "jerryfane/gitmoot"},
		{name: "ssh", remote: "git@github.com:jerryfane/gitmoot.git", want: "jerryfane/gitmoot"},
		{name: "dotted repo", remote: "https://github.com/jerryfane/foo.bar.git", want: "jerryfane/foo.bar"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo, err := ParseGitHubRemote(tt.remote)
			if err != nil {
				t.Fatalf("ParseGitHubRemote returned error: %v", err)
			}
			if repo.String() != tt.want {
				t.Fatalf("repo = %q, want %q", repo.String(), tt.want)
			}
		})
	}
}
