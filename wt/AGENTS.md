# Preface

This document is the local knowledge base index for the `wt/` directory, the module `github.com/upils/tools/wt`. `wt` turns "start work on a branch in an isolated dev container" into one idempotent command: it creates a git worktree, wires the shared `.git` directory into the corresponding workshop container so that git works inside it, and prints the VS Code connection command.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

`wt` is a wrapper around two external commands, `git` and the `workshop` snap, and almost all of its complexity is in deciding what *not* to do. Re-running it against a partially converged worktree must be safe, and the naive sequence of workshop lifecycle transitions is not: several of them are invalid depending on the current status, so a blind replay produces hard errors instead of no-ops.

Consequently the interesting code is the convergence state machine in `internal/plan`, which is pure, and the invariants that make its decisions correct are recorded in `.kb/invariants.md`. Read that document before changing convergence behaviour, mount handling or the workshop lifecycle calls.

The single feature design is `designs/worktree-workshop.md`, and it is the authority for the `§`, `D`, `C` and `R` identifiers cited throughout the code. User-facing behaviour, flags and the full state table are documented in `README.md`.


# Important

- Build and test from inside this directory: `go build ./cmd/wt` and `go test ./...`.
- The entire dependency set is `gopkg.in/yaml.v3`, required for comment-preserving edits to user definitions. Adding a dependency requires a justification in the design.
- `internal/ws.Exec` is the single gateway for every child process. Do NOT call `os/exec` anywhere else; the pure packages take a narrow interface such as `gitwt.Runner` instead, which is what keeps them testable without real binaries.
- Exit codes are `0` success including "already done", `1` unexpected error, `2` usage error, and `3` refused because of the workshop status (`Pending`, `Waiting` or `Error`). They are the documented contract in `README.md`.
- `--dry-run` must remain free: it prints the plan the state machine produced, so read-only queries still run while mutating commands are only recorded.


# Architecture

The flow is a strict pipeline from observation to a plan to execution, and only the last step touches the world:

```
gitwt   discover the layout from git rev-parse --git-common-dir
wsdef   find, bootstrap or patch the workshop definition
ws      observe live state via workshop info
plan    pure function: observed state -> ordered steps        <- no side effects
cmd/wt  execute the steps through ws.Exec, or print them
```

Two facts are judged independently, and conflating them is the most common way to break the tool: whether the *definition* needs applying, via `workshop refresh`, and whether the *live binding* needs rebinding, via the stop, remount, start bracket. See `.kb/invariants.md`.


# Directory

- `cmd/wt/` - CLI entry point: flag parsing, exit codes and the imperative algorithm of design §5.3. Contains `up.go` for the `wt up` command, and the integration tests, which drive a real `git` against a faked `workshop` via the stub in `cmd/wt/testdata/workshop-stub.sh`.
- `internal/gitwt/` - Git plumbing: derives the path layout of a repository and its worktrees, and creates linked worktrees.
- `internal/lock/` - Coarse advisory `flock` serialising concurrent runs against one worktree, held outside the worktree.
- `internal/plan/` - The convergence state machine, as a pure function from observed state to an ordered plan.
- `internal/ws/` - Adapter for the `workshop` CLI: the single exec helper, tolerant parsers for `info` and `list`, and the operations the state machine needs.
- `internal/wsdef/` - Discovery, bootstrapping and comment-preserving patching of workshop definition files.
- `designs/` - Feature design documents, the authority for cited identifiers.
- `README.md` - User-facing documentation: flags, exit codes, behaviour table and the rationale for the mount.
- `go.mod`, `go.sum` - Module definition and dependency checksums.


# Documents

- `.kb/invariants.md` - The non-obvious rules that keep convergence correct, and the failure each one prevents.
- `designs/worktree-workshop.md` - The feature design implemented by this tool.
