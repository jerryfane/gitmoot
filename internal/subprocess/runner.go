package subprocess

import (
	"bytes"
	"context"
	"os/exec"
)

type Result struct {
	Command string
	Args    []string
	Stdout  string
	Stderr  string
}

func Run(ctx context.Context, dir string, command string, args ...string) (Result, error) {
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Dir = dir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	return Result{
		Command: command,
		Args:    args,
		Stdout:  stdout.String(),
		Stderr:  stderr.String(),
	}, err
}
