package cli

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
	"github.com/jerryfane/gitmoot/internal/preset"
)

var newPresetFetcher = func() preset.Fetcher {
	return preset.GHFetcher{}
}

func runPreset(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printPresetUsage(stdout)
		return 0
	}
	switch args[0] {
	case "list":
		return runPresetList(args[1:], stdout, stderr)
	case "show":
		return runPresetShow(args[1:], stdout, stderr)
	case "update":
		return runPresetUpdate(args[1:], stdout, stderr)
	case "diff":
		return runPresetDiff(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown preset command %q\n\n", args[0])
		printPresetUsage(stderr)
		return 2
	}
}

func printPresetUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  gitmoot preset list")
	fmt.Fprintln(w, "  gitmoot preset show thermo-nuclear-code-quality-review")
	fmt.Fprintln(w, "  gitmoot preset update thermo-nuclear-code-quality-review")
	fmt.Fprintln(w, "  gitmoot preset diff thermo-nuclear-code-quality-review")
}

func runPresetList(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preset list", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "preset list does not accept positional arguments")
		return 2
	}
	return withStoreExit(*home, stderr, "list presets", func(store *db.Store) error {
		installed, err := installedPresets(context.Background(), store)
		if err != nil {
			return err
		}
		for _, definition := range preset.Builtins() {
			status := "available"
			if installedPreset, ok := installed[definition.ID]; ok {
				status = "installed@" + shortCommit(installedPreset.ResolvedCommit)
			}
			fmt.Fprintf(stdout, "%-36s %-18s %s\n", definition.ID, status, definition.SourceRepo+"/"+definition.SourcePath)
		}
		return nil
	})
}

func runPresetShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preset show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "preset show requires exactly one preset id")
		return 2
	}
	definition, ok := preset.Lookup(fs.Arg(0))
	if !ok {
		fmt.Fprintf(stderr, "unknown preset %q\n", fs.Arg(0))
		return 2
	}
	return withStoreExit(*home, stderr, "show preset", func(store *db.Store) error {
		cached, err := store.GetPreset(context.Background(), definition.ID)
		installed := true
		if errors.Is(err, sql.ErrNoRows) {
			installed = false
		} else if err != nil {
			return err
		}
		writePresetDefinition(stdout, definition)
		if !installed {
			fmt.Fprintln(stdout, "installed: no")
			return nil
		}
		fmt.Fprintln(stdout, "installed: yes")
		fmt.Fprintf(stdout, "resolved commit: %s\n", cached.ResolvedCommit)
		fmt.Fprintf(stdout, "updated: %s\n", cached.UpdatedAt)
		fmt.Fprintln(stdout, "content:")
		fmt.Fprintln(stdout, strings.TrimRight(cached.Content, "\n"))
		return nil
	})
}

func runPresetUpdate(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preset update", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "preset update requires exactly one preset id")
		return 2
	}
	id := fs.Arg(0)
	if _, ok := preset.Lookup(id); !ok {
		fmt.Fprintf(stderr, "unknown preset %q\n", id)
		return 2
	}
	return withStoreExit(*home, stderr, "update preset", func(store *db.Store) error {
		updated, err := preset.Update(context.Background(), store, newPresetFetcher(), id)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "updated %s at %s\n", updated.ID, updated.ResolvedCommit)
		return nil
	})
}

func runPresetDiff(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preset diff", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 1 {
		fmt.Fprintln(stderr, "preset diff requires exactly one preset id")
		return 2
	}
	definition, ok := preset.Lookup(fs.Arg(0))
	if !ok {
		fmt.Fprintf(stderr, "unknown preset %q\n", fs.Arg(0))
		return 2
	}
	return withStoreExit(*home, stderr, "diff preset", func(store *db.Store) error {
		cached, err := store.GetPreset(context.Background(), definition.ID)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("preset %s is not installed; run gitmoot preset update %s", definition.ID, definition.ID)
		}
		if err != nil {
			return err
		}
		fetcher := newPresetFetcher()
		resolvedCommit, err := fetcher.ResolveRef(context.Background(), definition.SourceRepo, definition.SourceRef)
		if err != nil {
			return err
		}
		upstream, err := fetcher.FetchFile(context.Background(), definition.SourceRepo, resolvedCommit, definition.SourcePath)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "cached:   %s\n", cached.ResolvedCommit)
		fmt.Fprintf(stdout, "upstream: %s\n", resolvedCommit)
		fmt.Fprint(stdout, preset.Diff(cached.Content, upstream.Content))
		return nil
	})
}

func installedPresets(ctx context.Context, store *db.Store) (map[string]db.Preset, error) {
	presets, err := store.ListPresets(ctx)
	if err != nil {
		return nil, err
	}
	installed := make(map[string]db.Preset, len(presets))
	for _, cached := range presets {
		installed[cached.ID] = cached
	}
	return installed, nil
}

func writePresetDefinition(w io.Writer, definition preset.Definition) {
	fmt.Fprintf(w, "id: %s\n", definition.ID)
	fmt.Fprintf(w, "name: %s\n", definition.Name)
	fmt.Fprintf(w, "description: %s\n", definition.Description)
	fmt.Fprintf(w, "source: %s@%s:%s\n", definition.SourceRepo, definition.SourceRef, definition.SourcePath)
	fmt.Fprintf(w, "default role: %s\n", definition.DefaultRole)
	fmt.Fprintf(w, "default capabilities: %s\n", strings.Join(definition.DefaultCapabilities, ","))
	fmt.Fprintf(w, "mutation: %t\n", definition.Mutation)
}

func shortCommit(commit string) string {
	commit = strings.TrimSpace(commit)
	if len(commit) <= 12 {
		return commit
	}
	return commit[:12]
}

func withStoreExit(home string, stderr io.Writer, label string, fn func(*db.Store) error) int {
	if err := withStore(home, fn); err != nil {
		fmt.Fprintf(stderr, "%s: %v\n", label, err)
		return 1
	}
	return 0
}
