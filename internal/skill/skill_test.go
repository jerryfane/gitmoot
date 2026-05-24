package skill

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCanonicalSkillFrontmatter(t *testing.T) {
	text := readRepoFile(t, "skills", "gitmoot", "SKILL.md")
	frontmatter := parseFrontmatter(t, text)

	for _, want := range []string{
		"name: gitmoot",
		"description: Use Gitmoot",
		"license: MIT",
		"compatibility:",
		"metadata:",
		"gitmoot-version:",
	} {
		if !strings.Contains(frontmatter, want) {
			t.Fatalf("frontmatter missing %q:\n%s", want, frontmatter)
		}
	}

	for _, want := range []string{
		"GitHub PR comments",
		"agent subscriptions",
		"daemon checks",
		"jobs",
		"branch locks",
		"presets",
		"custom prompt agents",
		"Codex",
		"Claude Code",
	} {
		if !strings.Contains(frontmatter, want) {
			t.Fatalf("description missing trigger term %q:\n%s", want, frontmatter)
		}
	}
}

func TestCanonicalSkillReferencesExist(t *testing.T) {
	text := readRepoFile(t, "skills", "gitmoot", "SKILL.md")
	for _, ref := range []string{
		"references/CLI.md",
		"references/WORKFLOWS.md",
		"references/RESULT_CONTRACT.md",
		"references/SAFETY.md",
	} {
		if !strings.Contains(text, ref) {
			t.Fatalf("canonical SKILL.md missing reference %q", ref)
		}
		path := filepath.Join(append([]string{"..", "..", "skills", "gitmoot"}, strings.Split(ref, "/")...)...)
		if _, err := os.Stat(path); err != nil {
			t.Fatalf("referenced file %s missing: %v", ref, err)
		}
	}
}

func TestCanonicalSkillDocumentsResultAndRereadGuidance(t *testing.T) {
	text := readRepoFile(t, "skills", "gitmoot", "SKILL.md")
	for _, want := range []string{
		"gitmoot_result",
		"blocked",
		"failed",
		"branch locks",
		"Reread this `SKILL.md`",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("canonical SKILL.md missing %q", want)
		}
	}
}

func TestRootSkillCompatibilityEntrypoint(t *testing.T) {
	text := readRepoFile(t, "SKILL.md")
	frontmatter := parseFrontmatter(t, text)
	for _, want := range []string{
		"name: gitmoot",
		"description: Use Gitmoot",
	} {
		if !strings.Contains(frontmatter, want) {
			t.Fatalf("root SKILL.md frontmatter missing %q:\n%s", want, frontmatter)
		}
	}
	for _, want := range []string{
		"skills/gitmoot/",
		"gitmoot.io/SKILL.md",
		"gitmoot_result",
		"branch locks",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("root SKILL.md missing compatibility content %q", want)
		}
	}
}

func readRepoFile(t *testing.T, parts ...string) string {
	t.Helper()
	path := filepath.Join(append([]string{"..", ".."}, parts...)...)
	contents, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", filepath.Join(parts...), err)
	}
	return string(contents)
}

func parseFrontmatter(t *testing.T, text string) string {
	t.Helper()
	if !strings.HasPrefix(text, "---\n") {
		t.Fatal("SKILL.md missing YAML frontmatter opener")
	}
	parts := strings.SplitN(text, "---\n", 3)
	if len(parts) != 3 {
		t.Fatal("SKILL.md missing YAML frontmatter closer")
	}
	return parts[1]
}
