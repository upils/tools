# `wt` — worktree + workshop bootstrapper

`wt` turns "start work on a branch in an isolated dev container" into one idempotent
command. It creates the git worktree, wires the shared `.git` directory into the
[workshop](https://github.com/canonical/workshop) container so git actually works inside
it, and prints the VS Code connection command.

```console
$ wt up my-feature
created worktree /home/me/projects/chisel-worktrees/my-feature (branch my-feature)
patched .../workshop.yaml (mount plug "git-dir" -> /home/me/projects/chisel/.git)
+ workshop launch chisel-dev
+ workshop stop chisel-dev
+ workshop remount chisel-dev/vscode-remote:git-dir /home/me/projects/chisel/.git
+ workshop start chisel-dev

workshop:  chisel-dev
project:   /home/me/projects/chisel-worktrees/my-feature
git-dir:   /home/me/projects/chisel/.git  (mounted)
hostname:  chisel-dev.my-feature.wp

code --folder-uri vscode-remote://ssh-remote+workshop@chisel-dev.my-feature.wp/project
```

Run it again and it does nothing, at the cost of a single read-only query:

```console
$ wt up my-feature
workshop chisel-dev is already ready with /home/me/projects/chisel/.git mounted
```

## Why this tool exists

The manual equivalent is seven steps, four of which are workshop lifecycle transitions
whose validity depends on the current status — so re-running the sequence after a partial
failure produces hard errors rather than no-ops:

```console
$ git worktree add ../<project>-worktrees/<branch>
$ cd ../<project>-worktrees/<branch>
$ vim workshop.yaml                     # inject a git-dir mount plug by hand
$ workshop launch
$ workshop stop
$ workshop remount <name>/<sdk>:git-dir /home/<user>/projects/<project>/.git/
$ workshop start
$ code --folder-uri vscode-remote://ssh-remote+workshop@$(workshop info | grep hostname: | awk '{print $2}')/project
```

### The non-obvious part: why the `.git` mount is needed

A linked worktree has no `.git` **directory** — it has a `.git` **file** containing an
absolute host path:

```
gitdir: /home/me/projects/chisel/.git/worktrees/my-feature
```

A workshop mounts only the *project directory* (the worktree) at `/project`. The shared
repository lives outside it, so inside the container git follows that absolute pointer,
finds nothing, and every git operation fails.

The fix is a deliberate **path-identity trick**: the mount's `workshop-target` is an
*in-container* path set to a string byte-identical to the *host* path of the shared
`.git`. The pointer then resolves inside the container, and `commondir: ../..` lands on
the same mount. `wt` sets the target in `workshop.yaml` and binds the real host directory
as the mount source via `workshop remount` — two independent things that must carry the
same string, which is why `wt` keeps a single source of truth for it.

The `stop`/`start` bracket is unavoidable: a mount's host source can only be set by
`remount`, and remounting a *populated* source requires a stopped workshop. But it is
only needed once — the override survives `workshop refresh` and stop/start cycles.

## Install

```console
$ cd wt && go build -o ~/.local/bin/wt ./cmd/wt
```

Requires Go 1.25+, `git`, and the `workshop` snap on `PATH`.

## Usage

```
wt up [<branch>] [flags]
```

`<branch>` is optional when the current directory is already inside a linked worktree.

| Flag | Default | Meaning |
|---|---|---|
| `-C`, `--repo <dir>` | CWD | Repository to derive the worktree layout from |
| `--worktree <dir>` | derived | Explicit worktree path; overrides derivation |
| `--from <rev>` | repo HEAD | Start point for a new branch |
| `--workshop <name>` | the single workshop defined | Workshop name |
| `--sdk <name>` | `vscode-remote` | SDK owning the mount plug |
| `--plug <name>` | `git-dir` | Plug name |
| `--code` | off | Launch VS Code on success |
| `--dry-run` | off | Print the plan; change nothing |
| `--verbose` | off | Echo every command and its output |
| `--force` | off | Ignore a stale lock |
| `--timeout <dur>` | `15m` | Per-workshop-command timeout |

Paths are derived from `git rev-parse --git-common-dir`, which is authoritative from both
the main worktree and any linked one:

```
/home/me/projects/chisel/.git                        → the shared repository
/home/me/projects/chisel-worktrees/<branch>          → the worktree wt creates
```

Branch names containing `/` are flattened (`feat/x` → `feat-x`) to avoid nested
directories; use `--worktree` to override.

### Exit codes

| Code | Meaning |
|---|---|
| `0` | Success, including "already done" |
| `1` | Unexpected error |
| `2` | Usage error |
| `3` | Refused because of the workshop status (`Pending`/`Waiting`/`Error`) |

### Inspecting what it would do

`--dry-run` prints the plan and changes nothing — the primary debugging aid, since the
plan is what the state machine decided:

```console
$ wt up my-feature --dry-run
plan for workshop chisel-dev (status ready):
  1. stop (remounting a populated source requires a stopped workshop)
  2. remount (bind the shared .git directory)
  3. start (resume the workshop)
```

## Behaviour

`wt` is built to be re-run against worktrees in arbitrary states. It detects what is
already done rather than issuing blind transitions:

| State | Action |
|---|---|
| Worktree absent | create, inject plug, launch, stop, remount, start |
| Plug missing, workshop `Off` | inject, launch, stop, remount, start |
| `Ready`, plug just injected | refresh, stop, remount, start |
| `Ready`, mount on the auto-allocated dir | stop, remount, start |
| **`Ready`, mount already correct** | **nothing** — print connection info |
| `Stopped`, mount correct | start only |
| `Stopped`, mount incorrect | remount, start |
| `Pending` / `Waiting` / `Error` | refuse, exit 3 with a diagnosis |

The idempotency oracle is the **live** binding reported by `workshop info`, not the
presence of the plug in `workshop.yaml` — the YAML says nothing about what is actually
mounted.

### Things worth knowing

- **`workshop.yaml` is left modified, deliberately.** The injected `workshop-target` is
  an absolute, machine-specific host path. It must never be committed. `wt` never stages
  or commits it, and prints a reminder. Comments and key order in the file are preserved.
- **A rebind interrupts a live session.** If the workshop is `Ready` with the wrong
  mount, converging requires a `stop`; `wt` warns before doing so. In the steady state
  this never happens.
- **Concurrent runs are serialised** by an advisory lock kept under `$XDG_RUNTIME_DIR`
  (not in the worktree, so it never shows up in `git status`). Use `--force` to override
  a stale one.
- **A crash mid-bracket is recoverable**: the next run sees `Stopped` with a correct
  mount and only starts.

### Not implemented

No teardown subcommand — remove a worktree manually with `workshop remove` and
`git worktree remove`.

## Design and development

`worktree-workshop.md` is the design this implementation follows. It records the problem
analysis, the constraints (`C1`…`C9`), a decisions table with trade-offs (`D1`…`D16`),
rejected alternatives, the test plan and the risks (`R1`…`R10`). Code comments reference
those IDs, so non-obvious code points back to its rationale.

```
cmd/wt/          main.go  flags, exit codes; up.go  the algorithm (§5.3)
internal/gitwt/  git plumbing: common-dir discovery, layout, worktree creation
internal/wsdef/  comment-preserving workshop.yaml patch (yaml.Node) + atomic write
internal/ws/     workshop CLI adapter: exec helper, info/list parsers, operations
internal/plan/   the convergence state machine, as a pure function
internal/lock/   advisory lock
```

The state machine is deliberately a pure `(status, mountOK, yamlChanged) → []Step`
function, so every state is unit-testable without a container, and a wrong assumption
about workshop's runtime behaviour is a one-table fix.

```console
$ go test ./...
```

The suite needs neither the `workshop` snap nor a network: unit tests cover the pure
logic, and the integration tests in `cmd/wt` drive the whole flow against real `git` plus
a scripted `workshop` stub (`cmd/wt/testdata/workshop-stub.sh`) that enforces the real
status model.

> **Status:** the runtime assumptions about `start`/`stop`/`refresh`/`remount` edge
> behaviour are encoded in `internal/plan` and in the test stub, but have not yet been
> validated against the real snap (see §10 of the design). Two specifics to confirm on
> first real use: whether `refresh` is accepted while `Stopped`, and whether git can
> *write* inside the container — the mount target lies outside `/home/workshop`, so its
> `uid:gid` defaults to `0:0` (risk `R3`); if a `git fetch` in the workshop fails, the
> injected plug needs explicit `uid`/`gid`.
