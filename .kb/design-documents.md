# Preface

This document describes the design-document process used in this repository: where designs live, the identifier vocabulary they define, and the rules that keep them in sync with the code. Read it before changing code that cites a design.

Read the top-level `.kb/agents.md` file before continuing below.


# Overview

Non-trivial behaviour is designed before it is written. A feature starts as a design document under `<tool>/designs/<feature>.md`, produced by the process in `DESIGNER.md`. It records the problem, constraints, a decisions table with trade-offs, the spec, alternatives with rejection reasons, the implementation plan, the test plan and the risks.

The design is not a historical artefact; it is the rationale the code refers back to. Its identifiers are the shared vocabulary of the project, cited from code comments so that non-obvious code points at the reason it exists:

- `§5.3` - a section of the design.
- `D14` - a decision from the decisions table.
- `C5` - a constraint.
- `R10` - a risk.

For example, `wt/internal/plan` opens with a doc comment citing design `§5.3` for the state machine it implements and `§1.4` for the states it must cover.


# Important

- Read the relevant design before changing code that cites it.
- When a change contradicts the design, update the design in the same change. Never let code silently diverge from a cited decision.
- When behaviour exists only because of a decision or risk, cite its identifier in a comment explaining why, not what.
- New non-trivial behaviour needs a design first. Do NOT invent decision identifiers for work that has none; add the decision to the design document instead.
- A design document is per feature, not per tool. A tool may accumulate several over time.
