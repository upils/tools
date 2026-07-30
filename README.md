# tools

Personal development tools. Each tool lives in its own top-level directory with its
own Go module, so that tools can be built, versioned and depended on independently.

## Tools

| Tool | Description |
|---|---|
| [`wt`](wt/) | Bootstraps a git worktree together with its [workshop](https://github.com/canonical/workshop) dev container, wiring the shared `.git` directory into the container so git works inside it. One idempotent command instead of a seven-step manual dance. |

## Layout

```
tools/
├── workshop.yaml        dev container definition for working on this repo
├── AGENTS.md            root index into the agent knowledge base
├── .kb/                 repository-wide agent knowledge documents
├── DESIGNER.md          agent instructions for producing feature designs
└── <tool>/
    ├── go.mod           one module per tool
    ├── AGENTS.md        index into this tool's knowledge base
    ├── .kb/             knowledge documents specific to this tool
    ├── README.md        user-facing documentation
    ├── designs/         one design document per feature
    ├── cmd/<tool>/      entry point
    └── internal/        implementation packages
```

The `AGENTS.md` and `.kb/*.md` files are the knowledge base used by coding agents;
`.kb/agents.md` documents the conventions they follow. They are written to be
reviewable by humans too, and are the canonical place for contributor-facing rules —
the summary below is only an entry point.

## Conventions

- **Design first.** A feature starts as a design document under `<tool>/designs/`
  (see `DESIGNER.md` for the process) which records the problem, the constraints, a
  decisions table with trade-offs, alternatives and their rejection reasons, and the
  risks. The document is kept in sync with the code; section (`§5.3`), decision
  (`D4`), constraint (`C2`) and risk (`R3`) IDs are referenced from code comments so
  that non-obvious code points back to its rationale.
- **Pure core, thin shell.** Logic that can be a pure function (path derivation,
  output parsing, state machines) is one, and is unit-tested exhaustively. Process
  execution is isolated behind a single helper per tool.
- **Idempotent by default.** Commands are expected to be re-run against partially
  converged state and must detect completed work instead of erroring.

## Building

Each tool is a separate module, so build from inside its directory:

```console
$ cd wt && go build ./cmd/wt
```

Run the tests of one tool:

```console
$ cd wt && go test ./...
```

## Working on this repository

`workshop.yaml` defines the dev container used to work on this repository itself
(Go toolchain plus the VS Code remote SDK):

```console
$ workshop launch
$ workshop shell
```
