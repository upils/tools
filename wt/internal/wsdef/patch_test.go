package wsdef

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// realDef mirrors the shape of the tracked workshop.yaml, comments included
// (design C5, §5.6).
const realDef = `name: chisel-dev
base: ubuntu@24.04
sdks:
  - name: go # Toolchain for the project.
    channel: "1.25"
  - name: vscode-remote # Standard tool for agentic work.
`

const target = "/home/user@example.com/projects/chisel/.git"

func load(t *testing.T, content string) (*File, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "workshop.yaml")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	f, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	return f, path
}

func TestEnsureMountPlugAddsPlug(t *testing.T) {
	f, path := load(t, realDef)
	changed, err := f.EnsureMountPlug("vscode-remote", "git-dir", target)
	if err != nil {
		t.Fatalf("EnsureMountPlug: %v", err)
	}
	if !changed {
		t.Fatal("expected a change")
	}
	if err := f.Write(); err != nil {
		t.Fatalf("Write: %v", err)
	}
	got := read(t, path)

	for _, want := range []string{
		"interface: mount",
		`workshop-target: "` + target + `"`,
		"git-dir:",
		"plugs:",
		"# Standard tool for agentic work.", // comments survive (C5)
		"# Toolchain for the project.",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("output missing %q:\n%s", want, got)
		}
	}
	// SDK ordering survives.
	if strings.Index(got, "name: go") > strings.Index(got, "name: vscode-remote") {
		t.Errorf("sdk order changed:\n%s", got)
	}
	// The plug must land under the right SDK, i.e. after vscode-remote.
	if strings.Index(got, "git-dir") < strings.Index(got, "vscode-remote") {
		t.Errorf("plug attached to the wrong sdk:\n%s", got)
	}
}

// TestEnsureMountPlugIdempotent is the key regression test: a second call must
// report no change and leave the file byte-identical (design §8).
func TestEnsureMountPlugIdempotent(t *testing.T) {
	f, path := load(t, realDef)
	if _, err := f.EnsureMountPlug("vscode-remote", "git-dir", target); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(); err != nil {
		t.Fatal(err)
	}
	first := read(t, path)

	f2, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	changed, err := f2.EnsureMountPlug("vscode-remote", "git-dir", target)
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("second call must report no change")
	}
	if err := f2.Write(); err != nil {
		t.Fatal(err)
	}
	if second := read(t, path); second != first {
		t.Errorf("rewrite is not byte-identical:\n--- first ---\n%s\n--- second ---\n%s", first, second)
	}
}

func TestEnsureMountPlugTrailingSlashIsNoChange(t *testing.T) {
	f, _ := load(t, defWithPlug(target))
	changed, err := f.EnsureMountPlug("vscode-remote", "git-dir", target+"/")
	if err != nil {
		t.Fatal(err)
	}
	if changed {
		t.Error("a trailing slash must normalise to no change (D15)")
	}
}

func TestEnsureMountPlugRetargets(t *testing.T) {
	f, path := load(t, defWithPlug("/home/u/projects/other/.git"))
	changed, err := f.EnsureMountPlug("vscode-remote", "git-dir", target)
	if err != nil {
		t.Fatal(err)
	}
	if !changed {
		t.Fatal("expected a change")
	}
	if err := f.Write(); err != nil {
		t.Fatal(err)
	}
	got := read(t, path)
	if !strings.Contains(got, target) {
		t.Errorf("target not updated:\n%s", got)
	}
	if strings.Contains(got, "other/.git") {
		t.Errorf("old target still present:\n%s", got)
	}
	// Sibling keys are preserved (§5.6).
	if !strings.Contains(got, "read-only: true") {
		t.Errorf("sibling key lost:\n%s", got)
	}
}

func TestEnsureMountPlugSDKMissing(t *testing.T) {
	f, _ := load(t, "name: x\nbase: ubuntu@24.04\nsdks:\n  - name: go\n")
	_, err := f.EnsureMountPlug("vscode-remote", "git-dir", target)
	if !errors.Is(err, ErrSDKNotFound) {
		t.Fatalf("expected ErrSDKNotFound, got %v", err)
	}
}

func TestEnsureMountPlugNoSDKsKey(t *testing.T) {
	f, _ := load(t, "name: x\nbase: ubuntu@24.04\n")
	if _, err := f.EnsureMountPlug("vscode-remote", "git-dir", target); err == nil {
		t.Fatal("expected an error when sdks is absent")
	}
}

func TestEnsureMountPlugWrongInterface(t *testing.T) {
	def := `name: x
base: ubuntu@24.04
sdks:
  - name: vscode-remote
    plugs:
      git-dir:
        interface: network
`
	f, _ := load(t, def)
	_, err := f.EnsureMountPlug("vscode-remote", "git-dir", target)
	if err == nil || !strings.Contains(err.Error(), "interface") {
		t.Fatalf("expected an interface error, got %v", err)
	}
}

func TestEnsureMountPlugRelativeTarget(t *testing.T) {
	f, _ := load(t, realDef)
	if _, err := f.EnsureMountPlug("vscode-remote", "git-dir", "relative/.git"); err == nil {
		t.Fatal("expected an error for a relative target")
	}
}

func TestNameAndSDKNames(t *testing.T) {
	f, _ := load(t, realDef)
	if f.Name() != "chisel-dev" {
		t.Errorf("Name = %q", f.Name())
	}
	names := f.SDKNames()
	if len(names) != 2 || names[0] != "go" || names[1] != "vscode-remote" {
		t.Errorf("SDKNames = %v", names)
	}
}

func TestWriteIsAtomicAndKeepsMode(t *testing.T) {
	f, path := load(t, realDef)
	if err := os.Chmod(path, 0o640); err != nil {
		t.Fatal(err)
	}
	if _, err := f.EnsureMountPlug("vscode-remote", "git-dir", target); err != nil {
		t.Fatal(err)
	}
	if err := f.Write(); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o640 {
		t.Errorf("mode = %v, want 0640", st.Mode().Perm())
	}
	// No temp file left behind.
	entries, err := os.ReadDir(filepath.Dir(path))
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if strings.Contains(e.Name(), ".tmp-") {
			t.Errorf("temp file left behind: %s", e.Name())
		}
	}
}

func defWithPlug(tgt string) string {
	return `name: chisel-dev
base: ubuntu@24.04
sdks:
  - name: vscode-remote # Standard tool for agentic work.
    plugs:
      git-dir:
        interface: mount
        read-only: true
        workshop-target: "` + tgt + `"
`
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}
