// Package plan holds the convergence state machine of design §5.3 as a pure
// function, so that the nine states of §1.4 are exhaustively testable without a
// `workshop` binary (implementation plan step 5).
package plan

import (
	"fmt"

	"github.com/upils/tools/wt/internal/ws"
)

// Kind is a step of the plan.
type Kind string

const (
	Launch  Kind = "launch"
	Refresh Kind = "refresh"
	Start   Kind = "start"
	Stop    Kind = "stop"
	Remount Kind = "remount"
)

// Step is one workshop lifecycle transition.
type Step struct {
	Kind Kind
	// Why explains the step in the plan output (D14).
	Why string
}

func (s Step) String() string { return string(s.Kind) + " (" + s.Why + ")" }

// State is everything the plan depends on.
type State struct {
	// Status is the lowercased workshop status (ws.Status*).
	Status string
	// MountOK reports that <sdk>:<plug> is already bound to the shared .git
	// directory (the idempotency oracle, D9). It is meaningless when Status is
	// Off, since no container exists yet.
	MountOK bool
	// YAMLChanged reports that workshop.yaml was just patched and the new plug
	// still needs binding.
	YAMLChanged bool
}

// ErrRefused reports a status from which no transition is valid (D10).
type ErrRefused struct {
	Status string
}

func (e *ErrRefused) Error() string {
	return fmt.Sprintf("workshop status is %q; refusing to act. "+
		"Inspect it with `workshop info` and resolve it (e.g. `workshop stop` then `workshop start`) "+
		"before retrying", e.Status)
}

// Prepare returns the steps that bring the workshop to a state where the
// current definition is applied, and the status that results (design §5.3
// step 6).
//
// It deliberately says nothing about the mount binding: the remount override
// survives `refresh` and stop/start cycles (§1.3), so whether a rebind is needed
// can only be judged from a *fresh* `workshop info` taken after these steps.
// That is what Bracket is for.
func Prepare(status string, yamlChanged bool) (steps []Step, after string, err error) {
	switch status {
	case ws.StatusPending, ws.StatusWaiting, ws.StatusError:
		return nil, "", &ErrRefused{Status: status}
	case ws.StatusOff:
		// launch ties the workshop to the project and starts it, binding any
		// plug to an auto-allocated host directory.
		return []Step{{Launch, "workshop is Off"}}, ws.StatusReady, nil
	case ws.StatusReady:
		if yamlChanged {
			return []Step{{Refresh, "workshop.yaml changed; apply the definition"}}, ws.StatusReady, nil
		}
		return nil, ws.StatusReady, nil
	case ws.StatusStopped:
		if yamlChanged {
			// `refresh` is documented against a Ready workshop, so the
			// definition change is applied after a start (§5.3 step 6).
			return []Step{
				{Start, "apply the definition change from a started workshop"},
				{Refresh, "workshop.yaml changed; apply the definition"},
			}, ws.StatusReady, nil
		}
		return nil, ws.StatusStopped, nil
	default:
		return nil, "", fmt.Errorf("unknown workshop status %q", status)
	}
}

// Bracket returns the steps that bind the shared .git directory and leave the
// workshop Ready (design §5.3 step 7).
//
// status must be the *current* status and mountOK the *current* binding, read
// after Prepare's steps have run. When the binding is already correct no
// stop/remount/start bracket is emitted — that is what keeps the steady state
// free and avoids interrupting a live session (D8, R4).
func Bracket(status string, mountOK bool) ([]Step, error) {
	switch status {
	case ws.StatusReady:
		if mountOK {
			return nil, nil
		}
		return []Step{
			{Stop, "remounting a populated source requires a stopped workshop"},
			{Remount, "bind the shared .git directory"},
			{Start, "resume the workshop"},
		}, nil
	case ws.StatusStopped:
		if mountOK {
			return []Step{{Start, "workshop is stopped and the mount is already correct"}}, nil
		}
		return []Step{
			{Remount, "bind the shared .git directory"},
			{Start, "resume the workshop"},
		}, nil
	case ws.StatusPending, ws.StatusWaiting, ws.StatusError:
		return nil, &ErrRefused{Status: status}
	default:
		return nil, fmt.Errorf("unexpected workshop status %q after preparation", status)
	}
}

// Plan composes Prepare and Bracket into the full predicted sequence.
//
// The executor runs the two phases separately, re-reading the binding in
// between; Plan is the single-snapshot prediction used for `--dry-run` and for
// exhaustive testing of the state table.
//
// It never emits a transition that is invalid for the given status: no `start`
// on Ready, no `stop` on Stopped, and no `remount` of a populated source outside
// a Stopped bracket (C2, C4).
func Plan(s State) ([]Step, error) {
	steps, after, err := Prepare(s.Status, s.YAMLChanged)
	if err != nil {
		return nil, err
	}
	// After a launch the plug is bound to an auto-allocated directory, never to
	// the shared .git, so the binding is known-wrong regardless of the input.
	mountOK := s.MountOK
	if s.Status == ws.StatusOff {
		mountOK = false
	}
	bracket, err := Bracket(after, mountOK)
	if err != nil {
		return nil, err
	}
	return append(steps, bracket...), nil
}
