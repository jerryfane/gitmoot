package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"regexp"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/mention"
)

// runChat is the `gitmoot chat` command group (#534, V1 local-only): a durable,
// repo-aware chat ledger where registered agents + the human converse in
// threads, tag each other, and (in stage 2) promote messages into jobs. This
// stage ships storage + core CLI only — promotion (`task`) and the ask-gate
// answer channel (`answer`) are stage 2 and are intentionally NOT wired here.
func runChat(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printChatUsage(stdout)
		return 0
	}
	switch args[0] {
	case "create":
		return runChatCreate(args[1:], stdout, stderr)
	case "list":
		return runChatList(args[1:], stdout, stderr)
	case "show":
		return runChatShow(args[1:], stdout, stderr)
	case "send":
		return runChatSend(args[1:], stdout, stderr)
	case "inbox":
		return runChatInbox(args[1:], stdout, stderr)
	case "close":
		return runChatSetState(args[1:], stdout, stderr, db.ChatThreadStateArchived)
	case "reopen":
		return runChatSetState(args[1:], stdout, stderr, db.ChatThreadStateOpen)
	case "rename":
		return runChatRename(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown chat command %q\n\n", args[0])
		printChatUsage(stderr)
		return 2
	}
}

func printChatUsage(w io.Writer) {
	fmt.Fprintln(w, "Native agent chat: durable, repo-aware threads for agents + humans (#534).")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  gitmoot chat create <name> --repo owner/repo [--topic \"title\"] [--json]")
	fmt.Fprintln(w, "  gitmoot chat list [--repo owner/repo] [--all] [--json]")
	fmt.Fprintln(w, "  gitmoot chat show <thread> [--repo owner/repo] [--limit N] [--json]")
	fmt.Fprintln(w, "  gitmoot chat send <thread> \"message\" [--as agent] [--repo owner/repo] [--ref kind:value ...] [--json]")
	fmt.Fprintln(w, "  gitmoot chat inbox <agent> [--unread] [--json]")
	fmt.Fprintln(w, "  gitmoot chat close|reopen <thread> [--repo owner/repo] [--json]")
	fmt.Fprintln(w, "  gitmoot chat rename <thread> \"new name\" [--repo owner/repo] [--json]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "  <name> is the topic-path-safe slug ([a-z0-9-]); <thread> is a slug or thread id.")
	fmt.Fprintln(w, "  A message never starts work — promotion (`chat task`) and `chat answer` are stage 2.")
}

// slugRe is the topic-path-safe thread slug: lowercase alphanumerics with single
// interior hyphens, no leading/trailing/double hyphen. It inherently excludes
// '+', '#', '/', whitespace, and uppercase — so a slug always derives a valid
// MQTT topic later (`gitmoot/<repo>/<slug>`).
var slugRe = regexp.MustCompile(`^[a-z0-9]+(-[a-z0-9]+)*$`)

// slugify lowercases and collapses runs of non-[a-z0-9] into single hyphens,
// trimming leading/trailing hyphens. The result is then validated against
// slugRe so a name that slugifies to nothing is rejected rather than silently
// accepted.
func slugify(name string) string {
	var b strings.Builder
	lastHyphen := false
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9'):
			b.WriteRune(r)
			lastHyphen = false
		default:
			if !lastHyphen && b.Len() > 0 {
				b.WriteByte('-')
				lastHyphen = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}

// ---- output shapes ---------------------------------------------------------

type chatThreadOutput struct {
	ID        string `json:"id"`
	Slug      string `json:"slug"`
	Name      string `json:"name"`
	Repo      string `json:"repo"`
	Origin    string `json:"origin"`
	State     string `json:"state"`
	CreatedBy string `json:"created_by,omitempty"`
	UpdatedAt string `json:"updated_at,omitempty"`
}

func chatThreadToOutput(t db.ChatThread) chatThreadOutput {
	return chatThreadOutput{
		ID: t.ID, Slug: t.Slug, Name: t.Name, Repo: t.Repo, Origin: t.Origin,
		State: t.State, CreatedBy: t.CreatedBy, UpdatedAt: t.UpdatedAt,
	}
}

type chatMessageOutput struct {
	ID            string       `json:"id"`
	Seq           int64        `json:"seq"`
	TsMs          int64        `json:"ts_ms"`
	AuthorKind    string       `json:"author_kind"`
	AuthorName    string       `json:"author_name"`
	AuthorOrigin  string       `json:"author_origin,omitempty"`
	Kind          string       `json:"kind"`
	Body          string       `json:"body"`
	Mentions      []string     `json:"mentions,omitempty"`
	Refs          []db.ChatRef `json:"refs,omitempty"`
	ReplyTo       string       `json:"reply_to,omitempty"`
	PromotedJobID string       `json:"promoted_job_id,omitempty"`
}

// ---- create ----------------------------------------------------------------

func runChatCreate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat create", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	repo := fs.String("repo", "", "repo scope as owner/repo (required)")
	topic := fs.String("topic", "", "optional human display title (defaults to the slug)")
	jsonOut := fs.Bool("json", false, "print the created thread as JSON")
	name, code := parseChatOnePositional(fs, args, stderr, "chat create requires a <name>")
	if name == "" {
		return code
	}
	if strings.TrimSpace(*repo) == "" {
		fmt.Fprintln(stderr, "chat create requires --repo owner/repo")
		return 2
	}
	slug := slugify(name)
	if !slugRe.MatchString(slug) {
		fmt.Fprintf(stderr, "invalid chat name %q: a thread slug must be topic-path-safe ([a-z0-9-])\n", name)
		return 2
	}

	var out chatThreadOutput
	if err := withStore(*home, func(store *db.Store) error {
		thread, err := store.CreateChatThread(context.Background(), db.ChatThread{
			Slug:      slug,
			Name:      strings.TrimSpace(*topic),
			Repo:      strings.TrimSpace(*repo),
			CreatedBy: db.ChatAuthorKindHuman,
		})
		if err != nil {
			return err
		}
		out = chatThreadToOutput(thread)
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "chat create: %v\n", err)
		return 1
	}
	if *jsonOut {
		_ = writeJSON(stdout, out)
		return 0
	}
	writeLine(stdout, "thread: %s", out.Slug)
	writeLine(stdout, "id: %s", out.ID)
	writeLine(stdout, "name: %s", out.Name)
	writeLine(stdout, "repo: %s", out.Repo)
	writeLine(stdout, "state: %s", out.State)
	return 0
}

// ---- list ------------------------------------------------------------------

func runChatList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	repo := fs.String("repo", "", "filter by repo (owner/repo)")
	all := fs.Bool("all", false, "include archived (closed) threads")
	jsonOut := fs.Bool("json", false, "print as JSON")
	if err := parseChatFlags(fs, args, stderr); err != nil {
		return 2
	}
	state := db.ChatThreadStateOpen
	if *all {
		state = ""
	}

	var out []chatThreadOutput
	if err := withStore(*home, func(store *db.Store) error {
		threads, err := store.ListChatThreads(context.Background(), strings.TrimSpace(*repo), state)
		if err != nil {
			return err
		}
		for _, t := range threads {
			out = append(out, chatThreadToOutput(t))
		}
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "chat list: %v\n", err)
		return 1
	}
	if *jsonOut {
		_ = writeJSON(stdout, out)
		return 0
	}
	if len(out) == 0 {
		writeLine(stdout, "no chat threads")
		return 0
	}
	for _, t := range out {
		marker := ""
		if t.State == db.ChatThreadStateArchived {
			marker = " [archived]"
		}
		writeLine(stdout, "%-24s %-20s %s%s", t.Slug, t.Repo, t.Name, marker)
	}
	return 0
}

