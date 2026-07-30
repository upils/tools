# Feature design: `wt` — worktree + workshop bootstrapper

Status: implemented (see `cmd/wt`, `internal/`). T0 (§10) is still outstanding: the
runtime assumptions about `start`/`stop`/`refresh`/`remount` edge behaviour are encoded
in `internal/plan` and the test stub, and must be re-validated against the real snap.
Target repo: this repository (`tools`), module `github.com/upils/tools/wt`
Binary: `wt`

All absolute paths in this document are illustrative. `/home/user@example.com/...` stands
for a real home directory; the `@` is kept deliberately because an `@` in `$HOME` is a
property the implementation must handle (§5.2, and a dedicated test in `internal/gitwt`).

---

## 1. Problem

### 1.1 The manual workflow today

To start work on a branch in an isolated dev container, the user performs, by hand:

```console
$ git worktree add ../<project>-worktrees/<branch>
$ cd ../<project>-worktrees/<branch>
$ vim workshop.yaml                     # inject a `git-dir` mount plug
$ workshop launch
$ workshop stop
$ workshop remount <name>/<sdk>:git-dir /home/<user>/projects/<project>/.git/
$ workshop start
$ code --folder-uri vscode-remote://ssh-remote+workshop@$(workshop info | grep "hostname:" | awk '{print $2}')/project
```

This is 7 steps, 4 of which are workshop lifecycle transitions whose validity depends
on the current workshop status. Re-running the sequence after a partial failure, or on
an already-provisioned worktree, produces hard errors rather than no-ops (§2.3).

### 1.2 Why the `git-dir` mount is needed at all

This is the non-obvious core of the problem, and it drives the whole design.

A linked worktree does **not** contain a `.git` directory. It contains a `.git` *file*
holding an absolute host path (verified in `chisel-worktrees/improve-workshop`):

```
gitdir: /home/user@example.com/projects/chisel/.git/worktrees/improve-workshop
```

and the administrative directory it points at back-references the shared object store:

```console
$ cat /home/.../projects/chisel/.git/worktrees/improve-workshop/commondir
../..
```

A workshop mounts the *project directory* (the worktree) at `/project` inside the
container. The shared repository at `.../projects/chisel/.git` is **outside** the
project directory and therefore absent from the container. Git inside the workshop
follows the absolute `gitdir:` pointer, finds nothing, and every git operation fails.

The fix is a deliberate **path-identity trick**:

- `workshop-target` is an **in-container path**, not a host path. It is set to a string
  *identical* to the host path of the shared `.git` directory:

  ```yaml
  workshop-target: "/home/user@example.com/projects/chisel/.git"
  ```

- Because the in-container path is byte-identical to the host path, the absolute
  `gitdir:` pointer in the worktree's `.git` file resolves **inside** the container,
  and `commondir: ../..` lands on the same mount.

- `workshop remount` then binds the **real host** `.git` directory as the mount's
  `host-source` onto that target.

So the design must set two independent things that happen to carry the same string:
a *target* (in `workshop.yaml`, an in-container path) and a *source* (via `remount`,
a host path).

### 1.3 Why `launch → stop → remount → start`

Two documented constraints force the dance:

1. A mount's host source **cannot** be declared in `workshop.yaml`. Per
   `docs/reference/definition-files/_interfaces/mount.rst`, the system SDK provides the
   only host-backed mount slot, `system:mount`, with "a dynamic `host-source` attribute
   that **can be configured only at remount**". So `remount` is mandatory; the plug is
   first bound to an auto-allocated host directory under
   `~/.local/share/workshop/id/<PROJECT-ID>/<WORKSHOP>/mount/<SDK>/<PLUG>/`.

2. `remount` only swaps a *live* mount atomically when the new source is non-existent
   or empty. A populated source such as `.git` requires status `Stopped`
   (`docs/reference/cli/workshop-remount.rst`, `docs/how-to/customize-workshops/add-mounts.rst`).

Therefore the `stop`/`start` bracket is unavoidable, but it is needed **only on the
first remount**. Verified properties that make the operation cheap afterwards:

- the remount override **survives `workshop refresh`** and stop/start cycles;
- `workshop remove` deletes the *record* of the remount but not the host data.

So a correctly written tool performs the expensive path once, then becomes a near no-op.

### 1.4 What "smart enough to detect the work is already done" must mean

The tool is invoked repeatedly against worktrees in arbitrary states. Partial states
that occur in practice:

