package cli

import (
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/jerryfane/gitmoot/internal/cli/tui"
	"github.com/jerryfane/gitmoot/internal/config"
)

// buildDashboardConfigView parses the config file into the read-only sections
// the Config page renders. Every parser tolerates a missing file (the daemon
// flags come from daemon.json). Cheap file reads, so it rides the snapshot.
func buildDashboardConfigView(paths config.Paths, daemon dashboardDaemonDetail) tui.ConfigView {
	view := tui.ConfigView{Path: paths.ConfigFile}

	view.Sections = append(view.Sections, tui.ConfigSection{
		Title: "paths",
		Rows: [][]string{
			{"database", paths.Database},
			{"logs", paths.Logs},
			{"workspaces", paths.Workspaces},
		},
	})

	if types, err := config.LoadAgentTypes(paths); err == nil && len(types) > 0 {
		names := make([]string, 0, len(types))
		for name := range types {
			names = append(names, name)
		}
		sort.Strings(names)
		rows := [][]string{{"NAME", "RUNTIME", "TEMPLATE", "ROLE", "MAX_BG", "IDLE", "JOB"}}
		var editable []tui.ConfigField
		for _, name := range names {
			t := types[name]
			rows = append(rows, []string{
				name, t.Runtime, t.Template, dashConfig(t.Role),
				strconv.Itoa(t.MaxBackground), dashConfig(t.IdleTimeout), dashConfig(t.JobTimeout),
			})
			// Scalar fields safe to edit inline; the rest (runtime, template,
			// capabilities, adding/removing types) stay an $EDITOR job.
			editable = append(editable,
				tui.ConfigField{Label: name + " · max_background", KeyPath: []string{"agents", name, "max_background"}, Kind: tui.ConfigInt, Value: strconv.Itoa(t.MaxBackground)},
			)
			if strings.TrimSpace(t.IdleTimeout) != "" {
				editable = append(editable, tui.ConfigField{Label: name + " · idle_timeout", KeyPath: []string{"agents", name, "idle_timeout"}, Kind: tui.ConfigDuration, Value: t.IdleTimeout})
			}
			if strings.TrimSpace(t.JobTimeout) != "" {
				editable = append(editable, tui.ConfigField{Label: name + " · job_timeout", KeyPath: []string{"agents", name, "job_timeout"}, Kind: tui.ConfigDuration, Value: t.JobTimeout})
			}
		}
		view.Sections = append(view.Sections, tui.ConfigSection{Title: "agent types", Rows: rows, Editable: editable})
	}

	if policy, err := config.LoadParallelSessionPolicy(paths); err == nil {
		view.Sections = append(view.Sections, tui.ConfigSection{
			Title: "parallel sessions",
			Rows: [][]string{
				{"same_session", policy.SameSession},
				{"merge_back", policy.MergeBack},
				{"max_temp_sessions_per_agent", strconv.Itoa(policy.MaxTempSessionsPerAgent)},
				{"eligible_actions", strings.Join(policy.EligibleActions, ", ")},
			},
		})
	}

	if repo, err := config.LoadDefaultFeedbackRepo(paths); err == nil && strings.TrimSpace(repo) != "" {
		view.Sections = append(view.Sections, tui.ConfigSection{
			Title: "feedback",
			Rows:  [][]string{{"repo", dashConfig(repo)}},
			Editable: []tui.ConfigField{
				{Label: "feedback · repo", KeyPath: []string{"feedback", "repo"}, Kind: tui.ConfigText, Value: repo},
			},
		})
	}

	daemonRows := [][]string{}
	if len(daemon.Flags) > 0 {
		daemonRows = append(daemonRows, []string{"flags", strings.Join(daemon.Flags, " ")})
	}
	if daemon.WorkDir != "" {
		daemonRows = append(daemonRows, []string{"workdir", daemon.WorkDir})
	}
	if len(daemonRows) > 0 {
		view.Sections = append(view.Sections, tui.ConfigSection{Title: "daemon (persisted)", Rows: daemonRows})
	}

	return view
}

// configScalarForKind types the value for the writer from the field's own
// classification (the single source of truth), so a new int field cannot be
// mis-written as a string. Durations and repos are stored as TOML strings.
func configScalarForKind(kind tui.ConfigKind, value string) config.ConfigScalar {
	if kind == tui.ConfigInt {
		if n, err := strconv.Atoi(value); err == nil {
			return config.IntScalar(n)
		}
	}
	return config.StringScalar(value)
}

func dashConfig(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

// validateDashboardConfig re-runs the config parsers and returns
// human-readable problems (empty when the file parses cleanly). It is the
// safety net after an external edit.
func validateDashboardConfig(paths config.Paths) []string {
	var problems []string
	if _, err := config.LoadAgentTypes(paths); err != nil {
		problems = append(problems, "[agents.*] "+err.Error())
	}
	if _, err := config.LoadParallelSessionPolicy(paths); err != nil {
		problems = append(problems, "[parallel_sessions] "+err.Error())
	}
	if _, err := config.LoadDefaultFeedbackRepo(paths); err != nil {
		problems = append(problems, "[feedback] "+err.Error())
	}
	return problems
}

// editConfigCmd opens the config file in $EDITOR (fallback vi) via
// tea.ExecProcess, which suspends the program for the editor and resumes on
// exit, delivering a tui.ConfigEditedMsg.
// agentPromptScaffold seeds the custom-prompt editor when no base template is
// chosen. It includes the gitmoot_result contract reminder so the default does
// not trip the "missing contract" notice.
const agentPromptScaffold = `You are a Gitmoot-managed agent.

Describe this agent's role and how it should approach jobs here.

Always end every job with a concise, truthful gitmoot_result JSON object, e.g.:
  {"decision": "implemented", "summary": "what you did"}
Decisions: approved | changes_requested | blocked | implemented | failed.
Use "blocked" when you need human input or external state, and "failed" when an
attempted action errored.
`

// editAgentPromptCmd writes seed to a temp file, opens it in $EDITOR, and on
// close reads the saved content back into an AgentPromptEditedMsg. Mirrors
// editConfigCmd but round-trips a throwaway file rather than the config.
func editAgentPromptCmd(seed string) tea.Cmd {
	f, err := os.CreateTemp("", "gitmoot-agent-prompt-*.md")
	if err != nil {
		return func() tea.Msg { return tui.AgentPromptEditedMsg{Err: err} }
	}
	path := f.Name()
	_, writeErr := f.WriteString(seed)
	closeErr := f.Close()
	if writeErr != nil || closeErr != nil {
		os.Remove(path)
		if writeErr == nil {
			writeErr = closeErr
		}
		return func() tea.Msg { return tui.AgentPromptEditedMsg{Err: writeErr} }
	}
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	parts := strings.Fields(editor)
	args := append(parts[1:], path)
	cmd := exec.Command(parts[0], args...)
	return tea.ExecProcess(cmd, func(runErr error) tea.Msg {
		defer os.Remove(path)
		if runErr != nil {
			return tui.AgentPromptEditedMsg{Err: runErr}
		}
		content, readErr := os.ReadFile(path)
		return tui.AgentPromptEditedMsg{Content: string(content), Err: readErr}
	})
}

func editConfigCmd(configFile string) tea.Cmd {
	editor := strings.TrimSpace(os.Getenv("EDITOR"))
	if editor == "" {
		editor = "vi"
	}
	// Honor a multi-word EDITOR ("code --wait"), splitting on spaces.
	parts := strings.Fields(editor)
	args := append(parts[1:], configFile)
	cmd := exec.Command(parts[0], args...)
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		return tui.ConfigEditedMsg{Err: err}
	})
}
