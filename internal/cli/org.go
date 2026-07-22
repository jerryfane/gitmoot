package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/gitmoot/gitmoot/internal/config"
	"github.com/gitmoot/gitmoot/internal/db"
	"github.com/gitmoot/gitmoot/internal/workflow"
)

// orgPolicyResolver accepts a raw --home, while the Root variant below accepts
// the already resolved <home>/.gitmoot path used by daemon engines.
func orgPolicyResolver(home string) func(string) workflow.OrgEnforcement {
	if strings.TrimSpace(home) == "" {
		paths, err := pathsFromFlag("")
		if err != nil {
			return func(string) workflow.OrgEnforcement { return workflow.OrgEnforcement{LoadErr: err} }
		}
		return orgPolicyResolverPaths(paths)
	}
	return orgPolicyResolverPaths(config.PathsForHome(home))
}

func orgPolicyResolverRoot(root string) func(string) workflow.OrgEnforcement {
	return orgPolicyResolverPaths(config.Paths{ConfigFile: configFileAtRoot(root)})
}

func orgPolicyResolverPaths(paths config.Paths) func(string) workflow.OrgEnforcement {
	return func(string) workflow.OrgEnforcement {
		cfg, err := config.LoadOrg(paths)
		if err != nil {
			return workflow.OrgEnforcement{LoadErr: err}
		}
		return workflow.OrgEnforcement{
			Enabled: cfg.Enabled(), Enforce: cfg.Enforce(),
			Role: func(name string) (workflow.OrgRole, bool) {
				r, ok := cfg.Role(name)
				return workflow.OrgRole{Name: r.Name, Parent: r.Parent, Scope: append([]string(nil), r.Scope...), MergeRule: r.MergeRule}, ok
			},
			ScopeMatches: config.ScopeMatches,
		}
	}
}

// preflightOrgScope prevents an org-blocked implement/task-run request from
// allocating a task worktree or branch lock before Mailbox.Enqueue can reject.
// Warn mode intentionally leaves the durable event to the enqueue chokepoint.
func preflightOrgScope(policy workflow.OrgEnforcement, repo, actingRole string, operatorOrigin bool) error {
	if !operatorOrigin {
		return nil
	}
	_, err := workflow.OrgScopeDecision(policy, actingRole, repo)
	return err
}

func fixedOrgPolicy(policy workflow.OrgEnforcement) func(string) workflow.OrgEnforcement {
	return func(string) workflow.OrgEnforcement { return policy }
}

func runOrg(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		fmt.Fprintln(stdout, "Usage:\n  gitmoot org validate [--home path]\n  gitmoot org show [--home path]\n  gitmoot org escalate --to <role> --workflow <label> [--org-role <role>] [--repo <owner/repo>] [--home path] [--json] \"<question>\"")
		return 0
	}
	if args[0] == "escalate" {
		return runOrgEscalate(args[1:], stdout, stderr)
	}
	if args[0] != "validate" && args[0] != "show" {
		fmt.Fprintf(stderr, "unknown org command %q\n", args[0])
		return 2
	}
	fs := flag.NewFlagSet("org "+args[0], flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "org %s does not accept positional arguments\n", args[0])
		return 2
	}
	paths, err := pathsFromFlag(*home)
	if err != nil {
		fmt.Fprintf(stderr, "org %s: resolve paths: %v\n", args[0], err)
		return 1
	}
	cfg, err := config.LoadOrg(paths)
	if err != nil {
		fmt.Fprintf(stderr, "org %s: %v\n", args[0], err)
		return 1
	}
	if args[0] == "validate" {
		fmt.Fprintf(stdout, "ok %d roles\n", len(cfg.Roles()))
		return 0
	}
	for _, role := range cfg.Roles() {
		fmt.Fprintf(stdout, "%s\tparent=%s\tscope=%s\tmerge_rule=%s\n", role.Name, role.Parent, strings.Join(role.Scope, ","), firstNonEmpty(role.MergeRule, "owner"))
	}
	return 0
}

type orgEscalateOutput struct {
	From     string `json:"from"`
	To       string `json:"to"`
	Workflow string `json:"workflow"`
	Question string `json:"question"`
}

func runOrgEscalate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("org escalate", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	toFlag := fs.String("to", "", "ancestor role to escalate to")
	workflowID := fs.String("workflow", "", "workflow label for the escalation note")
	fromFlag := fs.String("org-role", "", "acting organization role")
	repo := fs.String("repo", "", "repository binding for the escalation note")
	jsonOutput := fs.Bool("json", false, "print the escalation as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "org escalate requires exactly one question")
		return 2
	}
	question := strings.TrimSpace(fs.Arg(0))
	if question == "" {
		fmt.Fprintln(stderr, "org escalate question must be non-empty")
		return 2
	}
	paths, err := pathsFromFlag(*home)
	if err != nil {
		fmt.Fprintf(stderr, "org escalate: resolve paths: %v\n", err)
		return 1
	}
	cfg, err := config.LoadOrg(paths)
	if err != nil {
		fmt.Fprintf(stderr, "org escalate: %v\n", err)
		return 1
	}
	if !cfg.Enabled() {
		fmt.Fprintln(stderr, "org escalate requires an [org] registry")
		return 2
	}
	from := strings.ToLower(strings.TrimSpace(*fromFlag))
	if from == "" {
		from = strings.ToLower(strings.TrimSpace(os.Getenv("GITMOOT_ORG_ROLE")))
	}
	if _, ok := cfg.Role(from); !ok {
		fmt.Fprintf(stderr, "unknown org role %q\n", from)
		return 2
	}
	to := strings.ToLower(strings.TrimSpace(*toFlag))
	if to == "" {
		fmt.Fprintln(stderr, "org escalate requires --to")
		return 2
	}
	if _, ok := cfg.Role(to); !ok {
		fmt.Fprintf(stderr, "unknown org role %q\n", to)
		return 2
	}
	if to == from || !orgContainsString(cfg.Ancestors(from), to) {
		fmt.Fprintf(stderr, "--to %q must be an ancestor of %q in the org hierarchy\n", to, from)
		return 2
	}
	label := strings.TrimSpace(*workflowID)
	if label == "" {
		fmt.Fprintln(stderr, "org escalate requires --workflow")
		return 2
	}
	if err := workflow.ValidateWorkflowID(label); err != nil {
		fmt.Fprintf(stderr, "org escalate: %v\n", err)
		return 2
	}
	body := workflow.FormatOrgEscalateNote(from, to, label, question)
	if body == "" || len(body) > workflowNoteBodyMax {
		fmt.Fprintf(stderr, "org escalate question must produce a note of at most %d bytes\n", workflowNoteBodyMax)
		return 2
	}
	if err := withStore(*home, func(store *db.Store) error {
		_, err := store.InsertWorkflowNote(context.Background(), db.WorkflowNote{
			WorkflowID: label, Author: from, Body: body, Repo: strings.TrimSpace(*repo),
		})
		return err
	}); err != nil {
		fmt.Fprintf(stderr, "org escalate: %v\n", err)
		return 1
	}
	out := orgEscalateOutput{From: from, To: to, Workflow: label, Question: question}
	if *jsonOutput {
		if err := json.NewEncoder(stdout).Encode(out); err != nil {
			fmt.Fprintf(stderr, "org escalate: %v\n", err)
			return 1
		}
		return 0
	}
	fmt.Fprintf(stdout, "escalated from %s to %s in workflow %s\n", from, to, label)
	return 0
}

func orgContainsString(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}
