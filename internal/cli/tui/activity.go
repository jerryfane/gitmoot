package tui

import (
	"strconv"
	"strings"
)

// activitySelectable is the flat list of jobs the Activity cursor can land on —
// each active root followed by its delegation children — so enter can open the
// detail (request + result) of a root OR a specific delegate.
func (m Model) activitySelectable() []JobRow {
	var out []JobRow
	for _, r := range m.snap.Activity {
		out = append(out, JobRow{ID: r.JobID, Agent: r.Agent, Type: r.Action, State: r.State})
		for _, c := range r.Children {
			out = append(out, JobRow{ID: c.ID, Agent: c.Agent, Type: c.Action, State: c.State})
		}
	}
	return out
}

// activityUnderCursor returns the job (root or delegate) under the Activity
// cursor, if any.
func (m Model) activityUnderCursor() (JobRow, bool) {
	if pages[m.selected].page != pageActivity {
		return JobRow{}, false
	}
	sel := m.activitySelectable()
	if m.activityCursor < 0 || m.activityCursor >= len(sel) {
		return JobRow{}, false
	}
	return sel[m.activityCursor], true
}

// activityTreeCap is how many delegation trees fit the Activity page, leaving
// room for the title/intro, the more/earlier markers, and the footer. A tree is
// roughly a root line + progress + up to ~5 children + spacing.
func activityTreeCap(height int) int {
	if n := (height - 7) / 8; n >= 1 {
		return n
	}
	return 1
}

// activityContent renders the Activity page: delegation trees with
// queued/running work, newest first. Each root shows the coordinator line, a
// progress summary, and the delegation children (which agent is doing what, and
// its state) plus the continuation job. The cursor walks roots AND children;
// enter opens the selected job's detail (its request + result). Trees are
// windowed around the cursor so the page always fits and the cursor stays
// visible; "↑/↓ N more trees" markers show what is scrolled off.
func (m Model) activityContent() string {
	roots := m.snap.Activity
	if len(roots) == 0 {
		return m.loadingOr("No active jobs — nothing is running right now.", !m.loadedAt.IsZero())
	}

	// counts[i] = selectable rows in tree i (root + its children); find which
	// tree the flat cursor falls in so we can window around it.
	counts := make([]int, len(roots))
	cursorTree := 0
	cum := 0
	for i, r := range roots {
		counts[i] = 1 + len(r.Children)
		if m.activityCursor >= cum && m.activityCursor < cum+counts[i] {
			cursorTree = i
		}
		cum += counts[i]
	}

	capacity := activityTreeCap(m.height)
	start := 0
	if len(roots) > capacity {
		start = cursorTree - capacity/2
		if start < 0 {
			start = 0
		}
		if start > len(roots)-capacity {
			start = len(roots) - capacity
		}
	}
	end := start + capacity
	if end > len(roots) {
		end = len(roots)
	}

	// pos = flat selectable position of the first row in the window.
	pos := 0
	for i := 0; i < start; i++ {
		pos += counts[i]
	}

	var b strings.Builder
	b.WriteString(mutedStyle.Render("Delegation trees with queued/running work (live, refreshes every 5s).") + "\n\n")
	if start > 0 {
		b.WriteString(mutedStyle.Render("  ↑ "+strconv.Itoa(start)+" more trees") + "\n")
	}
	for ti := start; ti < end; ti++ {
		r := roots[ti]
		marker := "  "
		id := r.JobID
		if pos == m.activityCursor {
			marker = "▸ "
			id = selectedRowStyle.Render(id)
		}
		line := marker + id + "  " + r.Agent + "  " + r.Action + "  " + jobStateColor(r.State)
		if r.Repo != "" {
			line += "  " + mutedStyle.Render(r.Repo)
		}
		b.WriteString(line + "\n")
		pos++

		if r.Total > 0 {
			b.WriteString("    " + mutedStyle.Render(
				strconv.Itoa(r.Total)+" delegations · "+
					strconv.Itoa(r.Running)+" running · "+
					strconv.Itoa(r.Queued)+" queued · "+
					strconv.Itoa(r.Blocked)+" blocked · "+
					strconv.Itoa(r.Done)+" done") + "\n")
			for _, c := range r.Children {
				cm := "  "
				agent := dash(c.Agent)
				if pos == m.activityCursor {
					cm = "▸ "
					agent = selectedRowStyle.Render(agent)
				}
				b.WriteString("    " + cm + agent + "  " + truncate(c.Action, 24) + "  " + jobStateColor(c.State) + "\n")
				pos++
			}
			if r.ContinuationID != "" {
				b.WriteString("      " + mutedStyle.Render("continuation") + "  " + jobStateColor(r.ContinuationState) + "\n")
			}
		}
		b.WriteByte('\n')
	}
	if end < len(roots) {
		b.WriteString(mutedStyle.Render("  ↓ "+strconv.Itoa(len(roots)-end)+" more trees") + "\n")
	}
	b.WriteString(mutedStyle.Render("↑/↓ select root or delegate · enter open its detail (request + result)"))
	b.WriteByte('\n')
	return b.String()
}
