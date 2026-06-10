package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/jerryfane/gitmoot/internal/config"
	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/skillopt"
)

// dashboardSnapshot is a read-only view of local Gitmoot state. It is assembled
// entirely from existing store/status sources; the dashboard never owns
// workflow state.
type dashboardSnapshot struct {
	Home            string                  `json:"home"`
	DatabasePath    string                  `json:"database_path"`
	DatabaseExists  bool                    `json:"database_exists"`
	Daemon          dashboardDaemon         `json:"daemon"`
	Repos           []dashboardRepo         `json:"repos"`
	Agents          []dashboardAgent        `json:"agents"`
	RuntimeSessions []dashboardSession      `json:"runtime_sessions"`
	Jobs            dashboardJobs           `json:"jobs"`
	Worktrees       []dashboardWorktree     `json:"worktrees"`
	BranchLocks     []dashboardBranchLock   `json:"branch_locks"`
	TrainSessions   []dashboardTrainSession `json:"train_sessions"`
	PendingPrompts  []dashboardPrompt       `json:"pending_prompts"`
}

type dashboardDaemon struct {
	Running bool   `json:"running"`
	PID     int    `json:"pid,omitempty"`
	LogFile string `json:"log_file,omitempty"`
}

type dashboardRepo struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type dashboardAgent struct {
	Name    string `json:"name"`
	Runtime string `json:"runtime"`
	Role    string `json:"role,omitempty"`
	Health  string `json:"health,omitempty"`
}

type dashboardSession struct {
	Name    string `json:"name"`
	Runtime string `json:"runtime"`
	Repo    string `json:"repo,omitempty"`
	State   string `json:"state,omitempty"`
}

type dashboardJobs struct {
	Total   int            `json:"total"`
	ByState map[string]int `json:"by_state"`
}

type dashboardWorktree struct {
	Task string `json:"task"`
	Repo string `json:"repo,omitempty"`
	Path string `json:"path"`
}

type dashboardBranchLock struct {
	Repo   string `json:"repo"`
	Branch string `json:"branch"`
	Owner  string `json:"owner,omitempty"`
}

type dashboardTrainSession struct {
	ID        string `json:"id"`
	Phase     string `json:"phase"`
	Candidate string `json:"candidate_version,omitempty"`
	Repo      string `json:"repo,omitempty"`
}

type dashboardPrompt struct {
	ID       string `json:"id"`
	Question string `json:"question"`
}

func runDashboard(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("dashboard", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	jsonOutput := fs.Bool("json", false, "write the snapshot as JSON")
	answerID := fs.String("answer", "", "pending prompt id to answer before showing the snapshot")
	answerValue := fs.String("value", "", "answer value to use with --answer")
	answerSource := fs.String("source", "dashboard", "answer source recorded with --answer")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "dashboard does not accept positional arguments")
		return 2
	}

	paths, err := initializedPaths(*home)
	if err != nil {
		fmt.Fprintf(stderr, "dashboard: %v\n", err)
		return 1
	}

	// The only state the dashboard may mutate is answering a pending prompt, and
	// it does so through the same store API as `gitmoot interactive answer`.
	if strings.TrimSpace(*answerID) != "" {
		if err := withStore(*home, func(store *db.Store) error {
			_, err := store.AnswerInteractivePrompt(context.Background(), *answerID, *answerValue, *answerSource)
			return err
		}); err != nil {
			fmt.Fprintf(stderr, "dashboard: answer prompt: %v\n", err)
			return 1
		}
	}

	snapshot, err := buildDashboardSnapshot(*home, paths)
	if err != nil {
		fmt.Fprintf(stderr, "dashboard: %v\n", err)
		return 1
	}
	if *jsonOutput {
		if err := writeJSON(stdout, snapshot); err != nil {
			fmt.Fprintf(stderr, "dashboard: %v\n", err)
			return 1
		}
		return 0
	}
	printDashboardSnapshot(stdout, snapshot)
	return 0
}

