# Preface

This document records the non-obvious invariants of `wt`, each with the failure it prevents. These are the properties most easily broken by a plausible-looking change, because the correct behaviour is counter-intuitive and the incorrect one still appears to work in the common case. Read this before changing convergence, mount handling or the workshop lifecycle calls.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

`wt` derives its decisions from two independent questions, and the invariants below all protect that separation or the mount trick that makes the container usable at all. The design `designs/worktree-workshop.md` is the authority; the identifiers cited here are its decisions (`D`) and risks (`R`).


# Important

## The idempotency oracle is the live mount, not the definition file

Whether a rebind is needed is decided from the binding reported by `workshop info`, never from the presence of the plug in `workshop.yaml`. A remount override survives `workshop refresh` and stop/start cycles, so a changed definition does NOT imply that the binding is wrong.

Two facts are therefore judged independently, by `plan.Prepare` and `plan.Bracket` respectively:

- whether the definition needs applying, via `workshop refresh`;
- whether the live binding needs rebinding, via the stop, remount, start bracket.

`plan.Bracket` must be given the status and binding read *after* `Prepare`'s steps have run, which is why the executor performs the two phases separately and re-reads `workshop info` in between. `plan.Plan` composes both from a single snapshot, and exists only for `--dry-run` and for exhaustive testing, not for execution.

Getting this wrong emits a needless stop, remount, start bracket against a workshop that was already correct, which kills a live VS Code session (`R4`). `internal/plan/regress_test.go` guards it.

## The workshop-target and the remount source must carry the same string

A linked worktree has no `.git` directory, only a `.git` file holding an *absolute host path* to `<main>/.git/worktrees/<branch>`. A workshop mounts only the project directory, so the shared repository lies outside it and that absolute pointer resolves to nothing inside the container, breaking every git operation.

The fix is a deliberate path-identity trick: the mount's in-container `workshop-target` is set to a string byte-identical to the host path of the shared `.git`, so the pointer resolves inside the container and `commondir` lands on the same mount. The target is written into the definition by `internal/wsdef` while the real host directory is bound as the mount source by `workshop remount`. These are two independent mechanisms that must carry the same string, so keep one source of truth for it.

## The stop/start bracket is unavoidable but must happen at most once

A mount's host source can only be set by `workshop remount`, and remounting a populated source requires a stopped workshop. The bracket is therefore necessary the first time, but the resulting override persists across `refresh` and stop/start, so a correct implementation never repeats it. In the steady state `wt` performs a single read-only query and exits (`D8`).

## Paths come from git, never from the CWD

The layout is derived from `git rev-parse --path-format=absolute --git-common-dir`, which is authoritative from the main worktree and from any linked one alike. Do NOT infer the repository or worktree layout by manipulating the current directory string. Branch names containing `/` are flattened to a single path segment to avoid nested directories and collisions (`R7`).

## Refusal beats guessing

A project may hold its workshop definition in `workshop.yaml`, `.workshop.yaml`, or `.workshop/<name>.yaml`, so `internal/wsdef` searches all three. When several exist and none is identifiable by name, it refuses and lists them rather than picking one. `--definition` must point at an existing file, so that a typo fails instead of quietly creating a new definition. A bootstrap happens only when *no* definition exists anywhere.

Statuses `Pending`, `Waiting` and `Error` admit no valid transition, so `wt` refuses with exit code 3 and a diagnosis instead of attempting one (`D10`).

## User definitions are patched, never rewritten

`internal/wsdef/patch.go` edits definitions through `yaml.Node` to preserve comments, key order and formatting. Never marshal a parsed definition back out: it silently destroys the user's file.

## The lock lives outside the worktree

The advisory `flock` serialising concurrent runs is deliberately placed outside the worktree so it can never appear in `git status` (`D16`, `R10`). `TestLockIsOutsideWorktree` guards this.
