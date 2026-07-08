package memory

// Deterministic grooming detectors for `gitmoot memory groom` (RFC #737, P4.2).
//
// Grooming mechanizes the manual curation pass that periodically retires stale,
// low-signal confirmed memories (status/changelog snapshots, table-of-contents
// index notes, bare to-do lists, and exact duplicates). Everything here is a
// PURE, DB-free function of its inputs so the detectors can be unit-tested in
// isolation against real-shaped fixtures — same inputs ⇒ same proposal, every
// run. The db-coupled orchestration (reading the vault snapshot, writing the plan
// artifact, applying retirements in one transaction) lives in the cli package.
//
// P4.2 is PROPOSE + retire-only: the detectors emit a reviewable plan the owner
// applies explicitly. Over-long "brick" memories are only FLAGGED for a later,
// human/LLM rewrite pass (P4.3) — this track never rewrites content, only
// proposes retirements the owner confirms.

import (
	"crypto/sha256"
	"encoding/hex"
	"regexp"
	"sort"
	"strings"
)

// Groom detector reason tokens. They name WHY a memory was proposed for
// retirement and become the store retire reason as "groom:<token>" at apply time.
const (
	GroomReasonDuplicate       = "duplicate"
	GroomReasonStatusChangelog = "status-changelog"
	GroomReasonTaskList        = "task-list"
)

// GroomRewriteThreshold is the content length (in bytes) above which a memory is
// flagged as a REWRITE candidate rather than retired. Multi-fact "bricks" carry
// real signal that a blind retirement would lose, so P4.2 only lists them for the
// owner; the actual rewrite is deferred to the opt-in LLM pass (P4.3).
const GroomRewriteThreshold = 1200

// groomFirstLineMax caps the first_line preview carried in the plan so a brick's
// opening paragraph can't bloat the artifact.
const groomFirstLineMax = 160

// GroomCandidate is the DB-free projection of one active confirmed memory the
// detectors consider. It mirrors the fields the vault snapshot already exposes.
type GroomCandidate struct {
	ID      int64
	Key     string
	Content string
}

// GroomRetirement is one proposed retirement: the memory id, its key, the detector
// that flagged it, and a first-line preview so the owner can eyeball the plan.
type GroomRetirement struct {
	ID        int64
	Key       string
	Reason    string
	FirstLine string
}

// GroomRewriteFlag flags an over-long memory for owner review. P4.2 does not
// rewrite; it only records the id/key/size so the plan can list it.
type GroomRewriteFlag struct {
	ID    int64
	Key   string
	Chars int
}

// GroomStats is the roll-up carried in the plan artifact.
type GroomStats struct {
	TotalMemories       int            `json:"total_memories"`
	ProposedRetirements int            `json:"proposed_retirements"`
	RewriteFlags        int            `json:"rewrite_flags"`
	ByReason            map[string]int `json:"by_reason"`
}

// GroomProposal is the detectors' full output over a candidate set.
type GroomProposal struct {
	Retirements  []GroomRetirement
	RewriteFlags []GroomRewriteFlag
	Stats        GroomStats
}

// DetectGroomActions runs every deterministic detector over the candidate set and
// returns a stable proposal. Precedence per memory (a memory is proposed at most
// once): exact-duplicate content, then a bare to-do list, then a status/changelog/
// ToC snapshot. Over-long memories that survive retirement are flagged for a later
// rewrite. Output is fully deterministic and independent of input order (candidates
// are sorted by id first).
func DetectGroomActions(cands []GroomCandidate) GroomProposal {
	sorted := append([]GroomCandidate(nil), cands...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].ID < sorted[j].ID })

	retired := make(map[int64]bool, len(sorted))
	var retirements []GroomRetirement
	addRetire := func(c GroomCandidate, reason string) {
		if retired[c.ID] {
			return
		}
		retired[c.ID] = true
		retirements = append(retirements, GroomRetirement{
			ID:        c.ID,
			Key:       c.Key,
			Reason:    reason,
			FirstLine: groomFirstLine(c.Content),
		})
	}

	// (d) Exact-duplicate content: group by sha256(content), keep the lowest id in
	// each group, propose retiring the rest. sorted is id-ascending so group[0] is
	// always the lowest id; hashOrder preserves first-seen order for deterministic
	// output.
	byHash := make(map[string][]GroomCandidate, len(sorted))
	var hashOrder []string
	for _, c := range sorted {
		sum := sha256.Sum256([]byte(c.Content))
		h := hex.EncodeToString(sum[:])
		if _, seen := byHash[h]; !seen {
			hashOrder = append(hashOrder, h)
		}
		byHash[h] = append(byHash[h], c)
	}
	for _, h := range hashOrder {
		group := byHash[h]
		if len(group) < 2 {
			continue
		}
		for _, dup := range group[1:] {
			addRetire(dup, GroomReasonDuplicate)
		}
	}

	// (a)+(b) Pattern detectors over rows not already claimed as duplicates.
	for _, c := range sorted {
		if retired[c.ID] {
			continue
		}
		switch {
		case detectTaskListOnly(c.Content):
			addRetire(c, GroomReasonTaskList)
		case detectStatusChangelog(c.Content):
			addRetire(c, GroomReasonStatusChangelog)
		}
	}

	// (c) Rewrite flags over rows that survived retirement: extreme length.
	var flags []GroomRewriteFlag
	for _, c := range sorted {
		if retired[c.ID] {
			continue
		}
		if len(c.Content) > GroomRewriteThreshold {
			flags = append(flags, GroomRewriteFlag{ID: c.ID, Key: c.Key, Chars: len(c.Content)})
		}
	}

	byReason := map[string]int{}
	for _, r := range retirements {
		byReason[r.Reason]++
	}
	return GroomProposal{
		Retirements:  retirements,
		RewriteFlags: flags,
		Stats: GroomStats{
			TotalMemories:       len(sorted),
			ProposedRetirements: len(retirements),
			RewriteFlags:        len(flags),
			ByReason:            byReason,
		},
	}
}