// ---- show ------------------------------------------------------------------

func runChatShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	repo := fs.String("repo", "", "repo scope to disambiguate a slug across repos")
	limit := fs.Int("limit", 0, "show only the most recent N messages (0 = all)")
	jsonOut := fs.Bool("json", false, "print as JSON")
	ref, code := parseChatThreadPositional(fs, args, stderr, "chat show requires a <thread>")
	if ref == "" {
		return code
	}

	var thread chatThreadOutput
	var msgs []chatMessageOutput
	if err := withStore(*home, func(store *db.Store) error {
		t, err := resolveChatThread(context.Background(), store, ref, strings.TrimSpace(*repo))
		if err != nil {
			return err
		}
		thread = chatThreadToOutput(t)
		messages, err := store.ListChatMessages(context.Background(), t.ID, *limit)
		if err != nil {
			return err
		}
		for _, m := range messages {
			msgs = append(msgs, chatMessageFromDB(m))
		}
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "chat show: %v\n", err)
		return 1
	}
	if *jsonOut {
		_ = writeJSON(stdout, struct {
			Thread   chatThreadOutput    `json:"thread"`
			Messages []chatMessageOutput `json:"messages"`
		}{Thread: thread, Messages: msgs})
		return 0
	}
	writeLine(stdout, "thread: %s (%s)", thread.Slug, thread.State)
	writeLine(stdout, "repo: %s", thread.Repo)
	if len(msgs) == 0 {
		writeLine(stdout, "(no messages)")
		return 0
	}
	for _, m := range msgs {
		writeLine(stdout, "#%d [%s] %s: %s", m.Seq, m.Kind, m.AuthorName, m.Body)
		if len(m.Mentions) > 0 {
			writeLine(stdout, "    mentions: %s", strings.Join(m.Mentions, ", "))
		}
		if m.PromotedJobID != "" {
			writeLine(stdout, "    promoted job: %s", m.PromotedJobID)
		}
		for _, r := range m.Refs {
			writeLine(stdout, "    ref: %s:%s", r.Kind, r.ID)
		}
	}
	return 0
}

