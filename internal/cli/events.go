package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"strings"

	"github.com/jerryfane/gitmoot/internal/db"
)

func runEvents(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("events", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	repoFlag := fs.String("repo", "", "repo scope as owner/repo")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "events does not accept positional arguments")
		return 2
	}
	repoFullName, ok := normalizeOptionalRepoFlag(*repoFlag, stderr)
	if !ok {
		return 2
	}
	if strings.TrimSpace(repoFullName) == "" {
		fmt.Fprintln(stderr, "events requires --repo")
		return 2
	}
	if err := withStore(*home, func(store *db.Store) error {
		return printRepoEvents(context.Background(), store, repoFullName, stdout)
	}); err != nil {
		fmt.Fprintf(stderr, "events: %v\n", err)
		return 1
	}
	return 0
}

func printRepoEvents(ctx context.Context, store *db.Store, repoFullName string, stdout io.Writer) error {
	jobs, err := store.ListJobs(ctx)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		payload, err := daemonJobPayload(job)
		if err != nil || payload.Repo != repoFullName {
			continue
		}
		events, err := store.ListJobEvents(ctx, job.ID)
		if err != nil {
			return err
		}
		for _, event := range events {
			fmt.Fprintf(stdout, "job\t%s\t%s\t%s\t%s\n", job.ID, job.State, event.Kind, event.Message)
		}
	}
	lockEvents, err := store.ListBranchLockEvents(ctx, repoFullName, "")
	if err != nil {
		return err
	}
	for _, event := range lockEvents {
		fmt.Fprintf(stdout, "lock\t%s\t%s\t%s\t%s\n", event.Branch, event.Owner, event.Kind, event.Message)
	}
	return nil
}