| # | State | Required action |
|---|---|---|
| 0 | No definition in any documented location | bootstrap a minimal definition, then as below (D17, D19) |
| 0b | Several definitions, none named after the project | refuse; `--definition`/`--workshop` disambiguate (D19) |
| 1 | Worktree absent | create worktree, inject plug, launch, stop, remount, start |
| 2 | Worktree exists, `workshop.yaml` lacks the plug, workshop `Off` | inject, launch, stop, remount, start |
| 3 | Worktree exists, plug present, workshop `Off` | launch, stop, remount, start |
| 4 | Workshop `Ready`, plug just injected (not yet bound) | refresh, stop, remount, start |
| 4b | Workshop `Ready`, definition changed but mount **already** correct | refresh only (D18) |
| 5 | Workshop `Ready`, mount bound to the auto-allocated dir | stop, remount, start |
| 6 | Workshop `Ready`, mount already bound to the shared `.git` | **nothing** — print connection info |
| 7 | Workshop `Stopped`, mount already correct | start only |
| 8 | Workshop `Stopped`, mount incorrect | remount, start |
| 9 | Workshop `Pending` / `Waiting` / `Error` | refuse with a clear diagnosis |

State 6 is the steady state and must cost one read-only query.

---

## 2. Verified facts (evidence base)

All facts below were confirmed against a local checkout of the `workshop` source/docs
(module `github.com/canonical/workshop`, go 1.26.2) and against live worktrees of a
separate project, referred to below as `chisel`. The `workshop` snap could **not** be
executed in this environment (`snap-confine has elevated permissions and is not confined`),
so no runtime output was captured; everything is source- or doc-derived and must be
re-validated once (§8, T0).

### 2.1 Command surface

| Command | Usage | Notes |
|---|---|---|
| `launch` | `workshop launch <WORKSHOP>... [flags]` | ties workshop to project **and starts it**; flags `--no-wait`, `--verbose`, `--wait-on-error`, `--continue`, `--abort` |
| `start` | `workshop start <WORKSHOP>...` | **error** if already started or not launched |
| `stop` | `workshop stop <WORKSHOP>...` | **error** if not started/launched; doc also says it tolerates "already stopped" — must be verified (§8, T0) |
| `refresh` | `workshop refresh` | applies definition changes; binds newly added plugs; preserves remount override |
| `remount` | `workshop remount <WORKSHOP>/<SDK>:<PLUG> <SOURCE> [--no-wait]` | source made absolute via `filepath.Abs` (`cmd/workshop/remount.go`) |
| `info` | `workshop info [<WORKSHOP>]` | YAML-ish; see §2.3 |
| `list` | `workshop list [--global] [--no-headers]` | includes `Off` workshops when scoped to a project |
| `remove` | `workshop remove [--project <dir>]` | out of scope (teardown deferred) |

Global persistent flag, on every command (`cmd/workshop/root.go:126`):

```go
cmd.PersistentFlags().StringVarP(&c.prj, "project", "p", c.cwd, "Specify the project's directory path.")
```

`-p` is honoured by `refresh, start, stop, info, exec, shell, run, remove, remount,
connections, connect, disconnect` (`root.go` `requireProject`). **`launch` is not in
that list** — it accepts `-p` as a flag (it is persistent) but the completion code notes
the project "might be unknown (e.g. for `workshop launch`)". Consequence for the design:
do **not** rely on `-p` for `launch`; `cd` into the worktree instead (§4.2, D4).

The workshop name argument is optional when the project has exactly one workshop
(`cli.SingleWorkshopName`).

### 2.2 Status model

`docs/reference/workshop-status.rst`: `Off`, `Ready`, `Stopped`, `Pending`, `Waiting`,
`Error`. Relevant transitions:

```
OFF     --launch-->  READY
READY   --stop-->    STOPPED       READY --remount--> READY (atomic case only)
STOPPED --start-->   READY         STOPPED --remount--> STOPPED
READY   --refresh--> READY
```

`Off` means "definition file exists, no container". `workshop list` synthesises it from
definition files (`cmd/workshop/list.go` `sortWorkshops`, `result = append(..., "Off", "-")`).

### 2.3 `workshop info` output: is it parseable YAML?

The user's concern is justified but resolvable. `cmd/workshop/info.go` writes through
`tabwriter.NewWriter(Stdout, 4, 3, 2, ' ', tabwriter.StripEscape)` and emits
`key:\tvalue` lines, so alignment is done with spaces and the `tabwriter.Escape`
sentinel bytes are stripped. Three decorations could break YAML, and **all three are
disabled when stdout is not a TTY**:

- ANSI colour: `colorTable()` returns `noesc` when `!isStdoutTTY`
  (`cmd/internal/cmdutil/color.go`, `var isStdoutTTY = term.IsTerminal(1)`).
- Unicode: `canUnicode()` returns false when `!isStdoutTTY`, so the empty-value dash is
  `--` — and the source comments confirm intent: `esc.Dash = "–" // that's an en dash
  (so yaml is happy)` / `esc.Dash = "--" // two dashes keeps yaml happy also`.
