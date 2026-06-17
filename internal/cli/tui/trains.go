package tui

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/jerryfane/gitmoot/internal/skillopt"
)

// openTrainDetail shows the detail view for the train under the cursor.
func (m *Model) openTrainDetail() {
	if t, ok := m.trainUnderCursor(); ok {
		m.activeTrain = t
		m.mode = modeTrainDetail
	}
}

// trainUnderCursor returns the session under the Trains cursor, if any. The
// cursor indexes the display-ordered list (orderedTrains), not raw snapshot
// order, so it resolves to the row the user sees highlighted.
func (m Model) trainUnderCursor() (TrainSession, bool) {
	if pages[m.selected].page != pageTrains {
		return TrainSession{}, false
	}
	ordered := m.orderedTrains()
	if m.trainCursor < 0 || m.trainCursor >= len(ordered) {
		return TrainSession{}, false
	}
	return ordered[m.trainCursor], true
}

// openTrainStop enters the stop-reason overlay for a live session.
func (m *Model) openTrainStop(t TrainSession) tea.Cmd {
	m.activeTrain = t
	m.actionErr = ""
	m.actionBusy = false
	m.mode = modeTrainStopReason
	ti := textinput.New()
	ti.Placeholder = "why is this run being abandoned?"
	m.input = ti
	return m.input.Focus()
}

// openTrainDelete enters the delete confirmation for a terminal session.
func (m *Model) openTrainDelete(t TrainSession) {
	m.activeTrain = t
	m.actionErr = ""
	m.actionBusy = false
	m.mode = modeConfirmTrainDelete
}

// updateTrainOverlay handles keys in the stop/delete/repo-cleanup modes. Like
// the job confirms, an overlay stays open while its action is in flight so the
// eventual error is never dropped silently.
func (m Model) updateTrainOverlay(msg tea.KeyMsg) (tea.Model, tea.Cmd) {
	switch m.mode {
	case modeTrainStopReason:
		switch msg.String() {
		case "esc":
			if m.actionBusy {
				return m, nil
			}
			m.mode = modeNormal
			m.actionErr = ""
		case "enter":
			if m.actionBusy {
				return m, nil
			}
			reason := strings.TrimSpace(m.input.Value())
			if reason == "" {
				m.actionErr = "a reason is required"
			} else {
				m.actionBusy = true
				m.actionErr = ""
				m.viewport.SetContent(m.content())
				return m, trainStopCmd(m.deps, m.activeTrain.ID, reason)
			}
		default:
			// Freeze the reason while the stop is in flight so a retry after an
			// error submits exactly what is rendered.
			if m.actionBusy {
				return m, nil
			}
			var cmd tea.Cmd
			m.input, cmd = m.input.Update(msg)
			m.viewport.SetContent(m.content())
			return m, cmd
		}
		m.viewport.SetContent(m.content())
		return m, nil
	case modeConfirmTrainDelete:
		switch msg.String() {
		case "y", "Y":
			if m.actionBusy {
				return m, nil
			}
			m.actionBusy = true
			m.actionErr = ""
			m.viewport.SetContent(m.content())
			return m, trainDeleteCmd(m.deps, m.activeTrain.ID)
		default:
			if m.actionBusy {
				return m, nil
			}
			m.mode = modeNormal
			m.actionErr = ""
		}
		m.viewport.SetContent(m.content())
		return m, nil
	case modeConfirmTrainRepoCleanup:
		switch msg.String() {
		case "y", "Y":
			if m.actionBusy {
				return m, nil
			}
			m.actionBusy = true
			m.actionErr = ""
			m.viewport.SetContent(m.content())
			return m, trainRepoCleanupCmd(m.deps, m.pendingRepos)
		default:
			if m.actionBusy {
				return m, nil
			}
			// The session is already gone; declining just keeps the repos.
			m.mode = modeNormal
			m.actionErr = ""
			m.pendingRepos = nil
		}
		m.viewport.SetContent(m.content())
		return m, nil
	}
	return m, nil
}

func trainStopCmd(deps Deps, id, reason string) tea.Cmd {
	return func() tea.Msg {
		if deps.StopTrain == nil {
			return trainStopMsg{}
		}
		return trainStopMsg{err: deps.StopTrain(id, reason)}
	}
}

func trainDeleteCmd(deps Deps, id string) tea.Cmd {
	return func() tea.Msg {
		if deps.DeleteTrain == nil {
			return trainDeleteMsg{}
		}
		repos, err := deps.DeleteTrain(id)
		return trainDeleteMsg{repos: repos, err: err}
	}
}

func trainRepoCleanupCmd(deps Deps, repos []string) tea.Cmd {
	return func() tea.Msg {
		if deps.DeleteTrainRepo == nil {
			return trainRepoCleanupMsg{}
		}
		var failed, errs []string
		for _, repo := range repos {
			if err := deps.DeleteTrainRepo(repo); err != nil {
				failed = append(failed, repo)
				errs = append(errs, err.Error())
			}
		}
		return trainRepoCleanupMsg{failed: failed, errs: errs}
	}
}

