package main

import (
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/upils/tools/wt/internal/gitwt"
	"github.com/upils/tools/wt/internal/lock"
)

// gitInRepo runs git in dir, failing the test on error.
func gitInRepo(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(),
		"GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null",
		"GIT_AUTHOR_NAME=T", "GIT_AUTHOR_EMAIL=t@example.com",
		"GIT_COMMITTER_NAME=T", "GIT_COMMITTER_EMAIL=t@example.com")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
}

// TestUpBootstrapsMissingDefinition covers a project that has no workshop.yaml
// at all: wt must create a minimal one and then converge normally.
func TestUpBootstrapsMissingDefinition(t *testing.T) {
	h := newHarness(t)
	// The harness commits a workshop.yaml; remove it from the main worktree so
	// that the created linked worktree has none either.
	if err := os.Remove(filepath.Join(h.repo, "workshop.yaml")); err != nil {
		t.Fatal(err)
	}
	gitInRepo(t, h.repo, "commit", "-am", "drop workshop.yaml")
	// The bootstrapped name is derived from the repository directory ("tools").
	t.Setenv("WT_STUB_NAME", "tools-dev")

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}

	def := filepath.Join(h.worktree, "workshop.yaml")
	body, err := os.ReadFile(def)
	if err != nil {
		t.Fatalf("workshop.yaml was not created: %v", err)
	}
	for _, want := range []string{
		"name: tools-dev",
		"base: ubuntu@24.04",
		"- name: vscode-remote",
		"interface: mount",
		h.common,
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("missing %q in bootstrapped definition:\n%s", want, body)
		}
	}
	if h.source() != h.common {
		t.Errorf("host-source = %q, want %q", h.source(), h.common)
	}
	if h.status() != "ready" {
		t.Errorf("status = %q", h.status())
	}
}

// TestUpDoesNotOverwriteExistingDefinition is the safety counterpart: a
// hand-written definition must be patched, never replaced.
func TestUpDoesNotOverwriteExistingDefinition(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}
	body, err := os.ReadFile(filepath.Join(h.worktree, "workshop.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	// The committed definition's distinguishing content survives.
	if !strings.Contains(string(body), "# Standard tool for agentic work.") {
		t.Errorf("existing definition was replaced:\n%s", body)
	}
	if !strings.Contains(string(body), "name: go") {
		t.Errorf("unrelated sdk lost:\n%s", body)
	}
}

// TestUpAfterFailedPatchDoesNotStop reproduces the reported bug.
//
// Scenario: the workshop is running with the shared .git already bound, but
// workshop.yaml does not declare the plug (the patch step failed, and the user
// then added a definition by hand). Re-running wt must apply the definition and
// stop there — it must NOT stop the workshop to rebind a mount that is already
// correct, which would needlessly kill a live VS Code session (R4).
func TestUpAfterFailedPatchDoesNotStop(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}

	// Simulate the failed patch: the live binding is correct (it survives
	// refresh and stop/start per §1.3) but the definition lacks the plug.
	restoreUnpatchedDefinition(t, h)
	h.resetLog()

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}

	cmds := h.commands()
	if contains(cmds, "stop") {
		t.Errorf("workshop was stopped although the mount was already correct: %v", cmds)
	}
	if contains(cmds, "remount") {
		t.Errorf("workshop was remounted although the mount was already correct: %v", cmds)
	}
	if !contains(cmds, "refresh") {
		t.Errorf("the definition change was not applied: %v", cmds)
	}
	if h.status() != "ready" {
		t.Errorf("status = %q, want ready", h.status())
	}
	if h.source() != h.common {
		t.Errorf("host-source = %q, want %q", h.source(), h.common)
	}
}

// TestUpAfterFailedPatchWhileStopped is the same scenario with the workshop
// stopped: it must start and refresh, without a stop/remount bracket.
func TestUpAfterFailedPatchWhileStopped(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}
	restoreUnpatchedDefinition(t, h)
	h.setStatus("stopped")
	h.resetLog()

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	cmds := h.commands()
	if contains(cmds, "stop") || contains(cmds, "remount") {
		t.Errorf("needless bracket issued: %v", cmds)
	}
	if h.status() != "ready" {
		t.Errorf("status = %q", h.status())
	}
}