- OSC-8 hyperlinks: `Escapes.MakeLink` returns the plain `fallback` when
  `e.hyperlink == ""`, which is the case for `noesc`.

Scalars are escaped through `cmdutil.EscapeYAMLScalar`, which round-trips via
`yaml.Marshal` (falling back to `strconv.Quote` for multi-line values). Conclusion:
**captured (piped) `info` output is intended to be valid YAML** and can be unmarshalled.

A subtle and important consequence of the hyperlink fallback: for auto-allocated
sources, `info.go` computes

```go
hostSource := string(escape(cmdutil.ContractHome(mount.HostSource)))
shortened := shortenDefaultPath(mount.HostSource, xdg, esc)
if shortened != mount.HostSource {
        hostSource = esc.MakeLink(shortened, link, hostSource)
}
```

so on a TTY the value is the pretty `…/<id>/mount/<sdk>/<plug>` link text, but when
piped it is the **full** path with `$HOME` contracted to `~`. Parsing must therefore
expand a leading `~/` before comparison (`cmdutil.ContractHome` does the contraction).

Relevant emitted shape:

```yaml
name:      chisel-dev
base:      ubuntu@24.04
project:   ~/projects/chisel-worktrees/improve-workshop
hostname:  chisel-dev.improve-workshop.wp
status:    ready            # lowercase: strings.ToLower(workshop.Status)
notes:     --
sdks:
  vscode-remote:
    tracking:  latest/stable
    installed: 1.2.3 2026-07-21 (42)
    mounts:
      git-dir:
        host-source:      ~/projects/chisel/.git
        workshop-target:  /home/user@example.com/projects/chisel/.git
```

Notes and risks for the parser:
- `status` is **lowercased**; `list` is not.
- `hostname:` is printed only when non-empty (absent while `Off`).
- `installed:` packs version/date/revision into one line — it is not a mapping; do not
  assume every nested key is well-formed YAML for fields we do not need. We only need
  `hostname`, `status`, and `sdks.<sdk>.mounts.<plug>.host-source`.
- Given `installed:` and similar cosmetic lines, a strict struct unmarshal is safer than
  a permissive `map[string]any` walk only if the shape is stable. Decision in D6.

### 2.4 Project scoping and name collisions

Workshops are keyed by **(project directory, name)**. `docs/how-to/develop-with-workshops/use-git.rst`
shows two worktrees each defining `name: dev` coexisting:

```console
$ workshop list --global
PROJECT     WORKSHOP  STATUS  NOTES
~/original  dev       Ready   -
~/resolved  dev       Ready   -
```

So the existing practice of `name: chisel-dev` in every worktree is safe, and hostnames
disambiguate by project basename (`chisel-dev.improve-workshop.wp`). **No renaming is
needed.** This removes what would otherwise be the largest source of complexity.

### 2.5 Git plumbing

Verified in `chisel-worktrees/improve-workshop`:

```console
$ git rev-parse --path-format=absolute --git-common-dir
/home/user@example.com/projects/chisel/.git
$ git rev-parse --path-format=absolute --git-dir
/home/user@example.com/projects/chisel/.git/worktrees/improve-workshop
$ git rev-parse --show-toplevel
/home/user@example.com/projects/chisel-worktrees/improve-workshop
```

`--git-common-dir` returns the shared `.git` **from both the main worktree and any linked
worktree**, which is exactly the value needed for `workshop-target` and for the `remount`
source. This is the single authoritative primitive; no string surgery on paths is required.

### 2.6 `workshop.yaml` is git-tracked

Verified: `git ls-files --error-unmatch workshop.yaml` succeeds; `main` contains the file
*without* the `git-dir` plug; the current worktree shows ` M workshop.yaml`.
`.gitignore` contains `.spread*` and `.workshop.lock`.

Consequence: injecting the plug **permanently dirties** the worktree, and the injected
value is machine-specific (an absolute host path with the user's `@`-containing home
directory). It must never be committed. Accepted per D3.

---

## 3. Constraints

1. **C1** — Host source of a mount is settable only through `workshop remount`.
2. **C2** — Remounting a populated source requires status `Stopped`.
3. **C3** — `workshop-target` must be an absolute in-container path, string-identical to
   the host `.git` path for the pointer trick to work.
4. **C4** — `start`/`stop` are stateful and error on the wrong status; the tool must
   branch on status rather than issue blind transitions.
5. **C5** — `workshop.yaml` is tracked and carries comments that must be preserved by any
   programmatic edit.
6. **C6** — No JSON output mode exists on `info`/`list`; text parsing is required, and it
   is only safe on non-TTY output.
7. **C7** — The `workshop` snap may be unavailable/misconfigured on the host; failures must
   be reported verbatim, not swallowed.
