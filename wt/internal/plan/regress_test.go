package plan

import (
	"testing"

	"github.com/upils/tools/wt/internal/ws"
)

// TestNoStopWhenMountAlreadyCorrect is a regression test.
//
// Reported symptom: after the workshop.yaml patch step failed and the user added
// a workshop.yaml by hand, re-running wt tried to *stop* the workshop.
//
// Root cause: the live remount override survives `workshop refresh` and
// stop/start cycles (design §1.3), so the mount can already be bound to the
// shared .git while workshop.yaml still needs patching. In that situation the
// mount needs no rebinding, and the expensive (and session-destroying) stop /
// remount / start bracket must not be emitted.
func TestNoStopWhenMountAlreadyCorrect(t *testing.T) {
	for _, status := range []string{ws.StatusReady, ws.StatusStopped} {
		t.Run(status, func(t *testing.T) {
			steps, err := Plan(State{Status: status, MountOK: true, YAMLChanged: true})
			if err != nil {
				t.Fatalf("Plan: %v", err)
			}
			for _, s := range steps {
				if s.Kind == Stop || s.Kind == Remount {
					t.Errorf("plan emits %q although the mount is already correct: %v",
						s.Kind, kinds(steps))
				}
			}
		})
	}
}