// ---- send ------------------------------------------------------------------

func runChatSend(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat send", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	repo := fs.String("repo", "", "repo scope to disambiguate a slug across repos")
	as := fs.String("as", "", "author the message as this registered agent (default: human)")
	jsonOut := fs.Bool("json", false, "print the created message as JSON")
	var refs chatRefFlags
	fs.Var(&refs, "ref", "attach a structured ref as kind:value (repeatable)")
	// <thread> and "message" are two positionals, both before the flags.
	if len(args) < 2 || args[0] == "-h" || args[0] == "--help" {
		if len(args) >= 1 && (args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		fmt.Fprintln(stderr, "chat send requires a <thread> and a \"message\"")
		return 2
	}
	ref := strings.TrimSpace(args[0])
	body := args[1]
	if ref == "" || strings.HasPrefix(ref, "-") {
		fmt.Fprintln(stderr, "chat send requires a <thread> as the first argument")
		return 2
	}
	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "chat send accepts exactly one <thread> and one \"message\"")
		return 2
	}
	if strings.TrimSpace(body) == "" {
		fmt.Fprintln(stderr, "chat send requires a non-empty message")
		return 2
	}

	authorKind := db.ChatAuthorKindHuman
	authorName := "human"
	if a := strings.TrimSpace(*as); a != "" {
		authorKind = db.ChatAuthorKindAgent
		authorName = a
	}

	var out chatMessageOutput
	if err := withStore(*home, func(store *db.Store) error {
		ctx := context.Background()
		thread, err := resolveChatThread(ctx, store, ref, strings.TrimSpace(*repo))
		if err != nil {
			return err
		}
		if thread.State == db.ChatThreadStateArchived {
			return fmt.Errorf("thread %q is archived; reopen it before sending", thread.Slug)
		}
		if authorKind == db.ChatAuthorKindAgent {
			if _, err := store.GetAgent(ctx, authorName); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					return fmt.Errorf("agent %q not found (use a registered agent for --as)", authorName)
				}
				return err
			}
		}
		mentions := mention.Parse(body)
		msg, err := store.AddChatMessage(ctx, db.ChatMessage{
			ThreadID:   thread.ID,
			AuthorKind: authorKind,
			AuthorName: authorName,
			Kind:       db.ChatKindChat,
			Body:       body,
			Mentions:   mentions,
			Refs:       refsToChatRefs(refs, thread.Repo),
		})
		if err != nil {
			return err
		}
		// Resolve each mention against the registry: known -> unread inbox row;
		// unknown -> recorded resolved=0 for audit + a stderr warning. A bad
		// mention NEVER fails the send.
		mentionRows := make([]db.ChatMention, 0, len(mentions))
		for _, name := range mentions {
			known := true
			if _, err := store.GetAgent(ctx, name); err != nil {
				if errors.Is(err, sql.ErrNoRows) {
					known = false
					fmt.Fprintf(stderr, "warning: @%s is not a registered agent; mention recorded but not delivered\n", name)
				} else {
					return err
				}
			}
			mentionRows = append(mentionRows, db.ChatMention{
				MessageID: msg.ID,
				ThreadID:  thread.ID,
				Agent:     name,
				Resolved:  known,
				Unread:    true,
			})
		}
		if err := store.AddChatMentions(ctx, mentionRows); err != nil {
			return err
		}
		out = chatMessageFromDB(msg)
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "chat send: %v\n", err)
		return 1
	}
	if *jsonOut {
		_ = writeJSON(stdout, out)
		return 0
	}
	writeLine(stdout, "sent #%d as %s", out.Seq, out.AuthorName)
	if len(out.Mentions) > 0 {
		writeLine(stdout, "mentions: %s", strings.Join(out.Mentions, ", "))
	}
	return 0
}