8. **C8** — Operations are slow (container launch). Long steps need progress output and
   must not be re-run needlessly.
9. **C9** — The default mount `uid`/`gid` for a target **outside** `/home/workshop/`,
   `/project/`, `/run/user/1000/` is `0:0` with mode `0755` (mount interface reference).
   The `git-dir` target is such a path. See R3.

---

## 4. Decisions

| ID | Decision | Rationale | Trade-off accepted |
|---|---|---|---|
| D1 | Single-purpose binary `wt` at `cmd/wt`, module `github.com/upils/tools/wt` | user choice; smallest surface | a second tool later needs its own module |
| D2 | Full scope: create the worktree when missing, then run the workshop dance; one command from a branch name | user choice; the worktree and the workshop are one logical unit | `wt` becomes a git wrapper too |
| D3 | Accept the dirty `workshop.yaml`; inject idempotently, skip when already correct; never `git add`/commit | user choice; keeps the file visible and honest | permanent ` M workshop.yaml` in every worktree |
| D4 | Never rely on `-p` for `launch`; run every `workshop` command with the process CWD set to the worktree, and *also* pass `-p <worktree>` where supported | `launch` is excluded from `requireProject` in `root.go`; CWD is the universally correct scoping | one extra field on the exec helper |
| D5 | Defaults `--sdk vscode-remote`, `--plug git-dir`, overridable | user choice | none |
| D6 | Parse `info` with a **narrow, tolerant** YAML model: unmarshal into a struct exposing only `hostname`, `status`, `sdks.<name>.mounts.<plug>.host-source`, with `yaml.Node`/`map[string]any` for unknown regions; force non-TTY plus `NO_COLOR=1`, `LC_ALL=C` in the child env | C6/§2.3; belt-and-braces against future decoration | if upstream changes the layout, parsing degrades — mitigated by M1 |
| D7 | Determine **status** from `workshop list --no-headers` (knows `Off`), determine **mount binding** from `workshop info` | `info` errors out for a non-existent workshop, conflating "absent" with "daemon down"; `list` distinguishes them | two queries in the cold path (one in the steady path, see D8) |
| D8 | Steady-state fast path: a single `workshop info`; if it reports `status: ready` **and** the correct `host-source`, exit immediately | C8; state 6 is the common case | none |
| D9 | Idempotency oracle is `host-source == <git-common-dir>` after `~` expansion and `filepath.Clean`; **not** the presence of the plug in `workshop.yaml` | the YAML says nothing about the live binding (C1) | requires the `info` parse to be correct |
| D10 | Refuse to act on `Pending`, `Waiting`, `Error`; exit non-zero with the status and the remedial command | transitions from these states are invalid or destructive | user must intervene |
| D11 | Edit `workshop.yaml` via `gopkg.in/yaml.v3` **Node** API, preserving comments/order; write atomically (temp file + `rename`) in the same directory | C5; avoids clobbering the SDK comments present in the real file | Node manipulation is more verbose than struct round-trip |
| D12 | Print the ready-to-paste `code --folder-uri ...` command; execute it only under `--code` | user choice | none |
| D13 | No teardown subcommand in v1 | user choice | manual `workshop remove` + `git worktree remove` |
| D14 | `--dry-run` prints the planned command sequence and exits 0; `--verbose` echoes each command and its output | C7/C8; makes the state machine auditable and is the primary debugging aid | small amount of extra plumbing |
| D15 | Trailing-slash normalisation: strip it. `workshop-target` per the user's note must have **no** trailing slash; the `remount` source is normalised by `filepath.Abs` anyway | consistency; the doc example passes `~/new-cache-mount` with no slash | none |
| D16 | Take a coarse advisory lock (`flock` on `<worktree>/.wt.lock`, in `.gitignore`-able form) for the duration of a run | prevents two concurrent runs racing the stop/remount/start bracket | a stale lock needs `--force` |
| D19 | Resolve the definition across all three documented locations (`workshop.yaml`, `.workshop.yaml`, `.workshop/<NAME>.yaml`) instead of assuming the root file. With several, edit the one named after the project (`<project>-dev`, then `<project>`), or the one given by `--workshop`/`--definition`; otherwise refuse and list the candidates. Bootstrap only when **none** exists | the reference allows all three, and `<NAME>` under `.workshop/` must equal the `name` field, so the filename is authoritative; editing an arbitrary definition, or shadowing an existing layout with a new root file, would silently misconfigure the project | discovery is a directory read on every run; ambiguous multi-workshop projects need one extra flag |
| D17 | When no definition exists anywhere, bootstrap `name: <project>-dev` / `base: ubuntu@24.04` / `sdks: [vscode-remote]`, named after the **repository** (not the branch); never overwrite an existing file (`O_EXCL`) | many projects have no definition yet, and the minimal one is always the same; the sdk matches the default `--sdk` so the file is immediately patchable | the bootstrapped file is *untracked* rather than modified, so R5's "do not commit" caveat applies more strongly |
| D18 | Judge "definition needs applying" and "binding needs rebinding" **independently**, and re-read `workshop info` between the two phases: `Prepare(status, yamlChanged)` then `Bracket(status, mountOK)` | the remount override survives `refresh` and stop/start (§1.3), so a changed definition does **not** imply a wrong binding; conflating them stopped a healthy workshop | two `info` reads on the cold path instead of one |

