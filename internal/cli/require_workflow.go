package cli

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/gitmoot/gitmoot/internal/config"
	"github.com/gitmoot/gitmoot/internal/workflow"
)

// requireWorkflowPolicyResolver accepts the raw --home value. In particular, a
// raw directory named .gitmoot is still a home directory, so its config lives
// under .gitmoot/.gitmoot rather than being mistaken for an already-resolved root.
func requireWorkflowPolicyResolver(home string) func(string) workflow.RequireWorkflowPolicy {
	if strings.TrimSpace(home) == "" {
		paths, err := pathsFromFlag("")
		if err != nil {
			return func(string) workflow.RequireWorkflowPolicy { return workflow.RequireWorkflowPolicy{} }
		}
		return requireWorkflowPolicyResolverPaths(paths)
	}
	return requireWorkflowPolicyResolverPaths(config.PathsForHome(home))
}

// requireWorkflowPolicyResolverRoot is for daemon and engine seams that already
// carry the canonical <home>/.gitmoot root. Keeping this separate prevents the
// #446/#459 double-resolution class while preserving raw --home semantics above.
func requireWorkflowPolicyResolverRoot(root string) func(string) workflow.RequireWorkflowPolicy {
	return requireWorkflowPolicyResolverPaths(config.Paths{ConfigFile: configFileAtRoot(root)})
}

func configFileAtRoot(root string) string {
	if strings.TrimSpace(root) == "" {
		return ""
	}
	return filepath.Join(strings.TrimSpace(root), config.ConfigName)
}

// requireWorkflowPolicyResolverPaths keeps config ownership in CLI while giving
// each enqueue producer a current policy. Invalid or unreadable config is
// fail-open here; config edit/init validation remains the operator-facing error
// path and the legacy default is disabled.
func requireWorkflowPolicyResolverPaths(paths config.Paths) func(string) workflow.RequireWorkflowPolicy {
	return func(repo string) workflow.RequireWorkflowPolicy {
		cfg, err := config.LoadRequireWorkflow(paths)
		if err != nil {
			return workflow.RequireWorkflowPolicy{}
		}
		p := cfg.For(repo)
		return workflow.RequireWorkflowPolicy{Enabled: p.Enabled, Mode: p.Mode}
	}
}

func preflightStrictWorkflowPolicy(home, repo, workflowID, policyExempt string) error {
	policy := requireWorkflowPolicyResolver(home)(repo)
	if policy.Enabled && policy.Mode == "strict" && strings.TrimSpace(workflowID) == "" && policyExempt != "exempt" && policyExempt != "auto-only" {
		return fmt.Errorf("repo %s has require_workflow=strict: pass --workflow <namespace>/<campaign>", strings.TrimSpace(repo))
	}
	return nil
}
