package wsdef

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestBootstrapCreatesUsableDefinition asserts the generated file is not just
// present but immediately patchable with the default sdk/plug — the whole point
// of bootstrapping.
func TestBootstrapCreatesUsableDefinition(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workshop.yaml")

	created, err := Bootstrap(path, "chisel")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if !created {
		t.Fatal("expected the file to be created")
	}

	got := read(t, path)
	want := "name: chisel-dev\nbase: ubuntu@24.04\nsdks:\n  - name: vscode-remote\n"
	if got != want {
		t.Errorf("content =\n%q\nwant\n%q", got, want)
	}

	f, err := Load(path)
	if err != nil {
		t.Fatalf("the bootstrapped file must parse: %v", err)
	}
	if f.Name() != "chisel-dev" {
		t.Errorf("Name = %q", f.Name())
	}
	changed, err := f.EnsureMountPlug(DefaultSDK, "git-dir", "/home/u/projects/chisel/.git")
	if err != nil {
		t.Fatalf("the bootstrapped file must be patchable with the default sdk: %v", err)
	}
	if !changed {
		t.Error("patching a fresh definition must report a change")
	}
	if err := f.Write(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, path), "interface: mount") {
		t.Errorf("patch not applied:\n%s", read(t, path))
	}
}

// TestBootstrapNeverOverwrites is the safety property: a hand-written definition
// must survive untouched.
func TestBootstrapNeverOverwrites(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "workshop.yaml")
	const existing = "name: mine\nbase: ubuntu@22.04\nsdks:\n  - name: go\n"
	if err := os.WriteFile(path, []byte(existing), 0o644); err != nil {
		t.Fatal(err)
	}

	created, err := Bootstrap(path, "chisel")
	if err != nil {
		t.Fatalf("Bootstrap: %v", err)
	}
	if created {
		t.Error("must not report creation for an existing file")
	}
	if got := read(t, path); got != existing {
		t.Errorf("existing file was modified:\n%s", got)
	}
}

func TestTemplateUsesProjectName(t *testing.T) {
	if got := string(Template("my-proj")); !strings.HasPrefix(got, "name: my-proj-dev\n") {
		t.Errorf("template = %q", got)
	}
}

func TestBootstrapUnwritableDirectory(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "ro")
	if err := os.Mkdir(sub, 0o500); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(sub, 0o700) })

	if _, err := Bootstrap(filepath.Join(sub, "workshop.yaml"), "x"); err == nil {
		t.Skip("running as a user that can write to a 0500 directory (root?)")
	}
}