// restoreUnpatchedDefinition rewrites the worktree's workshop.yaml without the
// mount plug, as if the patch step had failed or the user wrote it by hand.
func restoreUnpatchedDefinition(t *testing.T, h *harness) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(h.worktree, "workshop.yaml"), []byte(defContent), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestLockCoversWorktreeCreation guards the ordering of §5.3 steps 1 and 2: the
// lock must be taken before `git worktree add`, or two concurrent runs race to
// create the same worktree. The lock is keyed by a hash of the path (D16), so it
// does not need the directory to exist.
func TestLockCoversWorktreeCreation(t *testing.T) {
	h := newHarness(t)

	// Hold the lock for the worktree that does not exist yet.
	held, err := lock.Acquire(h.worktree, false)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Release()

	if code := h.up(); code != exitError {
		t.Fatalf("exit = %d, want %d (refused on the held lock)", code, exitError)
	}
	if gitwt.DirExists(h.worktree) {
		t.Error("the worktree was created despite the lock being held")
	}
	if cmds := h.commands(); len(cmds) != 0 {
		t.Errorf("workshop was invoked despite the lock being held: %v", cmds)
	}
}

// TestReportNoteOnlyWhenDefinitionWritten guards R5's reminder from crying wolf:
// it must name the definition actually written, and must be absent from a
// steady-state run that touched no file.
func TestReportNoteOnlyWhenDefinitionWritten(t *testing.T) {
	h := newHarness(t)

	first := captureStdout(t, func() {
		if code := h.up(); code != exitOK {
			t.Fatalf("first run exit = %d", code)
		}
	})
	if !strings.Contains(first, "workshop.yaml is intentionally left modified") {
		t.Errorf("the run that patched the definition printed no reminder:\n%s", first)
	}

	second := captureStdout(t, func() {
		if code := h.up(); code != exitOK {
			t.Fatalf("second run exit = %d", code)
		}
	})
	if strings.Contains(second, "intentionally left modified") {
		t.Errorf("steady-state run claimed to have modified a file:\n%s", second)
	}
}

// TestVerificationErrorQuotesWorkshopOutput guards R1's diagnostic rule at
// step 8: when the post-state is wrong, the error must show what workshop
// actually reported, because that mismatch is the signature of a drifted parser.
func TestVerificationErrorQuotesWorkshopOutput(t *testing.T) {
	h := newHarness(t)
	// The stub reports a project other than the worktree, tripping the R8
	// assertion of step 8 after an otherwise successful convergence.
	t.Setenv("WT_STUB_PROJECT", filepath.Join(h.repo, "somewhere-else"))

	stderr := captureStderr(t, func() {
		if code := h.up(); code != exitError {
			t.Fatalf("exit = %d, want %d", code, exitError)
		}
	})

	if !strings.Contains(stderr, "R8") {
		t.Errorf("the diagnosis does not cite the risk it guards:\n%s", stderr)
	}
	if !strings.Contains(stderr, "--- workshop info ---") {
		t.Errorf("the raw workshop output was not quoted:\n%s", stderr)
	}
	// A recognisable line of the stub's `info` output must be present.
	if !strings.Contains(stderr, "hostname:") {
		t.Errorf("the quoted output does not look like `workshop info`:\n%s", stderr)
	}
}

// captureStdout runs fn with os.Stdout redirected and returns what it wrote.
func captureStdout(t *testing.T, fn func()) string {
	t.Helper()
	return capture(t, &os.Stdout, fn)
}

// captureStderr runs fn with os.Stderr redirected and returns what it wrote.
func captureStderr(t *testing.T, fn func()) string {
	t.Helper()
	return capture(t, &os.Stderr, fn)
}

func capture(t *testing.T, stream **os.File, fn func()) string {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	saved := *stream
	*stream = w

	done := make(chan string, 1)
	go func() {
		var sb strings.Builder
		_, _ = io.Copy(&sb, r)
		done <- sb.String()
	}()

	fn()

	*stream = saved
	w.Close()
	out := <-done
	r.Close()
	return out
}