// ---- inbox -----------------------------------------------------------------

func runChatInbox(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat inbox", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	unread := fs.Bool("unread", false, "show only unread mentions")
	jsonOut := fs.Bool("json", false, "print as JSON")
	agent, code := parseChatOnePositional(fs, args, stderr, "chat inbox requires an <agent>")
	if agent == "" {
		return code
	}

	type inboxEntry struct {
		ThreadSlug string `json:"thread_slug"`
		ThreadName string `json:"thread_name"`
		Repo       string `json:"repo"`
		Seq        int64  `json:"seq"`
		AuthorName string `json:"author_name"`
		Kind       string `json:"kind"`
		Body       string `json:"body"`
		Unread     bool   `json:"unread"`
	}
	var out []inboxEntry
	if err := withStore(*home, func(store *db.Store) error {
		entries, err := store.InboxForAgent(context.Background(), agent, *unread)
		if err != nil {
			return err
		}
		for _, e := range entries {
			out = append(out, inboxEntry{
				ThreadSlug: e.ThreadSlug, ThreadName: e.ThreadName, Repo: e.Repo, Seq: e.Seq,
				AuthorName: e.AuthorName, Kind: e.Kind, Body: e.Body, Unread: e.Unread,
			})
		}
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "chat inbox: %v\n", err)
		return 1
	}
	if *jsonOut {
		_ = writeJSON(stdout, out)
		return 0
	}
	if len(out) == 0 {
		writeLine(stdout, "inbox empty")
		return 0
	}
	for _, e := range out {
		marker := " "
		if e.Unread {
			marker = "*"
		}
		writeLine(stdout, "%s %-24s #%d %s: %s", marker, e.ThreadSlug, e.Seq, e.AuthorName, e.Body)
	}
	return 0
}

// ---- close / reopen --------------------------------------------------------

