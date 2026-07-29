package lock

import (
	"os"
	"path/filepath"
	"testing"
)

func TestAcquireAndRelease(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	wt := "/some/worktree"

	l, err := Acquire(wt, false)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if _, err := os.Stat(l.Path()); err != nil {
		t.Fatalf("lock file missing: %v", err)
	}
	l.Release()

	// Releasing twice is safe.
	l.Release()

	l2, err := Acquire(wt, false)
	if err != nil {
		t.Fatalf("re-acquire after release: %v", err)
	}
	l2.Release()
}

// TestLockIsOutsideWorktree covers risk R10: the lock must never appear in
// `git status`.
func TestLockIsOutsideWorktree(t *testing.T) {
	runtime := t.TempDir()
	t.Setenv("XDG_RUNTIME_DIR", runtime)
	wt := t.TempDir()

	l, err := Acquire(wt, false)
	if err != nil {
		t.Fatal(err)
	}
	defer l.Release()

	if !filepath.HasPrefix(l.Path(), runtime) {
		t.Errorf("lock %q is not under %q", l.Path(), runtime)
	}
	entries, err := os.ReadDir(wt)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("worktree polluted: %v", entries)
	}
}

func TestPathForIsStableAndDistinct(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	a := PathFor("/a/b")
	if a != PathFor("/a/b/") {
		t.Error("PathFor must normalise the path")
	}
	if a == PathFor("/a/c") {
		t.Error("different worktrees must map to different locks")
	}
}

func TestForceIgnoresHeldLock(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())
	wt := "/held/worktree"

	// Hold the lock from a separate file descriptor so that flock actually
	// conflicts (flock is per open file description).
	held, err := Acquire(wt, false)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	if _, err := acquireFromNewFD(t, wt, false); err == nil {
		t.Error("expected a conflict without --force")
	}
	if _, err := acquireFromNewFD(t, wt, true); err != nil {
		t.Errorf("--force must ignore a held lock, got %v", err)
	}
}

// acquireFromNewFD re-runs Acquire, which always opens a fresh descriptor, so it
// conflicts with a lock held by this process through another descriptor.
func acquireFromNewFD(t *testing.T, wt string, force bool) (*Lock, error) {
	t.Helper()
	return Acquire(wt, force)
}
