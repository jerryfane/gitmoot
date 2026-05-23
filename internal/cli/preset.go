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
	case "add":
		return runPresetAdd(args[1:], stdout, stderr)
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
	fmt.Fprintln(w, "  gitmoot preset add <preset-id> --file ./agents/<preset-id>.md [--name <name>] [--description <text>]")
	fmt.Fprintln(w, "  gitmoot preset list")
	fmt.Fprintln(w, "  gitmoot preset show thermo-nuclear-code-quality-review")
	fmt.Fprintln(w, "  gitmoot preset update thermo-nuclear-code-quality-review")
	fmt.Fprintln(w, "  gitmoot preset diff thermo-nuclear-code-quality-review")
}

func runPresetAdd(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("preset add", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	file := fs.String("file", "", "local prompt file to install")
	name := fs.String("name", "", "preset display name")
	description := fs.String("description", "", "preset description")
	id, flagArgs := leadingID(args)
	if err := fs.Parse(flagArgs); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if id == "" {
		if fs.NArg() == 1 {
			id = fs.Arg(0)
		} else {
			fmt.Fprintln(stderr, "preset add requires exactly one preset id")
			return 2
		}
	} else if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "preset add requires exactly one preset id")
		return 2
	}
	if strings.TrimSpace(*file) == "" {
		fmt.Fprintln(stderr, "preset add requires --file")
		return 2
	}
	return withStoreExit(*home, stderr, "add preset", func(store *db.Store) error {
		added, err := preset.AddLocal(context.Background(), store, id, *file, *name, *description)
		if err != nil {
			return err
		}
		fmt.Fprintf(stdout, "added %s at %s\n", added.ID, added.ResolvedCommit)
		return nil
	})
}

func leadingID(args []string) (string, []string) {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return "", args
	}
	return args[0], args[1:]
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
		cachedPresets, err := store.ListPresets(context.Background())
		if err != nil {
			return err
		}
		installed := installedPresetMap(cachedPresets)
		for _, definition := range preset.Builtins() {
			status := "available"
			if installedPreset, ok := installed[definition.ID]; ok {
				status = "installed@" + shortCommit(installedPreset.ResolvedCommit)
			}
			fmt.Fprintf(stdout, "%-36s %-18s %s\n", definition.ID, status, definition.SourceRepo+"/"+definition.SourcePath)
		}
		for _, cached := range cachedPresets {
			if _, ok := preset.Lookup(cached.ID); ok {
				continue
			}
			status := "installed@" + shortCommit(cached.ResolvedCommit)
			fmt.Fprintf(stdout, "%-36s %-18s %s:%s\n", cached.ID, status, cached.SourceRepo, cached.SourcePath)
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
	id := fs.Arg(0)
	return withStoreExit(*home, stderr, "show preset", func(store *db.Store) error {
		if definition, ok := preset.Lookup(id); ok {
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
			writeInstalledPreset(stdout, cached)
			return nil
		}
		cached, err := store.GetPreset(context.Background(), id)
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("unknown preset %q", id)
		}
		if err != nil {
			return err
		}
		writeCustomPreset(stdout, cached)
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
	return withStoreExit(*home, stderr, "update preset", func(store *db.Store) error {
		updated, err := updatePresetByID(context.Background(), store, id)
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
	id := fs.Arg(0)
	return withStoreExit(*home, stderr, "diff preset", func(store *db.Store) error {
		cached, err := store.GetPreset(context.Background(), id)
		if errors.Is(err, sql.ErrNoRows) {
			if _, ok := preset.Lookup(id); ok {
				return fmt.Errorf("preset %s is not installed; run gitmoot preset update %s", id, id)
			}
			return fmt.Errorf("unknown preset %q", id)
		}
		if err != nil {
			return err
		}
		if preset.IsLocal(cached) {
			file, hash, err := preset.ReadLocalForDiff(cached.SourcePath)
			if err != nil {
				return err
			}
			fmt.Fprintf(stdout, "cached:   %s\n", cached.ResolvedCommit)
			fmt.Fprintf(stdout, "upstream: %s\n", hash)
			fmt.Fprint(stdout, preset.DiffExact(cached.Content, file.Content))
			return nil
		}
		definition, ok := preset.Lookup(id)
		if !ok {
			return fmt.Errorf("preset %s is not a local custom preset and has no built-in source", id)
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

func updatePresetByID(ctx context.Context, store *db.Store, id string) (db.Preset, error) {
	if _, ok := preset.Lookup(id); ok {
		return preset.Update(ctx, store, newPresetFetcher(), id)
	}
	cached, err := store.GetPreset(ctx, id)
	if errors.Is(err, sql.ErrNoRows) {
		return db.Preset{}, fmt.Errorf("unknown preset %q; run gitmoot preset add %s --file <path>", id, id)
	}
	if err != nil {
		return db.Preset{}, err
	}
	if !preset.IsLocal(cached) {
		return db.Preset{}, fmt.Errorf("preset %s is not a local custom preset and has no built-in source", id)
	}
	return preset.UpdateLocal(ctx, store, cached)
}

func installedPresetMap(presets []db.Preset) map[string]db.Preset {
	installed := make(map[string]db.Preset, len(presets))
	for _, cached := range presets {
		installed[cached.ID] = cached
	}
	return installed
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

func writeCustomPreset(w io.Writer, cached db.Preset) {
	fmt.Fprintf(w, "id: %s\n", cached.ID)
	fmt.Fprintf(w, "name: %s\n", cached.Name)
	fmt.Fprintf(w, "description: %s\n", cached.Description)
	fmt.Fprintf(w, "source: %s@%s:%s\n", cached.SourceRepo, cached.SourceRef, cached.SourcePath)
	fmt.Fprintln(w, "default role: ")
	fmt.Fprintln(w, "default capabilities: ")
	fmt.Fprintln(w, "mutation: false")
	writeInstalledPreset(w, cached)
}

func writeInstalledPreset(w io.Writer, cached db.Preset) {
	fmt.Fprintln(w, "installed: yes")
	fmt.Fprintf(w, "resolved commit: %s\n", cached.ResolvedCommit)
	fmt.Fprintf(w, "updated: %s\n", cached.UpdatedAt)
	fmt.Fprintln(w, "content:")
	fmt.Fprintln(w, strings.TrimRight(cached.Content, "\n"))
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
