// Package lock provides the coarse advisory lock that serialises runs against
// one worktree (design D16). The lock file lives outside the worktree so that it
// never shows up in `git status` (risk R10).
package lock

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock is a held advisory lock.
type Lock struct {
	path string
	f    *os.File
}

// Path returns the lock file path, for diagnostics.
func (l *Lock) Path() string { return l.path }

// Release unlocks and closes the lock file.
func (l *Lock) Release() {
	if l == nil || l.f == nil {
		return
	}
	_ = syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}

// dir returns the directory holding lock files: $XDG_RUNTIME_DIR/wt when set,
// else the OS temp dir.
func dir() string {
	base := os.Getenv("XDG_RUNTIME_DIR")
	if base == "" {
		base = os.TempDir()
	}
	return filepath.Join(base, "wt")
}

// PathFor returns the lock file path for a worktree directory.
func PathFor(worktree string) string {
	sum := sha256.Sum256([]byte(filepath.Clean(worktree)))
	return filepath.Join(dir(), hex.EncodeToString(sum[:8])+".lock")
}

// Acquire takes a non-blocking exclusive lock for worktree. When the lock is
// already held it fails, unless force is set, in which case the caller has
// explicitly decided to ignore a possibly stale holder.
func Acquire(worktree string, force bool) (*Lock, error) {
	path := PathFor(worktree)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		if force {
			return &Lock{path: path}, nil // held, but the user insists
		}
		return nil, fmt.Errorf(
			"another wt run is in progress for %s (lock %s); wait for it or pass --force",
			worktree, path,
		)
	}
	if _, err := fmt.Fprintf(f, "%d\n", os.Getpid()); err != nil {
		// Not fatal: the pid is only a hint.
		_ = err
	}
	return &Lock{path: path, f: f}, nil
}
