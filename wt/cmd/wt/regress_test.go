package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
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
