package cli

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gitmoot/gitmoot/internal/config"
	"github.com/gitmoot/gitmoot/internal/workflow"
)

func TestOrgPolicyResolverFailsClosed(t *testing.T) {
	home := t.TempDir()
	paths := config.PathsForHome(home)
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("[org.roles.\"owner\"]\nscope=[\"bad/extra/path\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := orgPolicyResolver(home)("owner/repo"); got.LoadErr == nil {
		t.Fatal("malformed config did not surface LoadErr")
	}
}

func TestOrgCommandAndAgentOrgRolePrecedence(t *testing.T) {
	home := t.TempDir()
	paths := config.PathsForHome(home)
	if err := os.MkdirAll(filepath.Dir(paths.ConfigFile), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(paths.ConfigFile, []byte("[org.roles.\"owner\"]\nscope=[\"*\"]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	if code := runOrg([]string{"validate", "--home", home}, &out, &errOut); code != 0 || out.String() != "ok 1 roles\n" {
		t.Fatalf("validate code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	out.Reset()
	if code := runOrg([]string{"show", "--home", home}, &out, &errOut); code != 0 || !strings.Contains(out.String(), "owner\tparent=\tscope=*\tmerge_rule=owner") {
		t.Fatalf("show code=%d out=%q err=%q", code, out.String(), errOut.String())
	}
	t.Setenv("GITMOOT_ORG_ROLE", "env-role")
	options, ok := parseAgentAskOptions([]string{"agent", "message"}, &errOut)
	if !ok || options.orgRole != "env-role" {
		t.Fatalf("env options=%+v ok=%v", options, ok)
	}
	options, ok = parseAgentAskOptions([]string{"--org-role", "flag-role", "agent", "message"}, &errOut)
	if !ok || options.orgRole != "flag-role" {
		t.Fatalf("flag options=%+v ok=%v", options, ok)
	}
}

func TestOrgPreflightEnqueueParity(t *testing.T) {
	for _, tt := range []struct {
		name          string
		enforce       string
		role          string
		repo          string
		wantViolation bool
		wantErr       bool
	}{
		{name: "block role-less", enforce: "block", repo: "owner/repo", wantErr: true},
		{name: "block unknown", enforce: "block", role: "unknown", repo: "owner/repo", wantErr: true},
		{name: "block out-of-scope", enforce: "block", role: "owner", repo: "other/repo", wantErr: true},
		{name: "block in-scope", enforce: "block", role: "OWNER", repo: "owner/repo"},
		{name: "warn role-less", enforce: "warn", repo: "owner/repo", wantViolation: true},
		{name: "warn unknown", enforce: "warn", role: "unknown", repo: "owner/repo", wantViolation: true},
		{name: "warn out-of-scope", enforce: "warn", role: "owner", repo: "other/repo", wantViolation: true},
		{name: "warn in-scope", enforce: "warn", role: "owner", repo: "owner/repo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			policy := orgParityPolicy(tt.enforce)
			violation, decisionErr := workflow.OrgScopeDecision(policy, tt.role, tt.repo)
			if (decisionErr != nil) != tt.wantErr || (violation != "") != tt.wantViolation {
				t.Fatalf("shared decision violation=%q err=%v", violation, decisionErr)
			}
			if err := preflightOrgScope(policy, tt.repo, tt.role, true); (err != nil) != tt.wantErr {
				t.Fatalf("preflight err=%v, shared err=%v", err, decisionErr)
			}
			store := openCLIJobStore(t, t.TempDir())
			defer store.Close()
			job, err := (workflow.Mailbox{Store: store, OrgPolicy: fixedOrgPolicy(policy)}).Enqueue(context.Background(), workflow.JobRequest{ID: "parity", Agent: "agent", Action: "ask", Repo: tt.repo, OperatorOrigin: true, ActingOrgRole: tt.role})
			if (err != nil) != tt.wantErr {
				t.Fatalf("enqueue err=%v, shared err=%v", err, decisionErr)
			}
			if err != nil {
				return
			}
			events, err := store.ListJobEvents(context.Background(), job.ID)
			if err != nil {
				t.Fatal(err)
			}
			gotViolation := false
			for _, event := range events {
				gotViolation = gotViolation || event.Kind == "org_scope_violation"
			}
			if gotViolation != tt.wantViolation {
				t.Fatalf("warning event=%v want %v; events=%+v", gotViolation, tt.wantViolation, events)
			}
		})
	}
}

func orgParityPolicy(enforce string) workflow.OrgEnforcement {
	return workflow.OrgEnforcement{
		Enabled: true,
		Enforce: enforce,
		Role: func(name string) (workflow.OrgRole, bool) {
			if name == "owner" {
				return workflow.OrgRole{Name: "owner", Scope: []string{"owner/*"}}, true
			}
			return workflow.OrgRole{}, false
		},
		ScopeMatches: func(scopes []string, repo string) bool {
			return len(scopes) == 1 && scopes[0] == "owner/*" && strings.HasPrefix(repo, "owner/")
		},
	}
}
