package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jerryfane/gitmoot/internal/daemon"
	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/runtime"
)

func runSetup(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("setup", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	repoFlag := fs.String("repo", "", "repo scope as owner/repo")
	path := fs.String("path", ".", "local checkout path")
	agentName := fs.String("agent", "", "agent name to subscribe")
	runtimeName := fs.String("runtime", "", "agent runtime: codex, claude, or shell")
	session := fs.String("session", "", "runtime session reference, last, or shell command")
	role := fs.String("role", "agent", "agent role")
	startDaemon := fs.Bool("start-daemon", false, "start the background daemon after setup")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "setup does not accept positional arguments")
		return 2
	}
	if strings.TrimSpace(*repoFlag) == "" || strings.TrimSpace(*agentName) == "" || strings.TrimSpace(*runtimeName) == "" || strings.TrimSpace(*session) == "" {
		fmt.Fprintln(stderr, "setup requires --repo, --agent, --runtime, and --session")
		return 2
	}
	repo, err := daemon.ParseRepository(*repoFlag)
	if err != nil {
		fmt.Fprintf(stderr, "invalid repo: %v\n", err)
		return 2
	}
	record, err := repoRecordFromPath(context.Background(), repo, *path)
	if err != nil {
		fmt.Fprintf(stderr, "setup: %v\n", err)
		return 1
	}
	agent := runtime.Agent{
		Name:           strings.TrimSpace(*agentName),
		Role:           strings.TrimSpace(*role),
		Runtime:        strings.TrimSpace(*runtimeName),
		RuntimeRef:     strings.TrimSpace(*session),
		RepoScope:      repo.FullName(),
		Capabilities:   []string{"review", "implement", "ask"},
		AutonomyPolicy: "auto",
		HealthStatus:   "unknown",
	}
	if err := runtime.ValidateAgent(agent); err != nil {
		fmt.Fprintf(stderr, "invalid agent: %v\n", err)
		return 2
	}

	writeLine(stdout, "step: initialize local state")
	writeLine(stdout, "step: register repo %s at %s", record.FullName(), record.CheckoutPath)
	writeLine(stdout, "step: subscribe agent %s (%s)", agent.Name, agent.Runtime)
	if err := withStore(*home, func(store *db.Store) error {
		exists, err := existingCompatibleAgent(context.Background(), store, agent)
		if err != nil {
			return err
		}
		if err := store.UpsertRepo(context.Background(), record); err != nil {
			return err
		}
		if !exists {
			if err := store.UpsertAgent(context.Background(), dbAgent(agent)); err != nil {
				return err
			}
		}
		return store.AllowAgentRepo(context.Background(), agent.Name, repo.FullName())
	}); err != nil {
		fmt.Fprintf(stderr, "setup: %v\n", err)
		return 1
	}
	writeLine(stdout, "configured %s with agent %s", repo.FullName(), agent.Name)
	if *startDaemon {
		writeLine(stdout, "step: start background daemon")
		return runDaemonStartWithWorkDir([]string{"--home", *home, "--repo", repo.FullName()}, record.CheckoutPath, stdout, stderr)
	}
	writeLine(stdout, "next: gitmoot daemon start --repo %s", repo.FullName())
	return 0
}

func existingCompatibleAgent(ctx context.Context, store *db.Store, agent runtime.Agent) (bool, error) {
	existing, err := store.GetAgent(ctx, agent.Name)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if existing.Runtime != agent.Runtime || existing.RuntimeRef != agent.RuntimeRef {
		return false, fmt.Errorf("agent %s already exists with runtime %s session %s", existing.Name, existing.Runtime, existing.RuntimeRef)
	}
	return true, nil
}
