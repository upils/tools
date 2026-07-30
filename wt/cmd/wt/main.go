// Command wt bootstraps a git worktree together with its workshop dev
// container, wiring the shared .git directory into the container so that git
// works inside it.
//
// See worktree-workshop.md for the design this implements.
package main

import (
	"errors"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/upils/tools/wt/internal/plan"
)

// Exit codes (design §5.1).
const (
	exitOK      = 0
	exitError   = 1
	exitUsage   = 2
	exitRefused = 3
)

const usage = `wt — worktree + workshop bootstrapper

Usage:
  wt up [<branch>] [flags]

Arguments:
  <branch>   Branch/worktree name. Optional when the current directory is
             already inside a linked worktree.

Flags:
  -C, --repo <dir>       Repository to derive the worktree layout from (default: CWD).
      --worktree <dir>   Explicit worktree path; overrides layout derivation.
      --from <rev>       Start point for a new branch (default: repo HEAD).
      --workshop <name>  Workshop name (default: the single workshop defined).
      --definition <p>   Worktree-relative path of the workshop definition to edit
                         (default: the only one found, else <project>-dev under
                         .workshop/). Looked up at workshop.yaml,
                         .workshop.yaml and .workshop/<name>.yaml.
      --sdk <name>       SDK owning the plug (default: vscode-remote).
      --plug <name>      Plug name (default: git-dir).
      --code             Launch VS Code on success.
      --dry-run          Print the plan; change nothing.
      --verbose          Echo every command and its output.
      --force            Ignore a stale lock.
      --timeout <dur>    Per-workshop-command timeout (default: 15m).
`

type options struct {
	repo       string
	worktree   string
	from       string
	workshop   string
	definition string
	sdk        string
	plug       string
	code       bool
	dryRun     bool
	verbose    bool
	force      bool
	timeout    time.Duration
	branch     string
}

func main() {
	os.Exit(run(os.Args[1:]))
}

func run(argv []string) int {
	if len(argv) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return exitUsage
	}
	switch argv[0] {
	case "up":
		argv = argv[1:]
	case "-h", "--help", "help":
		fmt.Print(usage)
		return exitOK
	default:
		fmt.Fprintf(os.Stderr, "unknown command %q\n\n%s", argv[0], usage)
		return exitUsage
	}

	opts, err := parseFlags(argv)
	if err != nil {
		fmt.Fprintf(os.Stderr, "wt: %v\n", err)
		return exitUsage
	}

	if err := up(opts); err != nil {
		var refused *plan.ErrRefused
		if errors.As(err, &refused) {
			fmt.Fprintf(os.Stderr, "wt: %v\n", err)
			return exitRefused
		}
		fmt.Fprintf(os.Stderr, "wt: %v\n", err)
		return exitError
	}
	return exitOK
}

func parseFlags(argv []string) (*options, error) {
	var o options
	fs := flag.NewFlagSet("wt up", flag.ContinueOnError)
	fs.SetOutput(os.Stderr)
	fs.Usage = func() { fmt.Fprint(os.Stderr, usage) }

	fs.StringVar(&o.repo, "repo", "", "repository directory")
	fs.StringVar(&o.repo, "C", "", "repository directory (shorthand)")
	fs.StringVar(&o.worktree, "worktree", "", "explicit worktree path")
	fs.StringVar(&o.from, "from", "", "start point for a new branch")
	fs.StringVar(&o.workshop, "workshop", "", "workshop name")
	fs.StringVar(&o.definition, "definition", "",
		"worktree-relative path of the workshop definition to edit")
	fs.StringVar(&o.sdk, "sdk", "vscode-remote", "sdk owning the plug")
	fs.StringVar(&o.plug, "plug", "git-dir", "plug name")
	fs.BoolVar(&o.code, "code", false, "launch VS Code on success")
	fs.BoolVar(&o.dryRun, "dry-run", false, "print the plan; change nothing")
	fs.BoolVar(&o.verbose, "verbose", false, "echo every command and its output")
	fs.BoolVar(&o.force, "force", false, "ignore a stale lock")
	fs.DurationVar(&o.timeout, "timeout", 15*time.Minute, "per-command timeout")

	// Accept the branch either before or after the flags.
	var positional []string
	rest := argv
	for len(rest) > 0 {
		if err := fs.Parse(rest); err != nil {
			return nil, err
		}
		rest = fs.Args()
		if len(rest) == 0 {
			break
		}
		positional = append(positional, rest[0])
		rest = rest[1:]
	}
	if len(positional) > 1 {
		return nil, fmt.Errorf("at most one branch may be given, got %v", positional)
	}
	if len(positional) == 1 {
		o.branch = positional[0]
	}

	cwd, err := os.Getwd()
	if err != nil {
		return nil, err
	}
	if o.repo == "" {
		o.repo = cwd
	}
	o.repo, err = filepath.Abs(o.repo)
	if err != nil {
		return nil, err
	}
	if o.worktree != "" {
		if o.worktree, err = filepath.Abs(o.worktree); err != nil {
			return nil, err
		}
	}
	if o.sdk == "" || o.plug == "" {
		return nil, errors.New("--sdk and --plug must not be empty")
	}
	return &o, nil
}