func buildDashboardSnapshot(home string, paths config.Paths) (dashboardSnapshot, error) {
	snapshot := dashboardSnapshot{
		Home:            paths.Home,
		DatabasePath:    paths.Database,
		Jobs:            dashboardJobs{ByState: map[string]int{}},
		Repos:           []dashboardRepo{},
		Agents:          []dashboardAgent{},
		RuntimeSessions: []dashboardSession{},
		Worktrees:       []dashboardWorktree{},
		BranchLocks:     []dashboardBranchLock{},
		TrainSessions:   []dashboardTrainSession{},
		PendingPrompts:  []dashboardPrompt{},
	}
	if info, err := os.Stat(paths.Database); err == nil && !info.IsDir() {
		snapshot.DatabaseExists = true
	}

	state := daemonProcessState(paths)
	if pid, _, err := currentDaemonPID(state); err == nil && pid > 0 {
		snapshot.Daemon = dashboardDaemon{Running: true, PID: pid, LogFile: state.LogFile}
	} else {
		snapshot.Daemon = dashboardDaemon{Running: false, LogFile: state.LogFile}
	}

	err := withStore(home, func(store *db.Store) error {
		ctx := context.Background()
		repos, err := store.ListRepos(ctx)
		if err != nil {
			return err
		}
		for _, repo := range repos {
			snapshot.Repos = append(snapshot.Repos, dashboardRepo{Name: repo.FullName(), Enabled: repo.Enabled})
		}
		agents, err := store.ListAgents(ctx)
		if err != nil {
			return err
		}
		for _, agent := range agents {
			snapshot.Agents = append(snapshot.Agents, dashboardAgent{Name: agent.Name, Runtime: agent.Runtime, Role: agent.Role, Health: agent.HealthStatus})
		}
		instances, err := store.ListAgentInstances(ctx)
		if err != nil {
			return err
		}
		for _, instance := range instances {
			snapshot.RuntimeSessions = append(snapshot.RuntimeSessions, dashboardSession{Name: instance.Name, Runtime: instance.Runtime, Repo: instance.RepoFullName, State: instance.State})
		}
		jobs, err := store.ListJobs(ctx)
		if err != nil {
			return err
		}
		snapshot.Jobs.Total = len(jobs)
		for _, job := range jobs {
			state := job.State
			if strings.TrimSpace(state) == "" {
				state = "unknown"
			}
			snapshot.Jobs.ByState[state]++
		}
		tasks, err := store.ListTasks(ctx)
		if err != nil {
			return err
		}
		for _, task := range tasks {
			if strings.TrimSpace(task.WorktreePath) != "" {
				snapshot.Worktrees = append(snapshot.Worktrees, dashboardWorktree{Task: task.ID, Repo: task.RepoFullName, Path: task.WorktreePath})
			}
		}
		locks, err := store.ListBranchLocks(ctx, "")
		if err != nil {
			return err
		}
		for _, lock := range locks {
			snapshot.BranchLocks = append(snapshot.BranchLocks, dashboardBranchLock{Repo: lock.RepoFullName, Branch: lock.Branch, Owner: lock.Owner})
		}
		trainSessions, err := store.ListSkillOptTrainSessions(ctx)
		if err != nil {
			return err
		}
		for _, session := range trainSessions {
			entry := dashboardTrainSession{ID: session.ID, Phase: session.State, Repo: session.TargetRepo}
			iteration, err := store.GetLatestSkillOptTrainIteration(ctx, session.ID)
			switch {
			case err == nil:
				summary := skillopt.BuildTrainStatusSummary(session, &iteration, skillopt.TrainStatusCounts{})
				entry.Phase = summary.CurrentPhase
				entry.Candidate = summary.CandidateVersion
			case errors.Is(err, sql.ErrNoRows):
				// No iteration yet — keep the session-state fallback.
			default:
				return err
			}
			snapshot.TrainSessions = append(snapshot.TrainSessions, entry)
		}
		prompts, err := store.ListInteractivePrompts(ctx, db.InteractivePromptStatePending)
		if err != nil {
			return err
		}
		for _, prompt := range prompts {
			snapshot.PendingPrompts = append(snapshot.PendingPrompts, dashboardPrompt{ID: prompt.ID, Question: prompt.Question})
		}
		return nil
	})
	if err != nil {
		return dashboardSnapshot{}, err
	}
	return snapshot, nil
}

func printDashboardSnapshot(stdout io.Writer, snapshot dashboardSnapshot) {
	writeLine(stdout, "home: %s", snapshot.Home)
	writeLine(stdout, "database: %s (%s)", snapshot.DatabasePath, presentOrMissing(snapshot.DatabaseExists))
	if snapshot.Daemon.Running {
		writeLine(stdout, "daemon: running pid %d", snapshot.Daemon.PID)
	} else {
		writeLine(stdout, "daemon: stopped")
	}

	writeLine(stdout, "repos: %d", len(snapshot.Repos))
	for _, repo := range snapshot.Repos {
		writeLine(stdout, "  %s (%s)", repo.Name, enabledOrDisabled(repo.Enabled))
	}
	writeLine(stdout, "agents: %d", len(snapshot.Agents))
	for _, agent := range snapshot.Agents {
		writeLine(stdout, "  %s [%s]%s", agent.Name, agent.Runtime, dashboardSuffix(agent.Role, agent.Health))
	}
	writeLine(stdout, "runtime_sessions: %d", len(snapshot.RuntimeSessions))
	for _, session := range snapshot.RuntimeSessions {
		writeLine(stdout, "  %s [%s] %s %s", session.Name, session.Runtime, emptyText(session.Repo), session.State)
	}
	writeLine(stdout, "jobs: %d", snapshot.Jobs.Total)
	for _, state := range sortedKeys(snapshot.Jobs.ByState) {
		writeLine(stdout, "  %s: %d", state, snapshot.Jobs.ByState[state])
	}
	writeLine(stdout, "worktrees: %d", len(snapshot.Worktrees))
	for _, worktree := range snapshot.Worktrees {
		writeLine(stdout, "  %s %s", worktree.Task, worktree.Path)
	}
	writeLine(stdout, "branch_locks: %d", len(snapshot.BranchLocks))
	for _, lock := range snapshot.BranchLocks {
		writeLine(stdout, "  %s@%s %s", lock.Repo, lock.Branch, emptyText(lock.Owner))
	}
	writeLine(stdout, "train_sessions: %d", len(snapshot.TrainSessions))
	for _, train := range snapshot.TrainSessions {
		writeLine(stdout, "  %s phase=%s candidate=%s", train.ID, train.Phase, emptyText(train.Candidate))
	}
	writeLine(stdout, "pending_prompts: %d", len(snapshot.PendingPrompts))
	for _, prompt := range snapshot.PendingPrompts {
		writeLine(stdout, "  %s\t%s", prompt.ID, prompt.Question)
	}
}

func presentOrMissing(present bool) string {
	if present {
		return "present"
	}
	return "missing"
}

func enabledOrDisabled(enabled bool) string {
	if enabled {
		return "enabled"
	}
	return "disabled"
}

func dashboardSuffix(role, health string) string {
	parts := []string{}
	if strings.TrimSpace(role) != "" {
		parts = append(parts, role)
	}
	if strings.TrimSpace(health) != "" {
		parts = append(parts, health)
	}
	if len(parts) == 0 {
		return ""
	}
	return " " + strings.Join(parts, " ")
}

func sortedKeys(m map[string]int) []string {
	keys := make([]string, 0, len(m))
	for key := range m {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
