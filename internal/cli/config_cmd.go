package cli

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/jerryfane/gitmoot/internal/config"
)

func runConfig(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" {
		printConfigUsage(stdout)
		return 0
	}
	switch args[0] {
	case "path":
		return runConfigPath(args[1:], stdout, stderr)
	case "show":
		return runConfigShow(args[1:], stdout, stderr)
	default:
		fmt.Fprintf(stderr, "unknown config command %q\n\n", args[0])
		printConfigUsage(stderr)
		return 2
	}
}

func printConfigUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  gitmoot config path")
	fmt.Fprintln(w, "  gitmoot config show")
}

func runConfigPath(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config path", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "config path does not accept positional arguments")
		return 2
	}
	paths, err := pathsFromFlag(*home)
	if err != nil {
		fmt.Fprintf(stderr, "config path: %v\n", err)
		return 1
	}
	writeLine(stdout, "%s", paths.ConfigFile)
	return 0
}

func runConfigShow(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("config show", flag.ContinueOnError)
	fs.SetOutput(stderr)
	home := fs.String("home", "", "home directory to use instead of the current user's home")
	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return 0
		}
		return 2
	}
	if fs.NArg() != 0 {
		fmt.Fprintln(stderr, "config show does not accept positional arguments")
		return 2
	}
	paths, err := pathsFromFlag(*home)
	if err != nil {
		fmt.Fprintf(stderr, "config show: %v\n", err)
		return 1
	}
	if err := config.Initialize(paths); err != nil {
		fmt.Fprintf(stderr, "config show: %v\n", err)
		return 1
	}
	content, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		fmt.Fprintf(stderr, "config show: %v\n", err)
		return 1
	}
	_, _ = stdout.Write(content)
	return 0
}
