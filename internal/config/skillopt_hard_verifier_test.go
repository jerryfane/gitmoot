package config

import (
	"os"
	"testing"
)

// TestLoadSkillOptPolicyHardVerifiersDefaultsDisabled: the hard-verifier tier is OFF
// by default and has no commands (#474).
func TestLoadSkillOptPolicyHardVerifiersDefaultsDisabled(t *testing.T) {
	policy := DefaultSkillOptPolicy()
	if policy.HardVerifiers || policy.HardVerifiersEnabled() {
		t.Fatalf("hard verifiers must default OFF, got %+v", policy)
	}
	if policy.HardVerifierCommands != nil {
		t.Fatalf("hard_verifier_commands must default nil, got %+v", policy.HardVerifierCommands)
	}
	if got := policy.ResolvedHardVerifierCommands(); len(got) != 0 {
		t.Fatalf("default resolved hard-verifier commands = %v, want empty", got)
	}
}

// TestLoadSkillOptPolicyHardVerifiersRequireAutoTrace: hard_verifiers_enabled +
// commands WITHOUT auto_trace_enabled is OFF — HardVerifiersEnabled() requires the
// auto-trace harvester too, mirroring DeterministicCheckersEnabled().
func TestLoadSkillOptPolicyHardVerifiersRequireAutoTrace(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(DefaultConfig(paths)+`
[skillopt]
hard_verifiers_enabled = true
hard_verifier_commands = go build ./...
`), 0o600); err != nil {
		t.Fatalf("write config returned error: %v", err)
	}
	policy, err := LoadSkillOptPolicy(paths)
	if err != nil {
		t.Fatalf("LoadSkillOptPolicy returned error: %v", err)
	}
	if !policy.HardVerifiers {
		t.Fatal("hard_verifiers_enabled = true should parse")
	}
	if policy.HardVerifiersEnabled() {
		t.Fatal("HardVerifiersEnabled() must be false without auto_trace_enabled (requires BOTH)")
	}
}

// TestLoadSkillOptPolicyHardVerifiersRequireCommands: both knobs on but NO commands
// is still OFF — an empty command list has nothing to run, so the leg is a no-op.
func TestLoadSkillOptPolicyHardVerifiersRequireCommands(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(DefaultConfig(paths)+`
[skillopt]
auto_trace_enabled = true
hard_verifiers_enabled = true
`), 0o600); err != nil {
		t.Fatalf("write config returned error: %v", err)
	}
	policy, err := LoadSkillOptPolicy(paths)
	if err != nil {
		t.Fatalf("LoadSkillOptPolicy returned error: %v", err)
	}
	if policy.HardVerifiersEnabled() {
		t.Fatalf("HardVerifiersEnabled() must be false with no commands configured, got %+v", policy)
	}
}

// TestLoadSkillOptPolicyHardVerifiersEnabledWithAll: all three prerequisites present
// (auto_trace + enabled + ≥1 command) => HardVerifiersEnabled().
func TestLoadSkillOptPolicyHardVerifiersEnabledWithAll(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(DefaultConfig(paths)+`
[skillopt]
auto_trace_enabled = true
hard_verifiers_enabled = true
hard_verifier_commands = go build ./...
`), 0o600); err != nil {
		t.Fatalf("write config returned error: %v", err)
	}
	policy, err := LoadSkillOptPolicy(paths)
	if err != nil {
		t.Fatalf("LoadSkillOptPolicy returned error: %v", err)
	}
	if !policy.HardVerifiersEnabled() {
		t.Fatalf("HardVerifiersEnabled() must be true with all prerequisites, got %+v", policy)
	}
}

// TestLoadSkillOptPolicyHardVerifierCommandsAppendRepeatable: each
// hard_verifier_commands line appends ONE command (order preserved), so a command may
// itself contain commas / shell operators without a delimiter ambiguity.
func TestLoadSkillOptPolicyHardVerifierCommandsAppendRepeatable(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(DefaultConfig(paths)+`
[skillopt]
auto_trace_enabled = true
hard_verifiers_enabled = true
hard_verifier_commands = go build ./...
hard_verifier_commands = go test ./... && echo done, ok
hard_verifier_commands =
`), 0o600); err != nil {
		t.Fatalf("write config returned error: %v", err)
	}
	policy, err := LoadSkillOptPolicy(paths)
	if err != nil {
		t.Fatalf("LoadSkillOptPolicy returned error: %v", err)
	}
	// A comma INSIDE a command survives (each line is one command); the blank line is
	// dropped.
	want := []string{"go build ./...", "go test ./... && echo done, ok"}
	got := policy.ResolvedHardVerifierCommands()
	if len(got) != len(want) {
		t.Fatalf("resolved hard-verifier commands = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("resolved hard-verifier commands[%d] = %q, want %q (got %v)", i, got[i], want[i], got)
		}
	}
}

// TestLoadSkillOptPolicyRejectsBadHardVerifiersBool: a non-bool
// hard_verifiers_enabled surfaces a parse error (fail-safe).
func TestLoadSkillOptPolicyRejectsBadHardVerifiersBool(t *testing.T) {
	paths := PathsForHome(t.TempDir())
	if err := Initialize(paths); err != nil {
		t.Fatalf("Initialize returned error: %v", err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte(DefaultConfig(paths)+`
[skillopt]
hard_verifiers_enabled = maybe
`), 0o600); err != nil {
		t.Fatalf("write config returned error: %v", err)
	}
	if _, err := LoadSkillOptPolicy(paths); err == nil {
		t.Fatal("expected an error for a non-bool hard_verifiers_enabled")
	}
}
