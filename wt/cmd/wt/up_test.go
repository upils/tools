package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/upils/tools/wt/internal/lock"
)

// harness is a hermetic environment: a real git repository plus a scripted
// `workshop` stub on PATH (design §8, "Integration — real git, faked workshop").
type harness struct {
	t        *testing.T
	repo     string
	stubDir  string
	worktree string
	common   string
}

const defContent = `name: tools-dev
base: ubuntu@24.04
sdks:
  - name: go
    channel: "1.25"
  - name: vscode-remote # Standard tool for agentic work.
`

func newHarness(t *testing.T) *harness {
	t.Helper()

	base := t.TempDir()
	repo := filepath.Join(base, "projects", "tools")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(dir string, args ...string) {
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
	git(repo, "init", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "workshop.yaml"), []byte(defContent), 0o644); err != nil {
		t.Fatal(err)
	}
	git(repo, "add", "workshop.yaml")
	git(repo, "commit", "-m", "initial")

	// Install the stub as `workshop`, first on PATH.
	stubDir := t.TempDir()
	binDir := filepath.Join(stubDir, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	stub, err := os.ReadFile(filepath.Join("testdata", "workshop-stub.sh"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(binDir, "workshop"), stub, 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("WT_STUB_DIR", stubDir)
	t.Setenv("WT_STUB_NAME", "tools-dev")
	t.Setenv("XDG_RUNTIME_DIR", t.TempDir())

	h := &harness{t: t, repo: repo, stubDir: stubDir}
	h.common = filepath.Join(repo, ".git")
	h.worktree = filepath.Join(base, "projects", "tools-worktrees", "feature")
	// The stub reports the project so that step 8's R8 assertion passes.
	t.Setenv("WT_STUB_PROJECT", h.worktree)
	return h
}

func (h *harness) setStatus(status string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.stubDir, "status"), []byte(status+"\n"), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) setSource(src string) {
	h.t.Helper()
	if err := os.WriteFile(filepath.Join(h.stubDir, "source"), []byte(src+"\n"), 0o644); err != nil {
		h.t.Fatal(err)
	}
}

func (h *harness) status() string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.stubDir, "status"))
	if err != nil {
		h.t.Fatal(err)
	}
	return strings.TrimSpace(string(b))
}

func (h *harness) source() string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.stubDir, "source"))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(b))
}

// commands returns the workshop subcommands invoked so far, in order.
func (h *harness) commands() []string {
	h.t.Helper()
	b, err := os.ReadFile(filepath.Join(h.stubDir, "log"))
	if err != nil {
		return nil
	}
	var out []string
	for _, line := range strings.Split(strings.TrimSpace(string(b)), "\n") {
		if line == "" {
			continue
		}
		out = append(out, strings.Fields(line)[0])
	}
	return out
}

func (h *harness) resetLog() {
	h.t.Helper()
	_ = os.Remove(filepath.Join(h.stubDir, "log"))
}

func (h *harness) up(extra ...string) int {
	h.t.Helper()
	argv := append([]string{"up", "feature", "-C", h.repo}, extra...)
	return run(argv)
}

// TestUpColdStart covers state 1: no worktree, workshop Off.
func TestUpColdStart(t *testing.T) {
	h := newHarness(t)

	if code := h.up(); code != exitOK {
		t.Fatalf("exit code = %d", code)
	}

	// The worktree was created as a linked worktree.
	if st, err := os.Stat(filepath.Join(h.worktree, ".git")); err != nil || st.IsDir() {
		t.Fatalf("worktree .git: %v", err)
	}
	// workshop.yaml was patched with the path-identity target (§1.2).
	def, err := os.ReadFile(filepath.Join(h.worktree, "workshop.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(def), `workshop-target: "`+h.common+`"`) {
		t.Errorf("definition not patched:\n%s", def)
	}
	if !strings.Contains(string(def), "# Standard tool for agentic work.") {
		t.Errorf("comments lost:\n%s", def)
	}
	// The full dance ran and the mount points at the shared .git.
	assertSubsequence(t, h.commands(), []string{"launch", "stop", "remount", "start"})
	if h.status() != "ready" {
		t.Errorf("status = %q", h.status())
	}
	if h.source() != h.common {
		t.Errorf("host-source = %q, want %q", h.source(), h.common)
	}
}

// TestUpIsIdempotent is the key regression test: a second run must be a no-op
// costing a single read-only query (design D8, state 6).
func TestUpIdempotentFastPath(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatalf("first run exit = %d", code)
	}
	h.resetLog()

	if code := h.up(); code != exitOK {
		t.Fatalf("second run exit = %d", code)
	}
	cmds := h.commands()
	if len(cmds) != 1 || cmds[0] != "info" {
		t.Fatalf("steady state must issue exactly one info, got %v", cmds)
	}
	if h.status() != "ready" {
		t.Errorf("status changed to %q", h.status())
	}
}

// TestUpStoppedCorrectMount covers state 7: start only.
func TestUpStoppedCorrectMount(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}
	h.setStatus("stopped")
	h.resetLog()

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	cmds := h.commands()
	for _, c := range cmds {
		if c == "stop" || c == "remount" {
			t.Errorf("unexpected %q in %v; only a start was needed", c, cmds)
		}
	}
	if !contains(cmds, "start") {
		t.Errorf("expected a start, got %v", cmds)
	}
	if h.status() != "ready" {
		t.Errorf("status = %q", h.status())
	}
}