func (m Model) trainStopView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("stop " + m.activeTrain.ID))
	b.WriteString("\n\n")
	b.WriteString("Stopping abandons the current run (phase " + dash(m.activeTrain.Phase) + ").\n\n")
	b.WriteString("reason: " + m.input.View())
	b.WriteByte('\n')
	if m.actionErr != "" {
		b.WriteString("\n" + errorStyle.Render(m.actionErr) + "\n")
	}
	b.WriteString("\n")
	if m.actionBusy {
		b.WriteString(mutedStyle.Render("stopping…"))
	} else {
		b.WriteString(mutedStyle.Render("enter stop  esc cancel"))
	}
	return b.String()
}

func (m Model) trainDeleteConfirmView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("Delete train " + m.activeTrain.ID))
	b.WriteString("\n\n")
	b.WriteString("phase " + dash(m.activeTrain.Phase) + " · " + dash(m.activeTrain.Repo) + "\n\n")
	b.WriteString("Delete this session and all its history? (y/n)\n")
	if m.actionErr != "" {
		b.WriteString("\n" + errorStyle.Render(m.actionErr) + "\n")
	}
	b.WriteString("\n")
	if m.actionBusy {
		b.WriteString(mutedStyle.Render("deleting…"))
	} else {
		b.WriteString(mutedStyle.Render("y delete  n/esc cancel"))
	}
	return b.String()
}

func (m Model) trainRepoCleanupView() string {
	var b strings.Builder
	b.WriteString(headerStyle.Render("session deleted"))
	b.WriteString("\n\n")
	b.WriteString("gitmoot created these GitHub repos for it:\n")
	for _, repo := range m.pendingRepos {
		b.WriteString("  " + repo + "\n")
	}
	b.WriteString("\nAlso delete them from GitHub? (y/n)\n")
	if m.actionErr != "" {
		b.WriteString("\n" + errorStyle.Render(m.actionErr) + "\n")
	}
	b.WriteString("\n")
	if m.actionBusy {
		b.WriteString(mutedStyle.Render("deleting repos…"))
	} else {
		b.WriteString(mutedStyle.Render("y delete repos  n/esc keep them"))
	}
	return b.String()
}

// Train status categories. A session lands in exactly one section; the ordering
// of the constants is also the top-to-bottom render order of the sections.
const (
	trainCatActive = iota
	trainCatBlocked
	trainCatDone
)

var trainSectionTitles = map[int]string{
	trainCatActive:  "Active",
	trainCatBlocked: "Blocked",
	trainCatDone:    "Done",
}

// trainStatusCategory buckets a phase into Active/Blocked/Done for the grouped
// list. Blocked is checked before the broad Active default so a stalled
// heartbeat or stale lock surfaces under Blocked rather than masquerading as
// live work. Done covers the terminal states.
func trainStatusCategory(phase string) int {
	p := skillopt.NormalizeTrainState(phase)
	switch p {
	case skillopt.TrainStateRunAbandoned,
		skillopt.TrainStateCandidatePromoted,
		skillopt.TrainStateCandidateRejected,
		skillopt.TrainStateOptimizerCompletedNoCandidate:
		return trainCatDone
	}
	switch p {
	case "blocked_stale_lock", "failed_unrecoverable", "blocked_config":
		return trainCatBlocked
	}
	if strings.HasSuffix(p, "_heartbeat_stale") {
		return trainCatBlocked
	}
	// Everything else — the ordered in-progress phases plus the live lock
	// phases (generating_options/optimizer_running/preflight_running/
	// recovery_available) — is Active.
	return trainCatActive
}

// Only an explicit "-vN" suffix marks a version lineage. A bare "-N" (e.g. a
// timestamp tail or an independently named run) is NOT treated as a version, so
// unrelated sessions like nightly-1/nightly-2 do not collapse together.
var trainLineageSuffix = regexp.MustCompile(`-v\d+$`)

// trainLineageBase strips an explicit version suffix ("-v3") from a session id,
// returning the shared base name and whether a suffix was present. Sessions in
// the same status section that share a base collapse under one lineage line.
func trainLineageBase(id string) (string, bool) {
	loc := trainLineageSuffix.FindStringIndex(id)
	if loc == nil {
		return id, false
	}
	return id[:loc[0]], true
}

// trainGroup is one unit of the Trains display: either a lone session
// (len(members)==1, base "") or a collapsed lineage (members share base).
type trainGroup struct {
	base    string
	members []TrainSession
}

// trainSectionGroups returns the display-ordered groups for one status category.
// Sessions are taken in snapshot order; a base shared by more than one session
// in the category collapses into a single group whose members stay contiguous
// (in snapshot order), emitted at the position of its first member. This keeps
// every lineage child under its own parent even when members are not adjacent
// in the snapshot.
func (m Model) trainSectionGroups(cat int) []trainGroup {
	var rows []TrainSession
	for _, t := range m.snap.Trains {
		if trainStatusCategory(t.Phase) == cat {
			rows = append(rows, t)
		}
	}
	counts := map[string]int{}
	for _, t := range rows {
		if base, ok := trainLineageBase(t.ID); ok {
			counts[base]++
		}
	}
	emitted := map[string]bool{}
	var groups []trainGroup
	for _, t := range rows {
		base, ok := trainLineageBase(t.ID)
		if ok && counts[base] > 1 {
			if emitted[base] {
				continue
			}
			emitted[base] = true
			g := trainGroup{base: base}
			for _, u := range rows {
				if b, o := trainLineageBase(u.ID); o && b == base {
					g.members = append(g.members, u)
				}
			}
			groups = append(groups, g)
			continue
		}
		groups = append(groups, trainGroup{members: []TrainSession{t}})
	}
	return groups
}