---

## 5. Specification

### 5.1 CLI

```
wt up [<branch>] [flags]

Arguments:
  <branch>   Branch/worktree name. Optional when CWD is already inside a linked
             worktree, in which case the current worktree is used.

Flags:
  -C, --repo <dir>       Repository to derive the worktree layout from.
                         Default: CWD.
      --worktree <dir>   Explicit worktree path; overrides layout derivation.
      --from <rev>       Start point for a new branch. Default: the repo HEAD.
      --workshop <name>  Workshop name. Default: the single workshop in the
                         definition, else required.
      --sdk <name>       SDK owning the plug. Default: vscode-remote.
      --plug <name>      Plug name. Default: git-dir.
      --code             Launch VS Code on success.
      --dry-run          Print the plan; change nothing.
      --verbose          Echo every command and its output.
      --force            Ignore a stale lock.
      --timeout <dur>    Per-workshop-command timeout. Default: 15m.
```

Exit codes: `0` success (including "already done"); `1` unexpected error; `2` usage
error; `3` refused because of workshop status (D10).

### 5.2 Derivation of paths

```
gitCommonDir  = git -C <repo> rev-parse --path-format=absolute --git-common-dir
              → /home/<user>/projects/chisel/.git          (no trailing slash, Clean'd)
mainRoot      = filepath.Dir(gitCommonDir)                  → .../projects/chisel
projectName   = filepath.Base(mainRoot)                     → chisel
worktreeDir   = filepath.Join(filepath.Dir(mainRoot),
                              projectName+"-worktrees", branch)
```

`--worktree` overrides `worktreeDir`. If `gitCommonDir` does not end in `.git`
(bare repo, `core.worktree` oddity), abort with a clear message rather than guess.

`workshopTarget = gitCommonDir` (C3, D15).
`remountSource  = gitCommonDir`.

Both are the same string, by construction — this is the trick of §1.2 expressed in one
assignment, and the design deliberately keeps a single source of truth for it.

### 5.3 Algorithm

```
0.  parse flags; resolve repo, gitCommonDir, branch, worktreeDir
1.  acquire lock on worktreeDir (after step 2 creates it)

2.  ENSURE WORKTREE
    if worktreeDir exists:
        verify it is a linked worktree of this repo:
            `git -C worktreeDir rev-parse --path-format=absolute --git-common-dir`
            must equal gitCommonDir            → else abort (foreign directory)
        (branch mismatch is reported, not corrected)
    else:
        if branch exists locally:  git -C repo worktree add <worktreeDir> <branch>
        else:                      git -C repo worktree add -b <branch> <worktreeDir> [<--from>]

4.  ENSURE PLUG IN THE DEFINITION  (before the fast path: it is a local file read,
                                   and a live-but-undeclared mount is not converged —
                                   the next `workshop refresh` would drop it)
    resolve the definition file (D19):
        --definition <rel>            → must exist
        exactly one of workshop.yaml / .workshop.yaml / .workshop/*.yaml → use it
        several                       → the one named <project>-dev / <project>,
                                        or --workshop; else refuse   # state 0b
        none                          → write the bootstrap template,
                                        named after the repository   # state 0, D17
    read the resolved definition
    locate sdks[] entry with name == --sdk          → else abort (SDK not in definition)
    desired: plugs.<plug>.interface == "mount"
             plugs.<plug>.workshop-target == workshopTarget
    if already exactly that:   yamlChanged = false
    else:                      patch via yaml.Node, atomic write, yamlChanged = true

3.  FAST PATH   (only when yamlChanged == false, so that the declared and the
                 live state agree)
    info := workshop info [<name>] (cwd=worktreeDir, -p worktreeDir)
    if info parsed and info.status == "ready"
       and mountSource(info, sdk, plug) == gitCommonDir:
           print connection info; exit 0            # state 6

5.  RESOLVE STATUS
    status := from `workshop list --no-headers` (cwd=worktreeDir, -p worktreeDir),
              matched on the workshop name
    if status in {Pending, Waiting, Error}:  exit 3 with diagnosis      # state 9

6.  PREPARE — apply the definition only (plan.Prepare)
    switch status:
      Off:      workshop launch <name>                  # leaves it Ready
      Ready:    if yamlChanged: workshop refresh <name> # apply the definition
      Stopped:  if yamlChanged: workshop start; workshop refresh; (stays Ready)
                # refresh is documented against Ready; simplest correct route is
                # start → refresh
    # This step says NOTHING about the binding: the remount override survives
    # refresh and stop/start (§1.3), so a changed definition does not imply a
    # wrong binding. See D18.

7.  BRACKET — rebind only if the LIVE binding is wrong (plan.Bracket)
    info := workshop info <name>          # re-read; do not reuse step 5's snapshot
    if mountSource(info, sdk, plug) == gitCommonDir:
        if info.status == "stopped": workshop start <name>
        goto 8                                            # states 6/7/4b
    else:
        if info.status != "stopped": workshop stop <name>  # C2
        workshop remount <name>/<sdk>:<plug> <gitCommonDir>
        workshop start <name>

8.  VERIFY
    info := workshop info <name>
    assert info.status == "ready"
    assert mountSource(info, sdk, plug) == gitCommonDir   # else exit 1
    hostname := info.hostname                             # must be non-empty

9.  REPORT
    print:
      workshop:  <name>
      project:   <worktreeDir>
      git-dir:   <gitCommonDir>  (mounted)
      hostname:  <hostname>

      code --folder-uri vscode-remote://ssh-remote+workshop@<hostname>/project
    if --code: exec that command
```

