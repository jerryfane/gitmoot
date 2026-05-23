package preset

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/subprocess"
)

const ThermoNuclearCodeQualityReviewID = "thermo-nuclear-code-quality-review"

type Definition struct {
	ID                  string
	Name                string
	Description         string
	DefaultRole         string
	DefaultCapabilities []string
	Mutation            bool
	SourceRepo          string
	SourceRef           string
	SourcePath          string
}

type File struct {
	Content string
}

type Fetcher interface {
	ResolveRef(ctx context.Context, repo string, ref string) (string, error)
	FetchFile(ctx context.Context, repo string, ref string, path string) (File, error)
}

var builtins = []Definition{
	{
		ID:                  ThermoNuclearCodeQualityReviewID,
		Name:                "Thermo-Nuclear Code Quality Review",
		Description:         "Strict review-only preset sourced from Cursor Team Kit.",
		DefaultRole:         "reviewer",
		DefaultCapabilities: []string{"ask", "review"},
		Mutation:            false,
		SourceRepo:          "cursor/plugins",
		SourceRef:           "main",
		SourcePath:          "cursor-team-kit/skills/thermo-nuclear-code-quality-review/SKILL.md",
	},
}

func Builtins() []Definition {
	definitions := make([]Definition, len(builtins))
	copy(definitions, builtins)
	return definitions
}

func Lookup(id string) (Definition, bool) {
	id = strings.TrimSpace(id)
	for _, definition := range builtins {
		if definition.ID == id {
			return definition, true
		}
	}
	return Definition{}, false
}

func Update(ctx context.Context, store *db.Store, fetcher Fetcher, id string) (db.Preset, error) {
	if store == nil {
		return db.Preset{}, errors.New("preset store is required")
	}
	if fetcher == nil {
		return db.Preset{}, errors.New("preset fetcher is required")
	}
	definition, ok := Lookup(id)
	if !ok {
		return db.Preset{}, fmt.Errorf("unknown preset %q", id)
	}
	resolvedCommit, err := fetcher.ResolveRef(ctx, definition.SourceRepo, definition.SourceRef)
	if err != nil {
		return db.Preset{}, err
	}
	file, err := fetcher.FetchFile(ctx, definition.SourceRepo, resolvedCommit, definition.SourcePath)
	if err != nil {
		return db.Preset{}, err
	}
	preset := db.Preset{
		ID:             definition.ID,
		Name:           definition.Name,
		Description:    definition.Description,
		SourceRepo:     definition.SourceRepo,
		SourceRef:      definition.SourceRef,
		SourcePath:     definition.SourcePath,
		ResolvedCommit: resolvedCommit,
		Content:        file.Content,
	}
	if err := store.UpsertPreset(ctx, preset); err != nil {
		return db.Preset{}, err
	}
	return store.GetPreset(ctx, preset.ID)
}

type GHFetcher struct {
	Runner subprocess.Runner
	Dir    string
}

func (f GHFetcher) ResolveRef(ctx context.Context, repo string, ref string) (string, error) {
	repo = strings.TrimSpace(repo)
	ref = strings.TrimPrefix(strings.TrimSpace(ref), "refs/heads/")
	if repo == "" || ref == "" {
		return "", errors.New("repo and ref are required")
	}
	result, err := f.run(ctx, "api", "repos/"+repo+"/git/ref/heads/"+url.PathEscape(ref), "--jq", ".object.sha")
	if err != nil {
		return "", err
	}
	sha := strings.TrimSpace(result.Stdout)
	if sha == "" {
		return "", errors.New("github ref response did not include a commit sha")
	}
	return sha, nil
}

func (f GHFetcher) FetchFile(ctx context.Context, repo string, ref string, path string) (File, error) {
	repo = strings.TrimSpace(repo)
	ref = strings.TrimSpace(ref)
	path = strings.TrimLeft(strings.TrimSpace(path), "/")
	if repo == "" || ref == "" || path == "" {
		return File{}, errors.New("repo, ref, and path are required")
	}
	result, err := f.run(ctx, "api", "-X", "GET", "repos/"+repo+"/contents/"+path, "-f", "ref="+ref)
	if err != nil {
		return File{}, err
	}
	var response struct {
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal([]byte(result.Stdout), &response); err != nil {
		return File{}, fmt.Errorf("decode github contents response: %w", err)
	}
	if response.Encoding != "base64" {
		return File{}, fmt.Errorf("unsupported github contents encoding %q", response.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(stripBase64Whitespace(response.Content))
	if err != nil {
		return File{}, fmt.Errorf("decode github contents: %w", err)
	}
	return File{Content: string(decoded)}, nil
}

func (f GHFetcher) run(ctx context.Context, args ...string) (subprocess.Result, error) {
	runner := f.Runner
	if runner == nil {
		runner = subprocess.ExecRunner{}
	}
	result, err := runner.Run(ctx, f.Dir, "gh", args...)
	if err != nil {
		detail := strings.TrimSpace(result.Stderr)
		if detail == "" {
			detail = strings.TrimSpace(result.Stdout)
		}
		if detail == "" {
			return result, err
		}
		return result, fmt.Errorf("%s: %w", detail, err)
	}
	return result, nil
}

func stripBase64Whitespace(value string) string {
	replacer := strings.NewReplacer("\n", "", "\r", "", "\t", "", " ", "")
	return replacer.Replace(value)
}

func Diff(local string, upstream string) string {
	local = strings.TrimRight(local, "\n")
	upstream = strings.TrimRight(upstream, "\n")
	if local == upstream {
		return "preset content is up to date\n"
	}
	localLines := strings.Split(local, "\n")
	upstreamLines := strings.Split(upstream, "\n")
	prefix := commonPrefix(localLines, upstreamLines)
	suffix := commonSuffix(localLines[prefix:], upstreamLines[prefix:])
	var builder strings.Builder
	builder.WriteString("--- cached\n")
	builder.WriteString("+++ upstream\n")
	for _, line := range localLines[prefix : len(localLines)-suffix] {
		builder.WriteString("-")
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	for _, line := range upstreamLines[prefix : len(upstreamLines)-suffix] {
		builder.WriteString("+")
		builder.WriteString(line)
		builder.WriteByte('\n')
	}
	return builder.String()
}

func commonPrefix(left []string, right []string) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for index := 0; index < limit; index++ {
		if left[index] != right[index] {
			return index
		}
	}
	return limit
}

func commonSuffix(left []string, right []string) int {
	limit := len(left)
	if len(right) < limit {
		limit = len(right)
	}
	for index := 0; index < limit; index++ {
		if left[len(left)-1-index] != right[len(right)-1-index] {
			return index
		}
	}
	return limit
}
