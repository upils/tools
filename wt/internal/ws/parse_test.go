package ws

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// infoFixture mirrors the shape documented in design §2.3 for a piped (non-TTY)
// `workshop info`.
const infoFixture = `name:      chisel-dev
base:      ubuntu@24.04
project:   ~/projects/chisel-worktrees/improve-workshop
hostname:  chisel-dev.improve-workshop.wp
status:    ready
notes:     --
sdks:
  go:
    tracking:  1.25/stable
    installed: 1.25.3 2026-07-21 (42)
  vscode-remote:
    tracking:  latest/stable
    installed: 1.2.3 2026-07-21 (42)
    mounts:
      git-dir:
        host-source:      ~/projects/chisel/.git
        workshop-target:  /home/user@example.com/projects/chisel/.git
`

func TestParseInfo(t *testing.T) {
	info, err := ParseInfo(infoFixture)
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if info.Name != "chisel-dev" {
		t.Errorf("name = %q", info.Name)
	}
	if info.Status != StatusReady {
		t.Errorf("status = %q", info.Status)
	}
	if info.Hostname != "chisel-dev.improve-workshop.wp" {
		t.Errorf("hostname = %q", info.Hostname)
	}
	src, ok := info.MountSource("vscode-remote", "git-dir")
	if !ok {
		t.Fatal("mount not found")
	}
	if src != "~/projects/chisel/.git" {
		t.Errorf("host-source = %q", src)
	}
	// The ~ must be expanded before comparison (§2.3).
	home, _ := os.UserHomeDir()
	if !info.MountIs("vscode-remote", "git-dir", filepath.Join(home, "projects/chisel/.git")) {
		t.Error("MountIs should expand ~")
	}
	if info.MountIs("vscode-remote", "git-dir", filepath.Join(home, "elsewhere/.git")) {
		t.Error("MountIs must not match a different path")
	}
	if _, ok := info.MountSource("go", "git-dir"); ok {
		t.Error("go sdk has no mounts")
	}
	if _, ok := info.MountSource("absent", "git-dir"); ok {
		t.Error("absent sdk must not resolve")
	}
}

// TestParseInfoAutoAllocated covers state 5: the plug is bound to the
// auto-allocated host directory, not the shared .git.
func TestParseInfoAutoAllocated(t *testing.T) {
	const out = `name:      chisel-dev
status:    ready
hostname:  chisel-dev.wp
sdks:
  vscode-remote:
    mounts:
      git-dir:
        host-source:      ~/.local/share/workshop/id/AbCd1234/chisel-dev/mount/vscode-remote/git-dir
        workshop-target:  /home/u/projects/chisel/.git
`
	info, err := ParseInfo(out)
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if info.MountIs("vscode-remote", "git-dir", "/home/u/projects/chisel/.git") {
		t.Error("auto-allocated source must not be considered correct")
	}
}

// TestParseInfoOff: an Off workshop prints no hostname (§2.3).
func TestParseInfoNoHostname(t *testing.T) {
	info, err := ParseInfo("name:    chisel-dev\nstatus:  stopped\nnotes:   --\n")
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if info.Hostname != "" {
		t.Errorf("hostname = %q, want empty", info.Hostname)
	}
	if info.Status != StatusStopped {
		t.Errorf("status = %q", info.Status)
	}
}

func TestParseInfoQuotedPath(t *testing.T) {
	const out = `name: chisel-dev
status: READY
sdks:
  vscode-remote:
    mounts:
      git-dir:
        host-source: "/home/user@example.com/projects/chisel/.git"
`
	info, err := ParseInfo(out)
	if err != nil {
		t.Fatalf("ParseInfo: %v", err)
	}
	if info.Status != StatusReady {
		t.Errorf("status must be lowercased, got %q", info.Status)
	}
	if !info.MountIs("vscode-remote", "git-dir", "/home/user@example.com/projects/chisel/.git/") {
		t.Error("trailing slash must not matter")
	}
}

func TestParseInfoGarbage(t *testing.T) {
	_, err := ParseInfo("\tthis: is: not\n  valid yaml: [\n")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "raw output") {
		t.Errorf("error must include the raw output, got %v", err)
	}
}

func TestParseInfoUnrecognised(t *testing.T) {
	if _, err := ParseInfo("some: thing\n"); err == nil {
		t.Fatal("expected an error for output without name/status")
	}
}

func TestParseList(t *testing.T) {
	const out = `chisel-dev  Ready    -
other-dev   Off      -
`
	entries, err := ParseList(out)
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %d entries: %v", len(entries), entries)
	}
	if entries[0].Workshop != "chisel-dev" || entries[0].Status != "Ready" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
	if entries[1].Status != "Off" {
		t.Errorf("entry 1 = %+v", entries[1])
	}
}

func TestParseListWithHeaders(t *testing.T) {
	const out = `WORKSHOP    STATUS  NOTES
chisel-dev  Ready   -
`
	entries, err := ParseList(out)
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	if len(entries) != 1 || entries[0].Workshop != "chisel-dev" {
		t.Fatalf("got %+v", entries)
	}
}

func TestParseListGlobal(t *testing.T) {
	const out = `PROJECT     WORKSHOP  STATUS  NOTES
~/original  dev       Ready   -
~/resolved  dev       Ready   -
`
	entries, err := ParseList(out)
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("got %+v", entries)
	}
	if entries[0].Project != "~/original" || entries[0].Workshop != "dev" {
		t.Errorf("entry 0 = %+v", entries[0])
	}
}

func TestParseListEmpty(t *testing.T) {
	entries, err := ParseList("\n")
	if err != nil {
		t.Fatalf("ParseList: %v", err)
	}
	if len(entries) != 0 {
		t.Fatalf("got %+v", entries)
	}
	if _, _, err := FindStatus(entries, ""); err == nil {
		t.Fatal("FindStatus must fail on an empty list")
	}
}

func TestFindStatus(t *testing.T) {
	entries := []ListEntry{{Workshop: "a", Status: "Ready"}, {Workshop: "b", Status: "Off"}}

	if _, _, err := FindStatus(entries, ""); err == nil {
		t.Error("an empty name with several workshops must fail")
	}
	name, status, err := FindStatus(entries, "b")
	if err != nil || name != "b" || status != StatusOff {
		t.Errorf("got %q %q %v", name, status, err)
	}
	if _, _, err := FindStatus(entries, "zz"); err == nil {
		t.Error("unknown name must fail")
	}
	name, status, err = FindStatus(entries[:1], "")
	if err != nil || name != "a" || status != StatusReady {
		t.Errorf("single workshop: got %q %q %v", name, status, err)
	}
}

func TestSamePath(t *testing.T) {
	home, _ := os.UserHomeDir()
	cases := []struct {
		a, b string
		want bool
	}{
		{"/a/b", "/a/b", true},
		{"/a/b/", "/a/b", true},
		{"/a/./b", "/a/b", true},
		{"/a/c/../b", "/a/b", true},
		{"~/x/.git", filepath.Join(home, "x/.git"), true},
		{"/a/b", "/a/c", false},
		{"", "/a/b", false},
		{"/a/b", "  ", false},
	}
	for _, c := range cases {
		if got := SamePath(c.a, c.b); got != c.want {
			t.Errorf("SamePath(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

func TestSplitColumnsKeepsSingleSpaces(t *testing.T) {
	got := splitColumns("chisel-dev  Ready  needs a refresh")
	want := []string{"chisel-dev", "Ready", "needs a refresh"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("got %q, want %q", got, want)
		}
	}
}
