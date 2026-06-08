package config

import (
	"os"
	"strings"
	"testing"
)

func TestLoadParallelSessionPolicyDefaults(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}

	policy, err := LoadParallelSessionPolicy(paths)

	if err != nil {
		t.Fatalf("LoadParallelSessionPolicy returned error: %v", err)
	}
	if policy.SameSession != ParallelSessionForkTempSession {
		t.Fatalf("SameSession = %q, want %q", policy.SameSession, ParallelSessionForkTempSession)
	}
	if policy.MergeBack != ParallelSessionMergeBackSummary {
		t.Fatalf("MergeBack = %q, want %q", policy.MergeBack, ParallelSessionMergeBackSummary)
	}
	if policy.MaxTempSessionsPerAgent != 4 {
		t.Fatalf("MaxTempSessionsPerAgent = %d, want 4", policy.MaxTempSessionsPerAgent)
	}
	if strings.Join(policy.EligibleActions, ",") != "ask,review,implement" {
		t.Fatalf("EligibleActions = %v", policy.EligibleActions)
	}
}

func TestLoadParallelSessionPolicyOverrides(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(DefaultConfig(paths)+`
[parallel_sessions]
same_session = "queue"
merge_back = "off"
max_temp_sessions_per_agent = 2
eligible_actions = ["ask", "review"]
`), 0o600); err != nil {
		t.Fatalf("write config returned error: %v", err)
	}

	policy, err := LoadParallelSessionPolicy(paths)

	if err != nil {
		t.Fatalf("LoadParallelSessionPolicy returned error: %v", err)
	}
	if policy.SameSession != ParallelSessionQueue || policy.MergeBack != ParallelSessionMergeBackOff || policy.MaxTempSessionsPerAgent != 2 || strings.Join(policy.EligibleActions, ",") != "ask,review" {
		t.Fatalf("policy = %+v", policy)
	}
}

func TestLoadParallelSessionPolicyRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name    string
		body    string
		wantErr string
	}{
		{
			name: "same_session",
			body: `
[parallel_sessions]
same_session = "clone_hidden_state"
`,
			wantErr: "unsupported parallel_sessions.same_session",
		},
		{
			name: "merge_back",
			body: `
[parallel_sessions]
merge_back = "full"
`,
			wantErr: "unsupported parallel_sessions.merge_back",
		},
		{
			name: "max_temp_sessions_per_agent",
			body: `
[parallel_sessions]
max_temp_sessions_per_agent = 0
`,
			wantErr: "max_temp_sessions_per_agent must be positive",
		},
		{
			name: "eligible_actions",
			body: `
[parallel_sessions]
eligible_actions = ["ask", "deploy"]
`,
			wantErr: "unsupported parallel_sessions.eligible_actions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			paths := PathsForHome(t.TempDir())
			if err := Initialize(paths); err != nil {
				t.Fatalf("Initialize returned error: %v", err)
			}
			if err := os.WriteFile(paths.ConfigFile, []byte(DefaultConfig(paths)+tt.body), 0o600); err != nil {
				t.Fatalf("write config returned error: %v", err)
			}

			_, err := LoadParallelSessionPolicy(paths)

			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("LoadParallelSessionPolicy error = %v, want %q", err, tt.wantErr)
			}
		})
	}
}
