package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"

	"github.com/jerryfane/gitmoot/internal/buildinfo"
)

type versionOutput struct {
	buildinfo.Info
	Config   string `json:"config"`
	Database string `json:"database"`
}

func runVersion(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("version", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	jsonOutput := fs.Bool("json", false, "print version information as JSON")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "version does not accept positional arguments")
		return 2
	}

	paths, err := pathsFromFlag(*home)
	if err != nil {
		fmt.Fprintf(stderr, "resolve paths: %v\n", err)
		return 1
	}
	output := versionOutput{
		Info:     buildinfo.Current(),
		Config:   paths.ConfigFile,
		Database: paths.Database,
	}
	if *jsonOutput {
		if err := writeJSON(stdout, output); err != nil {
			fmt.Fprintf(stderr, "write version json: %v\n", err)
			return 1
		}
		return 0
	}

	writeLine(stdout, "gitmoot %s", output.Version)
	writeLine(stdout, "commit: %s", output.Commit)
	writeLine(stdout, "built: %s", output.Date)
	writeLine(stdout, "go: %s", output.Go)
	writeLine(stdout, "config: %s", output.Config)
	writeLine(stdout, "database: %s", output.Database)
	return 0
}