// groomFirstLine returns the first non-blank line of content, trimmed and capped,
// for the plan's human-readable preview.
func groomFirstLine(content string) string {
	for _, line := range strings.Split(content, "\n") {
		t := strings.TrimSpace(line)
		if t == "" {
			continue
		}
		if len(t) > groomFirstLineMax {
			t = strings.TrimSpace(t[:groomFirstLineMax])
		}
		return t
	}
	return ""
}

// groomTaskLine matches a Markdown checkbox item ("- [ ]", "* [x]", "1. [X]").
var groomTaskLine = regexp.MustCompile(`^([-*]|\d+\.)\s+\[[ xX]\]`)

// groomTocLine matches a Markdown list item whose payload is a link ("- [Title]",
// "- [[wikilink]]") — the shape of a table-of-contents / changelog index note. The
// negative lookahead-free form excludes checkboxes (handled by groomTaskLine): the
// character after '[' must NOT be a checkbox marker (space, x, X immediately
// followed by ']').
var groomTocLine = regexp.MustCompile(`^[-*]\s+\[[^\]]`)

// groomDateLed matches a changelog line led by an ISO date, optionally after a
// list bullet ("2026-07-08 …", "- 2026-07-08 …", "* [2026-07-08] …").
var groomDateLed = regexp.MustCompile(`^[-*]?\s*\[?\d{4}-\d{2}-\d{2}`)

// isTaskLine reports whether a single line is a Markdown checkbox item.
func isTaskLine(line string) bool {
	return groomTaskLine.MatchString(strings.TrimSpace(line))
}

// isStatusChangelogLine reports whether a single line looks like a status,
// changelog, or table-of-contents entry: a "STATUS:" marker, a shipped/deployed
// changelog phrase, an ISO-date-led line, or a Markdown link list item.
func isStatusChangelogLine(line string) bool {
	t := strings.TrimSpace(line)
	if t == "" {
		return false
	}
	if isTaskLine(t) {
		// A checkbox is a task-list line, not a ToC/changelog line; keep the two
		// detectors' populations disjoint so a pure checkbox list is reported as
		// task-list (not status-changelog).
		return false
	}
	upper := strings.ToUpper(t)
	lower := strings.ToLower(t)
	switch {
	case strings.HasPrefix(upper, "STATUS:"):
		return true
	case strings.Contains(t, "SHIPPED"):
		return true
	case strings.Contains(lower, "merged & deployed"),
		strings.Contains(lower, "merged and deployed"),
		strings.Contains(lower, "shipped & deployed"),
		strings.Contains(lower, "shipped and deployed"):
		return true
	case groomTocLine.MatchString(t):
		return true
	case groomDateLed.MatchString(t):
		return true
	}
	return false
}

// nonBlankLines returns the trimmed-nonblank lines of content.
func nonBlankLines(content string) []string {
	var out []string
	for _, line := range strings.Split(content, "\n") {
		if strings.TrimSpace(line) != "" {
			out = append(out, line)
		}
	}
	return out
}

// detectTaskListOnly reports whether content is nothing but a to-do list: at least
// one non-blank line and EVERY non-blank line is a checkbox item.
func detectTaskListOnly(content string) bool {
	lines := nonBlankLines(content)
	if len(lines) == 0 {
		return false
	}
	for _, line := range lines {
		if !isTaskLine(line) {
			return false
		}
	}
	return true
}

// groomStatusDominance is the fraction of non-blank lines that must look like
// status/changelog/ToC entries for the whole note to be proposed for retirement.
// Requiring dominance (not a single marker) protects substantive memories that
// merely mention "SHIPPED #754" in one line from being retired — only notes that
// ARE a changelog/index get flagged.
const groomStatusDominance = 0.8

// detectStatusChangelog reports whether content is predominantly a status,
// changelog, or table-of-contents snapshot: at least one non-blank line and at
// least groomStatusDominance of them are status/changelog/ToC lines.
func detectStatusChangelog(content string) bool {
	lines := nonBlankLines(content)
	if len(lines) == 0 {
		return false
	}
	matching := 0
	for _, line := range lines {
		if isStatusChangelogLine(line) {
			matching++
		}
	}
	return float64(matching) >= groomStatusDominance*float64(len(lines))
}