func runChatSetState(args []string, stdout, stderr io.Writer, state string) int {
	label := "chat close"
	if state == db.ChatThreadStateOpen {
		label = "chat reopen"
	}
	fs := flag.NewFlagSet(label, flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	repo := fs.String("repo", "", "repo scope to disambiguate a slug across repos")
	jsonOut := fs.Bool("json", false, "print the updated thread as JSON")
	ref, code := parseChatThreadPositional(fs, args, stderr, label+" requires a <thread>")
	if ref == "" {
		return code
	}

	var out chatThreadOutput
	if err := withStore(*home, func(store *db.Store) error {
		ctx := context.Background()
		thread, err := resolveChatThread(ctx, store, ref, strings.TrimSpace(*repo))
		if err != nil {
			return err
		}
		if err := store.SetChatThreadState(ctx, thread.ID, state); err != nil {
			return err
		}
		updated, err := store.GetChatThreadByID(ctx, thread.ID)
		if err != nil {
			return err
		}
		out = chatThreadToOutput(updated)
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", label, err)
		return 1
	}
	if *jsonOut {
		_ = writeJSON(stdout, out)
		return 0
	}
	writeLine(stdout, "thread: %s", out.Slug)
	writeLine(stdout, "state: %s", out.State)
	return 0
}

// ---- rename ----------------------------------------------------------------

func runChatRename(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("chat rename", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	repo := fs.String("repo", "", "repo scope to disambiguate a slug across repos")
	jsonOut := fs.Bool("json", false, "print the updated thread as JSON")
	// <thread> and "new name" are two positionals before the flags. The rename
	// changes only the human display name; the slug is immutable (it is the
	// topic-path-safe stable handle).
	if len(args) < 2 || args[0] == "-h" || args[0] == "--help" {
		if len(args) >= 1 && (args[0] == "-h" || args[0] == "--help") {
			return 0
		}
		fmt.Fprintln(stderr, "chat rename requires a <thread> and a \"new name\"")
		return 2
	}
	ref := strings.TrimSpace(args[0])
	name := args[1]
	if ref == "" || strings.HasPrefix(ref, "-") {
		fmt.Fprintln(stderr, "chat rename requires a <thread> as the first argument")
		return 2
	}
	if err := fs.Parse(args[2:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "chat rename accepts exactly one <thread> and one \"new name\"")
		return 2
	}
	if strings.TrimSpace(name) == "" {
		fmt.Fprintln(stderr, "chat rename requires a non-empty new name")
		return 2
	}

	var out chatThreadOutput
	if err := withStore(*home, func(store *db.Store) error {
		ctx := context.Background()
		thread, err := resolveChatThread(ctx, store, ref, strings.TrimSpace(*repo))
		if err != nil {
			return err
		}
		if err := store.RenameChatThread(ctx, thread.ID, name); err != nil {
			return err
		}
		updated, err := store.GetChatThreadByID(ctx, thread.ID)
		if err != nil {
			return err
		}
		out = chatThreadToOutput(updated)
		return nil
	}); err != nil {
		fmt.Fprintf(stderr, "chat rename: %v\n", err)
		return 1
	}
	if *jsonOut {
		_ = writeJSON(stdout, out)
		return 0
	}
	writeLine(stdout, "thread: %s", out.Slug)
	writeLine(stdout, "name: %s", out.Name)
	return 0
}

// ---- shared helpers --------------------------------------------------------

// chatRefFlags collects repeatable --ref kind:value flags.
type chatRefFlags []string

func (f *chatRefFlags) String() string { return strings.Join(*f, ",") }
func (f *chatRefFlags) Set(v string) error {
	v = strings.TrimSpace(v)
	if !strings.Contains(v, ":") {
		return fmt.Errorf("--ref must be kind:value, got %q", v)
	}
	*f = append(*f, v)
	return nil
}

func refsToChatRefs(raw chatRefFlags, repo string) []db.ChatRef {
	refs := make([]db.ChatRef, 0, len(raw))
	for _, r := range raw {
		kind, value, _ := strings.Cut(r, ":")
		refs = append(refs, db.ChatRef{
			Kind: strings.TrimSpace(kind),
			Repo: repo,
			ID:   strings.TrimSpace(value),
		})
	}
	return refs
}

func chatMessageFromDB(m db.ChatMessage) chatMessageOutput {
	return chatMessageOutput{
		ID: m.ID, Seq: m.Seq, TsMs: m.TsMs, AuthorKind: m.AuthorKind, AuthorName: m.AuthorName,
		AuthorOrigin: m.AuthorOrigin, Kind: m.Kind, Body: m.Body, Mentions: m.Mentions,
		Refs: m.Refs, ReplyTo: m.ReplyTo, PromotedJobID: m.PromotedJobID,
	}
}

// resolveChatThread turns a user-supplied <thread> (a thread id OR a slug) into a
// thread. An exact id match wins first; otherwise it resolves by slug — scoped to
// --repo when given, else across all repos (unique -> that thread; ambiguous ->
// an error asking for --repo).
func resolveChatThread(ctx context.Context, store *db.Store, ref, repo string) (db.ChatThread, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return db.ChatThread{}, errors.New("a thread id or slug is required")
	}
	if t, err := store.GetChatThreadByID(ctx, ref); err == nil {
		return t, nil
	} else if !errors.Is(err, sql.ErrNoRows) {
		return db.ChatThread{}, err
	}
	if repo != "" {
		t, err := store.GetChatThreadBySlug(ctx, repo, ref)
		if err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return db.ChatThread{}, fmt.Errorf("no chat thread %q in %s", ref, repo)
			}
			return db.ChatThread{}, err
		}
		return t, nil
	}
	threads, err := store.ListChatThreads(ctx, "", "")
	if err != nil {
		return db.ChatThread{}, err
	}
	var matches []db.ChatThread
	for _, t := range threads {
		if t.Slug == ref {
			matches = append(matches, t)
		}
	}
	switch len(matches) {
	case 0:
		return db.ChatThread{}, fmt.Errorf("no chat thread %q", ref)
	case 1:
		return matches[0], nil
	default:
		return db.ChatThread{}, fmt.Errorf("chat thread %q exists in multiple repos; pass --repo to disambiguate", ref)
	}
}

