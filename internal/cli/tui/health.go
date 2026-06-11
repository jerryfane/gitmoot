package tui

import (
	"strconv"
	"strings"
)

// healthContent renders the Health page: the daemon block (running state,
// persisted flags, log error tail) followed by the environment checks.
func (m Model) healthContent() string {
	var b strings.Builder

	b.WriteString(headerStyle.Render("daemon"))
	b.WriteByte('\n')
	d := m.snap.Daemon
	if d.Running {
		b.WriteString(greenStyle.Render("running") + "  " + mutedStyle.Render("pid "+strconv.Itoa(d.PID)) + "\n")
	} else {
		hint := "press s to start"
		if m.daemonBusy {
			hint = "starting…"
		}
		b.WriteString(redStyle.Render("stopped") + "  " + mutedStyle.Render(hint) + "\n")
		if m.daemonErr != "" {
			b.WriteString(errorStyle.Render(m.daemonErr) + "\n")
		}
	}
	rows := [][]string{}
	if len(d.Flags) > 0 {
		rows = append(rows, []string{"flags", strings.Join(d.Flags, " ")})
	}
	if d.WorkDir != "" {
		rows = append(rows, []string{"workdir", d.WorkDir})
	}
	if d.StartedAt != "" {
		rows = append(rows, []string{"started", d.StartedAt})
	}
	if d.LogFile != "" {
		rows = append(rows, []string{"log", d.LogFile})
	}
	if len(rows) > 0 {
		b.WriteString(renderRows(rows))
	}
	if len(d.LogErrors) > 0 {
		b.WriteByte('\n')
		b.WriteString(mutedStyle.Render("recent log errors:"))
		b.WriteByte('\n')
		for _, line := range d.LogErrors {
			b.WriteString(redStyle.Render("│ "+truncate(line, 100)) + "\n")
		}
	}

	b.WriteByte('\n')
	b.WriteString(headerStyle.Render("environment"))
	b.WriteByte('\n')
	switch {
	case m.healthErr != "":
		b.WriteString(errorStyle.Render(m.healthErr) + "\n")
	case m.healthLoading:
		b.WriteString(mutedStyle.Render("running checks…") + "\n")
	case !m.healthLoaded:
		b.WriteString(mutedStyle.Render("press r to run checks") + "\n")
	case len(m.healthChecks) == 0:
		b.WriteString(mutedStyle.Render("no checks") + "\n")
	default:
		checkRows := [][]string{{"CHECK", "STATUS", "DETAIL"}}
		for _, check := range m.healthChecks {
			checkRows = append(checkRows, []string{check.Name, healthStatusColor(check.Status), truncate(dash(check.Detail), 70)})
		}
		b.WriteString(renderRows(checkRows))
	}

	b.WriteByte('\n')
	b.WriteString(mutedStyle.Render("r re-run checks · s start daemon"))
	b.WriteByte('\n')
	return b.String()
}

func healthStatusColor(status string) string {
	switch status {
	case "ok":
		return greenStyle.Render(status)
	case "fail":
		return redStyle.Render(status)
	case "warn":
		return redStyle.Render(status)
	default:
		return status
	}
}