Step 6's `Stopped`+`yamlChanged` branch is the one place where the documented state
machine offers no direct route (`refresh` is documented from `Ready` only), hence the
`start`-then-`refresh` ordering. T0 must confirm this.

### 5.4 Parsing contracts

**`workshop list --no-headers`** — whitespace-aligned columns `WORKSHOP STATUS NOTES`
when project-scoped (`cmd/workshop/list.go`). Parse by splitting on runs of ≥2 spaces,
take fields 1 and 2, compare status case-insensitively. Empty output ⇒ no workshop
known for this project at all (definition missing) ⇒ abort with a clear message.

**`workshop info`** — unmarshal the captured bytes as YAML into:

```go
type wsInfo struct {
    Name     string                  `yaml:"name"`
    Hostname string                  `yaml:"hostname"`
    Status   string                  `yaml:"status"`   // lowercase
    Project  string                  `yaml:"project"`  // may start with ~/
    Sdks     map[string]struct {
        Mounts map[string]struct {
            HostSource     string `yaml:"host-source"`
            WorkshopTarget string `yaml:"workshop-target"`
        } `yaml:"mounts"`
    } `yaml:"sdks"`
}
```

Unknown keys are ignored by default in `yaml.v3` (no `KnownFields`), which is what makes
this tolerant to the cosmetic `installed:`/`tracking:` lines. If unmarshalling fails, the
tool must print the raw captured output alongside the parse error (M1) — never fail
silently or fall back to guessing.

Path comparison helper:

```go
func samePath(a, b string) bool { return expandHome(filepath.Clean(a)) == expandHome(filepath.Clean(b)) }
```

where `expandHome` replaces a leading `~` with `$HOME`, mirroring `cmdutil.ContractHome`.

### 5.5 Child process contract

Every `workshop`/`git` invocation goes through one helper that:

- sets `Dir` to the worktree (D4) and appends `-p <worktree>` for the commands that
  accept it;
- sets `Env` to the parent env plus `NO_COLOR=1`, `LC_ALL=C`, `TERM=dumb`;
- captures stdout separately from stderr (stdout is the parse target; stderr is diagnostic);
- streams stderr to the terminal for long operations so container progress stays visible (C8);
- applies `--timeout`;
- on non-zero exit, wraps the error with the exact argv and the captured stderr (C7).

### 5.6 `workshop.yaml` patch

Given the real file, the patch must produce (comments preserved):

```yaml
  - name: vscode-remote # Standard tool for agentic work.
    plugs:
      git-dir:
        interface: mount
        workshop-target: "/home/user@example.com/projects/chisel/.git"
```

Rules: create `plugs` if missing; create the plug if missing; if the plug exists with a
different `workshop-target`, overwrite that scalar only, leaving any sibling keys
(`read-only`, `mode`, `uid`, `gid`) untouched; if it exists with a non-`mount` interface,
abort. Quote the target (it contains `@` and `.`; quoting is safe and matches existing
style). Write to `workshop.yaml.tmp-*` in the same directory, `fsync`, `rename`.

---

## 6. Alternatives considered

