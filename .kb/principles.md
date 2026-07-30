# Preface

This document records the engineering principles every tool in this repository is held to, and what each one means concretely. It applies to all code in this repository, and is the standard a change is reviewed against.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

The principles are ordered: correctness outranks simplicity, simplicity outranks convenience. Where a principle is stated abstractly, a reference implementation in the tree is cited, because a principle with an example is enforceable and one without is decoration.

- Correctness is paramount.
- By default, keep things simple, easy to reason about and boring.
- A few deep abstractions are better than many shallow ones.
- Consistency across the project. Any deviation should be strongly justified and necessary.
- A tool must be as deterministic as possible, be predictable, always fail on the side of correctness, respect user content by default, and be as easy as possible to use.


# Important

- **Fail on the side of correctness** - When input is ambiguous, refuse and list what was found; never guess. `wt/internal/wsdef.Select` refusing several candidate definitions is the reference behaviour. A wrong action costs the user more than a clear refusal.
- **Respect user content** - Edits to user files preserve comments, key order and formatting, which is why `wt/internal/wsdef/patch.go` uses `yaml.Node` and never a marshal round-trip. Create a file only when none exists; a path given explicitly by the user must already exist rather than being created on a typo.
- **Deterministic and predictable** - No hidden network access, no ambient state, no action depending on wall-clock time or map iteration order. Derive paths from authoritative sources, such as `git rev-parse --git-common-dir`, rather than guessing from strings about the CWD.
- **Idempotent** - Every command is expected to be re-run against partially converged state and must detect completed work instead of erroring. Pick the live state as the idempotency oracle, not a proxy for it.
- **Standard library first** - Every dependency needs a justification in the design document.
- **Comments explain why** - The code already says what. When behaviour exists only because of a design decision or risk, cite its identifier.
