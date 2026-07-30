package ws

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// clientOnStub returns a Client whose Bin is a script recording every argv it is
// called with, one per line, in the returned file. It exercises the argv contract
// of D4 without a real `workshop`.
//
// dir is created, because it is the child's CWD (D4) and exec would fail on a
// missing one.
func clientOnStub(t *testing.T, dir, stdout string) (*Client, func() []string) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(t.TempDir(), "argv")
	bin := script(t, `echo "$@" >>`+logPath+`
cat <<'EOF'
`+stdout+`
EOF`)
	c := &Client{Exec: &Exec{}, Dir: dir, Bin: bin}
	return c, func() []string {
		b, err := os.ReadFile(logPath)
		if err != nil {
			return nil
		}
		var out []string
		for _, l := range strings.Split(strings.TrimSpace(string(b)), "\n") {
			if l != "" {
				out = append(out, l)
			}
		}
		return out
	}
}

// TestLaunchGetsNoProjectFlag is D4's whole point: `launch` is excluded from
// workshop's requireProject, so passing -p to it is an error; every other
// subcommand must carry it.
func TestLaunchGetsNoProjectFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tools-worktrees", "feature")
	c, argv := clientOnStub(t, dir, "")

	if err := c.Launch("tools-dev"); err != nil {
		t.Fatal(err)
	}
	got := argv()
	if len(got) != 1 {
		t.Fatalf("argv = %v", got)
	}
	if got[0] != "launch tools-dev" {
		t.Errorf("launch argv = %q, want %q", got[0], "launch tools-dev")
	}
	if strings.Contains(got[0], "-p") {
		t.Errorf("launch must not receive -p (D4): %q", got[0])
	}
}

func TestProjectScopedSubcommands(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tools-worktrees", "feature")
	tests := []struct {
		name string
		call func(*Client) error
		want string
	}{
		{
			"start", func(c *Client) error { return c.Start("tools-dev") },
			"start tools-dev -p " + dir,
		},
		{
			"stop", func(c *Client) error { return c.Stop("tools-dev") },
			"stop tools-dev -p " + dir,
		},
		{
			"refresh", func(c *Client) error { return c.Refresh() },
			"refresh -p " + dir,
		},
		{
			"remount", func(c *Client) error {
				return c.Remount("tools-dev", "vscode-remote", "git-dir", "/projects/tools/.git")
			},
			"remount tools-dev/vscode-remote:git-dir /projects/tools/.git -p " + dir,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c, argv := clientOnStub(t, dir, "")
			if err := tc.call(c); err != nil {
				t.Fatal(err)
			}
			got := argv()
			if len(got) != 1 || got[0] != tc.want {
				t.Errorf("argv = %v, want [%q]", got, tc.want)
			}
		})
	}
}

// TestStartStopOmitNameWhenUnknown: workshop allows omitting the name when the
// project defines exactly one, and an empty string must not become an argument.
func TestStartStopOmitNameWhenUnknown(t *testing.T) {
	dir := t.TempDir()
	c, argv := clientOnStub(t, dir, "")
	if err := c.Start(""); err != nil {
		t.Fatal(err)
	}
	if err := c.Stop(""); err != nil {
		t.Fatal(err)
	}
	if err := c.Launch(""); err != nil {
		t.Fatal(err)
	}
	want := []string{"start -p " + dir, "stop -p " + dir, "launch"}
	got := argv()
	if len(got) != len(want) {
		t.Fatalf("argv = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("argv[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}

// TestRemountRefusesEmptyName: the mount address is <name>/<sdk>:<plug>, so an
// empty name would silently address the wrong thing. Fail instead (C1).
func TestRemountRefusesEmptyName(t *testing.T) {
	c, argv := clientOnStub(t, t.TempDir(), "")
	if err := c.Remount("", "vscode-remote", "git-dir", "/projects/x/.git"); err == nil {
		t.Fatal("expected an error for an empty workshop name")
	}
	if got := argv(); len(got) != 0 {
		t.Errorf("a command was issued despite the error: %v", got)
	}
}

// TestRemountStripsTrailingSlash pins D15: the source must carry the same string
// as the workshop-target, which never has a trailing slash.
func TestRemountStripsTrailingSlash(t *testing.T) {
	c, argv := clientOnStub(t, t.TempDir(), "")
	if err := c.Remount("x-dev", "vscode-remote", "git-dir", "/projects/x/.git/"); err != nil {
		t.Fatal(err)
	}
	got := argv()
	if len(got) != 1 || !strings.Contains(got[0], " /projects/x/.git -p ") {
		t.Errorf("trailing slash not stripped: %v", got)
	}
}

// TestBinDefaultsToWorkshop guards the default of the overridable Bin field.
func TestBinDefaultsToWorkshop(t *testing.T) {
	if got := (&Client{}).bin(); got != "workshop" {
		t.Errorf("bin() = %q, want %q", got, "workshop")
	}
	if got := (&Client{Bin: "other"}).bin(); got != "other" {
		t.Errorf("bin() = %q, want %q", got, "other")
	}
}

// TestInfoParsesAndCarriesProjectFlag ties the adapter to the parser: `info` is
// project-scoped and its stdout is what feeds the idempotency oracle (D7, D9).
func TestInfoParsesAndCarriesProjectFlag(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "tools-worktrees", "feature")
	c, argv := clientOnStub(t, dir, `name:      tools-dev
hostname:  tools-dev.feature.wp
status:    Ready
project:   `+dir+`
sdks:
  vscode-remote:
    mounts:
      git-dir:
        host-source:     /projects/tools/.git
        workshop-target: /projects/tools/.git`)

	info, err := c.Info("tools-dev")
	if err != nil {
		t.Fatal(err)
	}
	if got := argv(); len(got) != 1 || got[0] != "info tools-dev -p "+dir {
		t.Errorf("argv = %v", got)
	}
	if info.Status != StatusReady {
		t.Errorf("status = %q, want %q (must be lowercased)", info.Status, StatusReady)
	}
	if !info.MountIs("vscode-remote", "git-dir", "/projects/tools/.git") {
		t.Error("the mount was not recognised as correct")
	}
}

// TestStatusUsesList pins D7: status comes from `list`, which knows Off, and not
// from `info`, which errors for a workshop that does not exist yet.
func TestStatusUsesList(t *testing.T) {
	dir := t.TempDir()
	c, argv := clientOnStub(t, dir, "tools-dev  Off  --")

	name, status, err := c.Status("")
	if err != nil {
		t.Fatal(err)
	}
	if name != "tools-dev" || status != StatusOff {
		t.Errorf("Status() = %q, %q; want tools-dev, off", name, status)
	}
	if got := argv(); len(got) != 1 || got[0] != "list --no-headers -p "+dir {
		t.Errorf("argv = %v", got)
	}
}

// TestInfoFailureIsNotMistakenForAbsence guards D7's rationale: a failing `info`
// must surface as an error, never as a parsed zero value.
func TestInfoFailureIsNotMistakenForAbsence(t *testing.T) {
	bin := script(t, `echo "workshop: cannot connect to the daemon" >&2; exit 1`)
	c := &Client{Exec: &Exec{}, Dir: t.TempDir(), Bin: bin}
	info, err := c.Info("tools-dev")
	if err == nil {
		t.Fatal("expected an error")
	}
	if info != nil {
		t.Errorf("info = %+v, want nil on failure", info)
	}
	if !strings.Contains(err.Error(), "cannot connect to the daemon") {
		t.Errorf("error lost the child's stderr: %v", err)
	}
}