| Alternative | Why rejected |
|---|---|
| **Shell/bash script** | The logic is a 9-state machine over parsed command output with path normalisation; Go gives testable pure functions for derivation/parsing/patching, and comment-preserving YAML editing is impractical in bash. (Also the user's explicit preference.) |
| **Import `github.com/canonical/workshop/client`** for typed status/mounts | Tempting — it removes all text parsing (D6/§5.4 disappears). Rejected per the user: couples this tool to an internal-ish API, pulls a large dependency and its `go 1.26.2` floor, and requires matching the daemon's API version. Revisit if parsing proves brittle (M1). |
| **Commit the `git-dir` plug to `workshop.yaml`** | The target is an absolute, machine- and user-specific path; committing it breaks every other clone and leaks the username. |
| **Symlink the shared `.git` into the worktree** | Git resolves the `gitdir:` pointer as an absolute path; a symlink inside `/project` does not make `/home/<user>/projects/<p>/.git` exist in the container. Does not solve §1.2. |
| **Rewrite the worktree's `.git` file / `commondir` to container-relative paths** | Would make the worktree unusable from the host, and diverges the host and container views of the same checkout. |
| **Mount the parent `projects/` directory instead of just `.git`** | Exposes every unrelated repository to the container, defeating isolation, and still needs a `remount` (C1) so it saves nothing. |
| **Put the workshop's project directory at the main repo and use `git -C`** | Loses the point of worktrees: independent checkouts with independent workshop definitions/bases (`use-git.rst`). |
| **Rename the workshop per branch (e.g. `chisel-dev-<branch>`)** | Unnecessary: workshops are scoped by (project, name) (§2.4), and renaming would dirty `workshop.yaml`'s `name:` too. |
| **Blindly run the whole `launch/stop/remount/start` sequence and ignore errors** | Cannot distinguish "already done" from a real failure; violates the "smart enough to detect" requirement and risks stopping a workshop the user is actively using. |
| **Skip the fast path and always `stop`/`start`** | Wastes minutes per invocation and interrupts a live VS Code session (C8). |

---

## 7. Implementation plan

```
tools/wt/
├── go.mod                       module github.com/upils/tools/wt
├── cmd/wt/main.go               flag parsing, wiring, exit codes
└── internal/
    ├── gitwt/                   git plumbing
    │   ├── discover.go          CommonDir, WorktreeLayout, IsLinkedWorktree
    │   └── add.go               EnsureWorktree (branch exists? add / add -b)
    ├── wsdef/                   workshop.yaml
    │   ├── load.go              read + yaml.Node
    │   └── patch.go             EnsureMountPlug (idempotent), atomic Write
    ├── ws/                      workshop CLI adapter
    │   ├── run.go               exec helper (§5.5)
    │   ├── parse.go             ParseInfo, ParseList, samePath/expandHome
    │   └── ops.go               Launch, Refresh, Start, Stop, Remount, Info, List
    ├── plan/                    the state machine (§5.3), pure where possible
    │   └── converge.go          Plan(state) []Step ; Execute(steps)
    └── lock/flock.go            advisory lock (D16)
```

Order of work:

1. **T0 spike** (blocking): with the snap working, capture real `workshop info`, `list`,
   and the exit codes/messages for `start` on `Ready`, `stop` on `Stopped`, `remount`
   on `Ready` with a populated source, and `refresh` on `Stopped`. Save the fixtures.
2. `gitwt` + unit tests over throwaway repos created in `t.TempDir()`.
3. `wsdef` + golden-file tests using the real `chisel` `workshop.yaml` as input.
4. `ws/parse.go` against the T0 fixtures.
5. `plan/converge.go` as a **pure** function `(status, mountOK, yamlChanged) → []Step`,
   exhaustively unit-tested against the nine states of §1.4.
6. `ws/run.go` + `ops.go`; `--dry-run` and `--verbose` wired through.
7. `cmd/wt` + report/`--code`.
8. Manual end-to-end on a scratch branch of `chisel`.

Deliberately deferred: `wt down` (D13), multi-workshop definitions beyond
`--workshop`, non-`mount` interfaces, `wt` self-config file.

---

## 8. Test plan

**Unit — pure logic (no `workshop`, no network)**

- `gitwt.WorktreeLayout`: derivation from `--git-common-dir`; rejects bare repos; handles
  a home directory containing `@`; `--worktree` override.
- `wsdef.EnsureMountPlug`: table-driven over — plug absent; plug present and identical
  (must report *no change* and produce byte-identical output); present with a different
  target; present with a trailing slash; present with `read-only: true` sibling (preserved);
  `plugs` key absent; SDK absent (error); non-`mount` interface (error). Assert comments
  and SDK ordering survive.
- `ws.ParseInfo`: T0 fixtures + synthetic cases — `hostname` absent (`Off`); `notes: --`;
  `host-source` as `~/...`; auto-allocated `~/.local/share/workshop/id/...`; a
  quoted/escaped path; garbage input ⇒ error that includes the raw text.
- `ws.ParseList`: with/without headers, multiple workshops, `Off` rows, empty output.
- `samePath`: `~` expansion, trailing slash, `.`/`..` segments.
- `plan.Plan`: all nine states of §1.4 ⇒ exact expected step sequence; assert the fast
  path emits **zero** mutating steps, and that no plan ever emits `stop` for a status
  where `stop` is invalid.

**Integration — real `git`, faked `workshop`**

A stub `workshop` executable on `PATH` (a Go test binary honouring a scripted state
machine) lets the whole of §5.3 run hermetically:

- states 1–8 each drive the expected argv sequence, asserted in order;
- state 6 issues exactly one `info`;
- state 9 exits 3 without mutating anything;
- a stub failure mid-bracket (e.g. `remount` fails) surfaces stderr and exits 1 without
  a spurious `start`;
- `--dry-run` executes no mutating argv;
- re-running `wt up` immediately after a success is a no-op (the key regression test);
- concurrent runs: the second blocks/refuses on the lock.

**End-to-end — real `workshop`** (manual, gated on T0)

Against a scratch branch of `chisel`: cold `wt up` from no worktree; assert
`workshop info` shows `host-source` = the shared `.git`; assert `git status`, `git log`,
and `git fetch` all work *inside* the workshop (`workshop exec`); re-run `wt up` and
assert no transitions; `workshop stop` then `wt up` ⇒ start only; `workshop refresh` then
assert the remount override survived (per §1.3).

---

## 9. Risks and mitigations

| ID | Risk | Impact | Mitigation |
|---|---|---|---|
| **R1** | `workshop info` layout changes upstream; the parser silently misreads `host-source` | Wrong idempotency decision ⇒ needless stop/start, or a broken git setup believed good | Step 8 verifies post-state and hard-fails; parse errors print the raw output; narrow struct (D6); T0 fixtures pinned in tests. Escape hatch: switch to the Go client. |
| **R2** | T0 assumptions about `start`/`stop`/`refresh` edge behaviour are wrong (source not runnable here) | State machine issues an invalid transition | T0 is a **blocking** prerequisite; `plan.Plan` is pure so corrections are one table edit |
| **R3** | Mount target is outside `/home/workshop`, `/project`, `/run/user/1000`, so `uid:gid` defaults to `0:0`, mode `0755` (C9) — git inside the workshop may be unable to **write** `.git` (index, refs, logs) | Read-only repo in the container; confusing failures | The user's manual flow reportedly works, so the bind of the host `.git` supersedes the created directory's ownership — but E2E must explicitly test a **write** (`git fetch`/commit) inside the workshop. If it fails, set `uid`/`gid` in the injected plug and document it. |
| **R4** | `workshop stop` interrupts a live VS Code remote session | Lost work | Fast path avoids it entirely in the steady state; before stopping, print a warning naming the workshop; consider a confirmation prompt when `status == ready` and a remount is actually required |
| **R5** | Injected absolute host path gets committed by accident | Breaks other clones, leaks username | Never stage the file; the report prints a reminder that `workshop.yaml` is intentionally dirty; document `git update-index --skip-worktree` as an opt-in the user declined (D3) |
| **R6** | Crash between `stop` and `start` leaves the workshop `Stopped` | Confusing state | Idempotent by design: the next `wt up` sees `Stopped` + correct mount ⇒ state 7 ⇒ `start` only |
| **R7** | Two branches map to the same `worktreeDir` (branch names containing `/`, e.g. `feat/x`) | Collision or nested dirs | Reject or flatten `/` in the derived directory name; document the rule; `--worktree` is the override |
| **R8** | `workshop launch` scoping via CWD is wrong for some invocation | Workshop created against the wrong project | D4 sets both CWD and `-p`; step 8 asserts `info.project` equals `worktreeDir` |
| **R9** | Long operations look hung | User kills a container mid-launch | Stream stderr (§5.5); `--timeout` with a clear message; `--verbose` |
| **R10** | `.wt.lock` file appears in `git status` | Noise in a tracked repo | Place the lock outside the worktree (e.g. under `$XDG_RUNTIME_DIR`, keyed by a hash of `worktreeDir`) rather than inside it — preferred over amending `.gitignore` |

---

## 10. Open items for T0

1. Exact exit code and stderr for `start` on `Ready`, and for `stop` on `Stopped`.
2. Whether `refresh` is accepted while `Stopped` (would simplify §5.3 step 6).
3. Whether `remount` on a `Ready` workshop with a populated `.git` fails cleanly or
   partially applies.
4. Whether `launch` truly ignores `-p`, or honours it (would let us drop the CWD dance).
5. Real `info` output for a workshop with the `git-dir` mount, to pin the fixtures.
