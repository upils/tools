# Preface

This repository hosts a set of personal development tools, one per top-level directory, each its own independently built and versioned Go module. This `AGENTS.md` is the root index into the dispersed knowledge base; read it to orient yourself, then follow the links below for details.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

Each tool is self-contained: its own module, its own design documents, its own user-facing `README.md`, and its own `AGENTS.md`. There is deliberately no shared library between tools and no root Go module, so a tool can be copied out or retired without touching the others.

What every tool has in common is the process rather than the code: designed before written (`.kb/design-documents.md`), built as a pure core behind a thin imperative shell, and held to the principles in `.kb/principles.md`.


# Important

- There is NO root Go module. Every `go` command must run from inside a tool directory, never from the repository root:
  ```
  cd <tool> && go build ./cmd/<tool>
  cd <tool> && go test ./...
  ```
- Exit codes are part of a tool's contract and are documented in its `README.md`. The scheme shared by all tools is `0` success including "already done", `1` unexpected error, `2` usage error, `3` refused because of external state.
- User-facing documentation lives in `<tool>/README.md` and is updated in the same change as any behaviour change. Flags, exit codes and state tables are documented surface.
- Machine-readable output from child processes is forced rather than assumed, by setting `NO_COLOR`, `LC_ALL=C` and `TERM=dumb` in the child environment.


# Architecture

**Pure core, thin shell.** Anything expressible as a pure function - path derivation, output parsing, state machines - is one, and is unit-tested exhaustively. Process execution is isolated behind a single exec helper per tool, injected into the pure packages as a narrow interface so that tests need no real binaries.

A tool's layout follows from that split. `cmd/<tool>/` holds flag parsing, exit codes and the imperative algorithm; `internal/` holds the pure packages and the adapters for external commands. Decision logic is a pure function returning a plan that the shell then executes, which is what makes a `--dry-run` mode free and the state space testable.

Testing follows the same split. Pure packages are covered by table tests over the whole state space. Integration tests use the real binaries a tool wraps only when they are ubiquitous, such as `git`; anything else is faked with a stub on `PATH`, so that a test never requires an external tool the contributor may not have installed. Every fixed bug gets a regression test in a `regress_test.go` named after the design risk it guards.


# Directory

- `wt/` - The `wt` tool: bootstraps a git worktree together with its workshop dev container.
- `DESIGNER.md` - Instructions for the agent process that produces a feature design document.
- `workshop.yaml` - Dev container definition for working on this repository, providing the Go toolchain and the VS Code remote SDK. Launch with `workshop launch` then `workshop shell`.
- `README.md` - Project readme, the human entry point and the index of tools.
- `.gitignore` - Ignores spread artefacts, the worktree lock file and the built `wt` binary.


# Documents

- `.kb/agents.md` - General rules for the knowledge base reading and writing.
- `.kb/principles.md` - The engineering principles all code is held to, and their concrete meaning.
- `.kb/design-documents.md` - The design-first process and the `§`/`D`/`C`/`R` citation vocabulary.
- `wt/AGENTS.md` - The `wt` tool: worktree and workshop bootstrapper.