// parseChatFlags parses a flag set that takes no positional arguments.
func parseChatFlags(fs *flag.FlagSet, args []string, stderr io.Writer) error {
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "%s does not accept positional arguments\n", fs.Name())
		return errors.New("unexpected positional")
	}
	return nil
}

// parseChatOnePositional pulls a single required positional off the FRONT of args
// (global flags precede positionals), then parses the remaining flags. It returns
// ("", exitCode) on any error so the caller can `return code`.
func parseChatOnePositional(fs *flag.FlagSet, args []string, stderr io.Writer, missingMsg string) (string, int) {
	if len(args) == 0 {
		fmt.Fprintln(stderr, missingMsg)
		return "", 2
	}
	if args[0] == "-h" || args[0] == "--help" {
		return "", 0
	}
	value := strings.TrimSpace(args[0])
	if value == "" || strings.HasPrefix(value, "-") {
		fmt.Fprintln(stderr, missingMsg)
		return "", 2
	}
	if err := fs.Parse(args[1:]); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return "", 0
		}
		return "", 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintf(stderr, "%s accepts exactly one positional argument\n", fs.Name())
		return "", 2
	}
	return value, 0
}

// parseChatThreadPositional is parseChatOnePositional specialized for the
// <thread> positional (identical mechanics; named for call-site clarity).
func parseChatThreadPositional(fs *flag.FlagSet, args []string, stderr io.Writer, missingMsg string) (string, int) {
	return parseChatOnePositional(fs, args, stderr, missingMsg)
}
