package cli

import (
	"fmt"
	"io"
	"strings"
)

type command struct {
	name        string
	summary     string
	description string
	run         func(args []string, stdout, stderr io.Writer) int
}

var rootCommands = []command{
	{name: "init", summary: "initialize local Gitmoot state", run: notImplemented("init")},
	{name: "doctor", summary: "check local Gitmoot prerequisites", run: notImplemented("doctor")},
	{name: "daemon", summary: "run the local PR watcher", run: notImplemented("daemon")},
	{name: "agent", summary: "manage registered agents", run: notImplemented("agent")},
	{name: "status", summary: "show local workflow status", run: notImplemented("status")},
	{name: "goal", summary: "import or inspect goals", run: notImplemented("goal")},
	{name: "task", summary: "run or inspect tasks", run: notImplemented("task")},
}

func Run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 || args[0] == "-h" || args[0] == "--help" || args[0] == "help" {
		printUsage(stdout)
		return 0
	}

	name := args[0]
	for _, cmd := range rootCommands {
		if cmd.name == name {
			return cmd.run(args[1:], stdout, stderr)
		}
	}

	fmt.Fprintf(stderr, "unknown command %q\n\n", name)
	printUsage(stderr)
	return 2
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "gitmoot coordinates local AI agent sessions through GitHub PRs.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  gitmoot <command> [flags]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Commands:")
	for _, cmd := range rootCommands {
		fmt.Fprintf(w, "  %-8s %s\n", cmd.name, cmd.summary)
	}
}

func notImplemented(name string) func(args []string, stdout, stderr io.Writer) int {
	return func(args []string, stdout, stderr io.Writer) int {
		if len(args) > 0 && (args[0] == "-h" || args[0] == "--help") {
			fmt.Fprintf(stdout, "Usage:\n  gitmoot %s [flags]\n", name)
			return 0
		}
		fmt.Fprintf(stderr, "gitmoot %s is not implemented yet\n", name)
		if len(args) > 0 {
			fmt.Fprintf(stderr, "received args: %s\n", strings.Join(args, " "))
		}
		return 1
	}
}
