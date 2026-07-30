// Package ws adapts the `workshop` command-line tool: a single exec helper
// (design §5.5), tolerant parsers for `info` and `list` (§5.4) and the small set
// of operations the state machine needs (§5.3).
package ws

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strings"
	"time"
)

// Exec is the single gateway for every child process (design §5.5).
type Exec struct {
	// Timeout applies to each individual command.
	Timeout time.Duration
	// Verbose echoes every argv and its output.
	Verbose bool
	// DryRun records mutating commands instead of running them. Read-only
	// commands are still executed so that the plan can be computed.
	DryRun bool
	// Log receives the echo of commands and streamed stderr.
	Log io.Writer
	// Env is appended to the child environment, after the forced values, so a
	// caller can override them.
	Env []string
}

const defaultTimeout = 15 * time.Minute

// childEnv forces machine-readable output: no colour, no unicode, no
// hyperlinks (§2.3, D6). Piping stdout already disables the decorations; these
// are belt and braces.
var childEnv = []string{"NO_COLOR=1", "LC_ALL=C", "TERM=dumb"}

func (e *Exec) timeout() time.Duration {
	if e.Timeout > 0 {
		return e.Timeout
	}
	return defaultTimeout
}

func (e *Exec) logf(format string, args ...any) {
	if e.Log == nil {
		return
	}
	fmt.Fprintf(e.Log, format, args...)
}

// Quote renders an argv for human consumption.
func Quote(name string, args ...string) string {
	parts := make([]string, 0, len(args)+1)
	parts = append(parts, name)
	for _, a := range args {
		if a == "" || strings.ContainsAny(a, " \t\"'$") {
			parts = append(parts, fmt.Sprintf("%q", a))
		} else {
			parts = append(parts, a)
		}
	}
	return strings.Join(parts, " ")
}

// Output runs a read-only command in dir and returns its stdout. Stderr is
// captured and folded into the error on failure (C7).
func (e *Exec) Output(dir, name string, args ...string) (string, error) {
	argv := Quote(name, args...)
	if e.Verbose {
		e.logf("+ %s\n", argv)
	}

	ctx, cancel := context.WithTimeout(context.Background(), e.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(inheritEnv(), childEnv...), e.Env...)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()
	if e.Verbose {
		if s := strings.TrimRight(stdout.String(), "\n"); s != "" {
			e.logf("%s\n", s)
		}
		if s := strings.TrimRight(stderr.String(), "\n"); s != "" {
			e.logf("%s\n", s)
		}
	}
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return stdout.String(), fmt.Errorf("timed out after %s: %s", e.timeout(), argv)
		}
		return stdout.String(), fmt.Errorf("%s: %w\n%s", argv, err, strings.TrimRight(stderr.String(), "\n"))
	}
	return stdout.String(), nil
}

// Run executes a mutating command in dir, streaming stderr so that slow
// container operations stay visible (C8). Under DryRun it only records the argv.
func (e *Exec) Run(dir, name string, args ...string) error {
	argv := Quote(name, args...)
	if e.DryRun {
		e.logf("would run: %s\n", argv)
		return nil
	}
	e.logf("+ %s\n", argv)

	ctx, cancel := context.WithTimeout(context.Background(), e.timeout())
	defer cancel()

	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Dir = dir
	cmd.Env = append(append(inheritEnv(), childEnv...), e.Env...)
	var stderr bytes.Buffer
	if e.Log != nil {
		cmd.Stdout = e.Log
		cmd.Stderr = io.MultiWriter(e.Log, &stderr)
	} else {
		cmd.Stderr = &stderr
	}

	if err := cmd.Run(); err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("timed out after %s: %s", e.timeout(), argv)
		}
		return fmt.Errorf("%s: %w\n%s", argv, err, strings.TrimRight(stderr.String(), "\n"))
	}
	return nil
}
