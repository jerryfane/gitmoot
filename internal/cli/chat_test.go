package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/runtime"
)

func seedChatAgent(t *testing.T, store *db.Store, name string) {
	t.Helper()
	if err := store.UpsertAgent(context.Background(), db.Agent{
		Name:         name,
		Role:         "agent",
		Runtime:      runtime.ShellRuntime,
		RuntimeRef:   "printf ok",
		RepoScope:    "owner/repo",
		Capabilities: []string{"ask"},
		HealthStatus: "ok",
	}); err != nil {
		t.Fatalf("UpsertAgent returned error: %v", err)
	}
}

func TestChatCreateValidatesSlug(t *testing.T) {
	home := t.TempDir()
	store := openCLIJobStore(t, home)
	defer store.Close()

	// A name that slugifies to nothing valid is rejected.
	var stderr bytes.Buffer
	if code := Run([]string{"chat", "create", "***", "--repo", "owner/repo", "--home", home}, &bytes.Buffer{}, &stderr); code != 2 {
		t.Fatalf("chat create with an invalid slug exit = %d, want 2 (stderr=%s)", code, stderr.String())
	}
	// Missing --repo is rejected.
	if code := Run([]string{"chat", "create", "room", "--home", home}, &bytes.Buffer{}, &bytes.Buffer{}); code != 2 {
		t.Fatalf("chat create without --repo should exit 2")
	}
}

func TestChatCreateAndList(t *testing.T) {
	home := t.TempDir()
	store := openCLIJobStore(t, home)
	defer store.Close()

	var stdout, stderr bytes.Buffer
	if code := Run([]string{"chat", "create", "release-room", "--repo", "owner/repo", "--topic", "Release coordination", "--json", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat create exit = %d, stderr=%s", code, stderr.String())
	}
	var created chatThreadOutput
	if err := json.Unmarshal(stdout.Bytes(), &created); err != nil {
		t.Fatalf("decode create JSON: %v (%s)", err, stdout.String())
	}
	if created.Slug != "release-room" || created.Name != "Release coordination" || created.State != "open" {
		t.Fatalf("created = %+v", created)
	}
	if created.Origin == "" || created.Origin == "self" {
		t.Fatalf("thread origin = %q, want a generated home_id (not the literal self)", created.Origin)
	}

	stdout.Reset()
	if code := Run([]string{"chat", "list", "--repo", "owner/repo", "--json", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat list exit = %d, stderr=%s", code, stderr.String())
	}
	var list []chatThreadOutput
	if err := json.Unmarshal(stdout.Bytes(), &list); err != nil {
		t.Fatalf("decode list JSON: %v", err)
	}
	if len(list) != 1 || list[0].Slug != "release-room" {
		t.Fatalf("list = %+v, want the one thread", list)
	}
}

func TestChatSendMentionsAndInbox(t *testing.T) {
	home := t.TempDir()
	store := openCLIJobStore(t, home)
	defer store.Close()
	seedChatAgent(t, store, "codex-b")

	if code := Run([]string{"chat", "create", "room", "--repo", "owner/repo", "--home", home}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("chat create failed")
	}

	// Send a message mentioning a known agent (codex-b) and an unknown one (ghost).
	var stdout, stderr bytes.Buffer
	if code := Run([]string{"chat", "send", "room", "@codex-b look at this, @ghost too", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat send exit = %d, stderr=%s", code, stderr.String())
	}
	if !strings.Contains(stderr.String(), "ghost") {
		t.Fatalf("expected a stderr warning about the unknown @ghost mention, got: %s", stderr.String())
	}

	// The known agent has an unread inbox entry; the send did NOT fail on the
	// unknown mention.
	stdout.Reset()
	if code := Run([]string{"chat", "inbox", "codex-b", "--unread", "--json", "--home", home}, &stdout, &stderr); code != 0 {
		t.Fatalf("chat inbox exit = %d, stderr=%s", code, stderr.String())
	}
	var inbox []struct {
		ThreadSlug string `json:"thread_slug"`
		Body       string `json:"body"`
		Unread     bool   `json:"unread"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &inbox); err != nil {
		t.Fatalf("decode inbox JSON: %v (%s)", err, stdout.String())
	}
	if len(inbox) != 1 || inbox[0].ThreadSlug != "room" || !inbox[0].Unread {
		t.Fatalf("inbox = %+v, want one unread entry", inbox)
	}
	// The unknown agent has no inbox entry.
	stdout.Reset()
	if code := Run([]string{"chat", "inbox", "ghost", "--json", "--home", home}, &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatal("chat inbox ghost failed")
	}
	if s := strings.TrimSpace(stdout.String()); s != "null" && s != "[]" {
		t.Fatalf("unknown agent inbox = %q, want empty", s)
	}
}

func TestChatSendRejectsArchived(t *testing.T) {
	home := t.TempDir()
	store := openCLIJobStore(t, home)
	defer store.Close()

	if code := Run([]string{"chat", "create", "room", "--repo", "owner/repo", "--home", home}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("chat create failed")
	}
	if code := Run([]string{"chat", "close", "room", "--home", home}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("chat close failed")
	}
	var stderr bytes.Buffer
	if code := Run([]string{"chat", "send", "room", "hello", "--home", home}, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("chat send to archived exit = %d, want 1", code)
	}
	if !strings.Contains(stderr.String(), "archived") {
		t.Fatalf("expected an 'archived' error, got: %s", stderr.String())
	}
	// Reopen restores sending.
	if code := Run([]string{"chat", "reopen", "room", "--home", home}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("chat reopen failed")
	}
	if code := Run([]string{"chat", "send", "room", "hello again", "--home", home}, &bytes.Buffer{}, &stderr); code != 0 {
		t.Fatalf("chat send after reopen exit = %d, stderr=%s", code, stderr.String())
	}
}

func TestChatSendAsUnknownAgentFails(t *testing.T) {
	home := t.TempDir()
	store := openCLIJobStore(t, home)
	defer store.Close()
	if code := Run([]string{"chat", "create", "room", "--repo", "owner/repo", "--home", home}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("chat create failed")
	}
	var stderr bytes.Buffer
	if code := Run([]string{"chat", "send", "room", "hi", "--as", "nobody", "--home", home}, &bytes.Buffer{}, &stderr); code != 1 {
		t.Fatalf("chat send --as unknown exit = %d, want 1 (stderr=%s)", code, stderr.String())
	}
}

func TestChatRenameKeepsSlug(t *testing.T) {
	home := t.TempDir()
	store := openCLIJobStore(t, home)
	defer store.Close()
	if code := Run([]string{"chat", "create", "room", "--repo", "owner/repo", "--home", home}, &bytes.Buffer{}, &bytes.Buffer{}); code != 0 {
		t.Fatal("chat create failed")
	}
	var stdout bytes.Buffer
	if code := Run([]string{"chat", "rename", "room", "New Name", "--json", "--home", home}, &stdout, &bytes.Buffer{}); code != 0 {
		t.Fatalf("chat rename failed: %s", stdout.String())
	}
	var out chatThreadOutput
	if err := json.Unmarshal(stdout.Bytes(), &out); err != nil {
		t.Fatalf("decode rename JSON: %v", err)
	}
	if out.Name != "New Name" || out.Slug != "room" {
		t.Fatalf("rename = %+v, want name updated and slug immutable", out)
	}
}
