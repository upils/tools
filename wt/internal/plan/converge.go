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

// Plan returns the steps that converge the workshop to "Ready with the shared
// .git bound" (design §5.3 steps 5–7).
//
// It never emits a transition that is invalid for the given status: no `start`
// on Ready, no `stop` on Stopped, and no `remount` of a populated source outside
// a Stopped bracket (C2, C4).
func Plan(s State) ([]Step, error) {
	switch s.Status {
	case ws.StatusPending, ws.StatusWaiting, ws.StatusError:
		return nil, &ErrRefused{Status: s.Status}
	case ws.StatusOff:
		// launch creates the container from the (already patched) definition
		// and leaves it Ready with the plug bound to the auto-allocated
		// directory; the bracket then points it at the shared .git. (state 1/2/3)
		return []Step{
			{Launch, "workshop is Off"},
			{Stop, "remounting a populated source requires a stopped workshop"},
			{Remount, "bind the shared .git directory"},
			{Start, "resume the workshop"},
		}, nil
	case ws.StatusReady:
		var steps []Step
		if s.YAMLChanged {
			// state 4: the plug exists in the definition but is not bound yet.
			steps = append(steps, Step{Refresh, "workshop.yaml changed; bind the new plug"})
		}
		if s.MountOK && !s.YAMLChanged {
			return nil, nil // state 6: steady state, nothing to do
		}
		steps = append(
			steps,
			Step{Stop, "remounting a populated source requires a stopped workshop"},
			Step{Remount, "bind the shared .git directory"},
			Step{Start, "resume the workshop"},
		)
		return steps, nil
	case ws.StatusStopped:
		var steps []Step
		if s.YAMLChanged {
			// `refresh` is documented against a Ready workshop, so the
			// definition change is applied after a start (§5.3 step 6).
			steps = append(
				steps,
				Step{Start, "apply the definition change from a started workshop"},
				Step{Refresh, "workshop.yaml changed; bind the new plug"},
			)
			steps = append(
				steps,
				Step{Stop, "remounting a populated source requires a stopped workshop"},
				Step{Remount, "bind the shared .git directory"},
				Step{Start, "resume the workshop"},
			)
			return steps, nil
		}
		if s.MountOK {
			return []Step{{Start, "workshop is stopped and the mount is already correct"}}, nil // state 7
		}
		return []Step{ // state 8
			{Remount, "bind the shared .git directory"},
			{Start, "resume the workshop"},
		}, nil
	default:
		return nil, fmt.Errorf("unknown workshop status %q", s.Status)
	}
}