// TestUpStoppedWrongMount covers state 8: remount then start, no stop.
func TestUpStoppedWrongMount(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}
	h.setStatus("stopped")
	h.setSource("~/.local/share/workshop/id/ABCD/tools-dev/mount/vscode-remote/git-dir")
	h.resetLog()

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	cmds := h.commands()
	if contains(cmds, "stop") {
		t.Errorf("must not stop an already stopped workshop: %v", cmds)
	}
	assertSubsequence(t, cmds, []string{"remount", "start"})
	if h.source() != h.common {
		t.Errorf("host-source = %q", h.source())
	}
}

// TestUpReadyWrongMount covers state 5: stop, remount, start.
func TestUpReadyWrongMount(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}
	h.setSource("~/.local/share/workshop/id/ABCD/tools-dev/mount/vscode-remote/git-dir")
	h.resetLog()

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	assertSubsequence(t, h.commands(), []string{"stop", "remount", "start"})
	if h.source() != h.common {
		t.Errorf("host-source = %q", h.source())
	}
}

// TestUpRefusesBadStatus covers state 9 (exit code 3, no mutation).
func TestUpRefusesBadStatus(t *testing.T) {
	for _, status := range []string{"pending", "waiting", "error"} {
		t.Run(status, func(t *testing.T) {
			h := newHarness(t)
			if code := h.up(); code != exitOK {
				t.Fatal("setup run failed")
			}
			h.setStatus(status)
			h.resetLog()

			if code := h.up(); code != exitRefused {
				t.Fatalf("exit = %d, want %d", code, exitRefused)
			}
			for _, c := range h.commands() {
				switch c {
				case "launch", "start", "stop", "remount", "refresh":
					t.Errorf("mutating command %q issued in status %s: %v", c, status, h.commands())
				}
			}
			if h.status() != status {
				t.Errorf("status changed to %q", h.status())
			}
		})
	}
}

// TestUpDryRunMutatesNothing covers D14.
func TestUpDryRunMutatesNothing(t *testing.T) {
	h := newHarness(t)
	if code := h.up("--dry-run"); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	if _, err := os.Stat(h.worktree); err == nil {
		t.Error("--dry-run created the worktree")
	}
	for _, c := range h.commands() {
		switch c {
		case "launch", "start", "stop", "remount", "refresh":
			t.Errorf("--dry-run issued %q", c)
		}
	}
}

// TestUpDryRunOnExistingWorktree checks the plan is printed without mutation
// once the worktree exists.
func TestUpDryRunOnExistingWorktree(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}
	h.setSource("~/.local/share/workshop/id/ABCD/tools-dev/mount/vscode-remote/git-dir")
	h.resetLog()

	if code := h.up("--dry-run"); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	for _, c := range h.commands() {
		switch c {
		case "launch", "start", "stop", "remount", "refresh":
			t.Errorf("--dry-run issued %q", c)
		}
	}
	if h.status() != "ready" {
		t.Errorf("status = %q", h.status())
	}
}

// TestUpRemountFailureSurfaces asserts a mid-bracket failure exits 1 without a
// spurious start (design §8).
func TestUpRemountFailureSurfaces(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}
	h.setSource("~/.local/share/workshop/id/ABCD/tools-dev/mount/vscode-remote/git-dir")
	// Make remount fail by pointing the stub at a different workshop name.
	t.Setenv("WT_STUB_NAME", "other-dev")
	h.resetLog()

	if code := h.up("--workshop", "tools-dev"); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	cmds := h.commands()
	if i := indexOf(cmds, "remount"); i >= 0 && contains(cmds[i+1:], "start") {
		t.Errorf("start issued after a failed remount: %v", cmds)
	}
}

// TestUpConcurrentRunsRefuse covers D16.
func TestUpConcurrentRunsRefuse(t *testing.T) {
	h := newHarness(t)
	if code := h.up(); code != exitOK {
		t.Fatal("setup run failed")
	}
	// Hold the lock, then run again.
	held, err := acquireHeld(h.worktree)
	if err != nil {
		t.Fatal(err)
	}
	defer held()

	if code := h.up(); code != exitError {
		t.Fatalf("exit = %d, want %d while the lock is held", code, exitError)
	}
	if code := h.up("--force"); code != exitOK {
		t.Fatalf("--force exit = %d", code)
	}
}

// TestUpUnknownSDK aborts clearly rather than guessing.
func TestUpUnknownSDK(t *testing.T) {
	h := newHarness(t)
	if code := h.up("--sdk", "not-there"); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
}

func TestUsageErrors(t *testing.T) {
	if code := run(nil); code != exitUsage {
		t.Errorf("no args: exit = %d", code)
	}
	if code := run([]string{"bogus"}); code != exitUsage {
		t.Errorf("unknown command: exit = %d", code)
	}
	if code := run([]string{"--help"}); code != exitOK {
		t.Errorf("--help: exit = %d", code)
	}
	if code := run([]string{"up", "a", "b"}); code != exitUsage {
		t.Errorf("two branches: exit = %d", code)
	}
}

// --- helpers ----------------------------------------------------------------

func contains(hay []string, needle string) bool { return indexOf(hay, needle) >= 0 }

func indexOf(hay []string, needle string) int {
	for i, s := range hay {
		if s == needle {
			return i
		}
	}
	return -1
}

// acquireHeld holds the worktree lock, returning a release function.
func acquireHeld(worktree string) (func(), error) {
	l, err := lock.Acquire(worktree, false)
	if err != nil {
		return nil, err
	}
	return l.Release, nil
}

// assertSubsequence checks that want appears in got in order.
func assertSubsequence(t *testing.T, got, want []string) {
	t.Helper()
	i := 0
	for _, g := range got {
		if i < len(want) && g == want[i] {
			i++
		}
	}
	if i != len(want) {
		t.Errorf("commands %v do not contain the sequence %v", got, want)
	}
}