// orderedTrains is the flat, display-ordered list of sessions — the exact
// top-to-bottom order rows render in (sections Active→Blocked→Done, lineages
// contiguous). The Trains cursor indexes into THIS slice, so ↑/↓ steps through
// the visible list in order and selection matches the highlighted row.
func (m Model) orderedTrains() []TrainSession {
	var out []TrainSession
	for _, cat := range []int{trainCatActive, trainCatBlocked, trainCatDone} {
		for _, g := range m.trainSectionGroups(cat) {
			out = append(out, g.members...)
		}
	}
	return out
}

func (m Model) trainsContent() string {
	if m.mode == modeTrainDetail {
		return m.trainDetail()
	}
	if len(m.snap.Trains) == 0 {
		return m.loadingOr("No train sessions.", !m.loadedAt.IsZero())
	}
	var b strings.Builder

	// pos is the display position of the next selectable session row; it must
	// advance in lockstep with orderedTrains() so the cursor highlights and
	// selects the same row. Section headers and lineage parents are display-only
	// and consume no position.
	pos := 0
	first := true
	for _, cat := range []int{trainCatActive, trainCatBlocked, trainCatDone} {
		groups := m.trainSectionGroups(cat)
		if len(groups) == 0 {
			continue
		}
		if !first {
			b.WriteByte('\n')
		}
		first = false
		b.WriteString(headerStyle.Render(trainSectionTitles[cat]))
		b.WriteByte('\n')
		for _, g := range groups {
			if len(g.members) > 1 {
				b.WriteString("  " + g.base + "  " + mutedStyle.Render("×"+strconv.Itoa(len(g.members))) + "\n")
				for _, t := range g.members {
					m.writeTrainRow(&b, t, pos == m.trainCursor, "    ")
					pos++
				}
				continue
			}
			m.writeTrainRow(&b, g.members[0], pos == m.trainCursor, "  ")
			pos++
		}
	}

	b.WriteString(mutedStyle.Render("enter open  s stop  d delete"))
	b.WriteByte('\n')
	return b.String()
}

// writeTrainRow renders a single selectable session row at the given indent.
// selected is true when this row's display position is under the cursor; the
// cursor row replaces the trailing two spaces of indent with the "▸ " marker so
// columns stay aligned.
func (m Model) writeTrainRow(b *strings.Builder, t TrainSession, selected bool, indent string) {
	phase := t.Phase
	if deadTrainPhase(t.Phase) {
		phase = mutedStyle.Render(t.Phase)
	}
	if selected {
		b.WriteString(indent[:len(indent)-2] + "▸ " + selectedRowStyle.Render(t.ID) + "  " + phase + "\n")
		return
	}
	b.WriteString(indent + t.ID + "  " + phase + "\n")
}

func (m Model) trainDetail() string {
	t := m.activeTrain
	var b strings.Builder
	b.WriteString(headerStyle.Render(t.ID))
	b.WriteString("\n\n")
	rows := [][]string{
		{"phase", dash(t.Phase)},
		{"candidate", dash(t.Candidate)},
		{"repo", dash(t.Repo)},
	}
	b.WriteString(renderRows(rows))
	b.WriteByte('\n')
	b.WriteString(headerStyle.Render("locks"))
	b.WriteByte('\n')
	locks := trainLocks(m.snap.ResourceLocks, t.ID)
	if len(locks) == 0 {
		b.WriteString(mutedStyle.Render("none"))
		b.WriteByte('\n')
	} else {
		for _, l := range locks {
			state := "active"
			if l.Stale {
				state = redStyle.Render("stale")
			}
			b.WriteString(l.Key + "  " + state + "\n")
		}
	}
	return b.String()
}

// trainLocks returns the resource locks for a session. Train lock keys have the
// form "<resource>:<sessionID>[:<iterationID>]", so the session id is matched as
// a whole colon-delimited segment — substring matching would cross-match
// sessions whose ids are prefixes of one another (e.g. "s1" vs "s12").
func trainLocks(locks []ResourceLock, sessionID string) []ResourceLock {
	out := []ResourceLock{}
	for _, l := range locks {
		for _, seg := range strings.Split(l.Key, ":") {
			if seg == sessionID {
				out = append(out, l)
				break
			}
		}
	}
	return out
}

// deadTrainPhase reports whether a session is in a terminal state (the
// canonical list lives in skillopt, so new terminal states gate correctly
// here without a second hand-kept copy).
func deadTrainPhase(phase string) bool {
	return skillopt.IsTerminalTrainState(phase)
}
