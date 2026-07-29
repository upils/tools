package plan

import (
	"errors"
	"testing"

	"github.com/upils/tools/wt/internal/ws"
)

// TestPlanStates covers the nine states of design §1.4.
func TestPlanStates(t *testing.T) {
	tests := []struct {
		name  string
		state State
		want  []Kind
	}{
		{
			// States 1–3 all reach here: the worktree/definition work is done
			// before Plan, and the workshop has no container yet.
			name:  "state 1-3: Off",
			state: State{Status: ws.StatusOff, YAMLChanged: true},
			want:  []Kind{Launch, Stop, Remount, Start},
		},
		{
			name:  "state 3: Off, yaml unchanged",
			state: State{Status: ws.StatusOff},
			want:  []Kind{Launch, Stop, Remount, Start},
		},
		{
			name:  "state 4: Ready, plug just injected",
			state: State{Status: ws.StatusReady, YAMLChanged: true},
			want:  []Kind{Refresh, Stop, Remount, Start},
		},
		{
			name:  "state 5: Ready, bound to the auto-allocated dir",
			state: State{Status: ws.StatusReady},
			want:  []Kind{Stop, Remount, Start},
		},
		{
			name:  "state 6: Ready, already correct",
			state: State{Status: ws.StatusReady, MountOK: true},
			want:  nil,
		},
		{
			name:  "state 7: Stopped, already correct",
			state: State{Status: ws.StatusStopped, MountOK: true},
			want:  []Kind{Start},
		},
		{
			name:  "state 8: Stopped, incorrect mount",
			state: State{Status: ws.StatusStopped},
			want:  []Kind{Remount, Start},
		},
		{
			name:  "Stopped with a yaml change needs a refresh from Ready",
			state: State{Status: ws.StatusStopped, YAMLChanged: true},
			want:  []Kind{Start, Refresh, Stop, Remount, Start},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			steps, err := Plan(tc.state)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			got := kinds(steps)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}

// TestPlanRefusesBadStatus covers state 9 (design D10).
func TestPlanRefusesBadStatus(t *testing.T) {
	for _, status := range []string{ws.StatusPending, ws.StatusWaiting, ws.StatusError} {
		steps, err := Plan(State{Status: status})
		if steps != nil {
			t.Errorf("%s: expected no steps, got %v", status, kinds(steps))
		}
		var refused *ErrRefused
		if !errors.As(err, &refused) {
			t.Errorf("%s: expected ErrRefused, got %v", status, err)
		}
	}
}

func TestPlanUnknownStatus(t *testing.T) {
	if _, err := Plan(State{Status: "banana"}); err == nil {
		t.Fatal("expected an error for an unknown status")
	}
}

// TestFastPathIsPure asserts the steady state emits no mutating step (D8).
func TestFastPathIsPure(t *testing.T) {
	steps, err := Plan(State{Status: ws.StatusReady, MountOK: true})
	if err != nil || len(steps) != 0 {
		t.Fatalf("steady state must be a no-op, got %v, %v", kinds(steps), err)
	}
}

// TestNoInvalidTransitions asserts the machine never issues a transition that
// workshop rejects for the current status (C4).
func TestNoInvalidTransitions(t *testing.T) {
	statuses := []string{ws.StatusOff, ws.StatusReady, ws.StatusStopped}
	for _, status := range statuses {
		for _, mountOK := range []bool{false, true} {
			for _, yamlChanged := range []bool{false, true} {
				steps, err := Plan(State{Status: status, MountOK: mountOK, YAMLChanged: yamlChanged})
				if err != nil {
					t.Fatalf("%s: %v", status, err)
				}
				// Simulate the status as the steps are applied.
				cur := status
				for _, s := range steps {
					switch s.Kind {
					case Launch:
						if cur != ws.StatusOff {
							t.Errorf("%s/%v/%v: launch from %s", status, mountOK, yamlChanged, cur)
						}
						cur = ws.StatusReady
					case Start:
						if cur != ws.StatusStopped {
							t.Errorf("%s/%v/%v: start from %s", status, mountOK, yamlChanged, cur)
						}
						cur = ws.StatusReady
					case Stop:
						if cur != ws.StatusReady {
							t.Errorf("%s/%v/%v: stop from %s", status, mountOK, yamlChanged, cur)
						}
						cur = ws.StatusStopped
					case Refresh:
						if cur != ws.StatusReady {
							t.Errorf("%s/%v/%v: refresh from %s", status, mountOK, yamlChanged, cur)
						}
					case Remount:
						// A populated source requires a stopped workshop (C2).
						if cur != ws.StatusStopped {
							t.Errorf("%s/%v/%v: remount from %s", status, mountOK, yamlChanged, cur)
						}
					}
				}
				if len(steps) > 0 && cur != ws.StatusReady {
					t.Errorf("%s/%v/%v: plan ends in %s, want ready", status, mountOK, yamlChanged, cur)
				}
			}
		}
	}
}

func kinds(steps []Step) []Kind {
	if steps == nil {
		return nil
	}
	out := make([]Kind, 0, len(steps))
	for _, s := range steps {
		out = append(out, s.Kind)
	}
	return out
}
