package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// moveDefinition replaces the committed root workshop.yaml with one at rel.
func moveDefinition(t *testing.T, h *harness, rel, body string) {
	t.Helper()
	gitInRepo(t, h.repo, "rm", "-q", "workshop.yaml")
	p := filepath.Join(h.repo, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	gitInRepo(t, h.repo, "add", "-A")
	gitInRepo(t, h.repo, "commit", "-m", "move definition to "+rel)
}

const dirDef = `name: tools-dev
base: ubuntu@24.04
sdks:
  - name: vscode-remote # Standard tool for agentic work.
`

// TestUpUsesHiddenRootDefinition: .workshop.yaml is a documented location and
// must be patched in place, not shadowed by a new workshop.yaml.
func TestUpUsesHiddenRootDefinition(t *testing.T) {
	h := newHarness(t)
	moveDefinition(t, h, ".workshop.yaml", dirDef)

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	assertPatched(t, filepath.Join(h.worktree, ".workshop.yaml"), h.common)
	assertAbsent(t, filepath.Join(h.worktree, "workshop.yaml"))
}

// TestUpUsesDirDefinition: .workshop/<name>.yaml must be patched in place.
func TestUpUsesDirDefinition(t *testing.T) {
	h := newHarness(t)
	moveDefinition(t, h, ".workshop/tools-dev.yaml", dirDef)

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	assertPatched(t, filepath.Join(h.worktree, ".workshop", "tools-dev.yaml"), h.common)
	assertAbsent(t, filepath.Join(h.worktree, "workshop.yaml"))
}

// TestUpPicksProjectNamedDefinition: with several definitions under .workshop/,
// only the one named after the project is touched.
func TestUpPicksProjectNamedDefinition(t *testing.T) {
	h := newHarness(t)
	gitInRepo(t, h.repo, "rm", "-q", "workshop.yaml")
	for rel, body := range map[string]string{
		".workshop/tools-dev.yaml": dirDef,
		".workshop/golang.yaml":    "name: golang\nbase: ubuntu@24.04\nsdks:\n  - name: go\n",
		".workshop/notebook.yaml":  "name: notebook\nbase: ubuntu@24.04\nsdks:\n  - name: vscode-remote\n",
	} {
		p := filepath.Join(h.repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitInRepo(t, h.repo, "add", "-A")
	gitInRepo(t, h.repo, "commit", "-m", "several definitions")

	if code := h.up(); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	assertPatched(t, filepath.Join(h.worktree, ".workshop", "tools-dev.yaml"), h.common)

	// The unrelated definitions must be untouched.
	for _, rel := range []string{"golang.yaml", "notebook.yaml"} {
		body := readFile(t, filepath.Join(h.worktree, ".workshop", rel))
		if strings.Contains(body, "git-dir") {
			t.Errorf(".workshop/%s was modified:\n%s", rel, body)
		}
	}
}

// TestUpAmbiguousDefinitionsRefuse: no project-named definition and no flag must
// fail clearly instead of guessing.
func TestUpAmbiguousDefinitionsRefuse(t *testing.T) {
	h := newHarness(t)
	gitInRepo(t, h.repo, "rm", "-q", "workshop.yaml")
	for rel, body := range map[string]string{
		".workshop/golang.yaml":   "name: golang\nbase: ubuntu@24.04\nsdks:\n  - name: go\n",
		".workshop/notebook.yaml": "name: notebook\nbase: ubuntu@24.04\nsdks:\n  - name: vscode-remote\n",
	} {
		p := filepath.Join(h.repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitInRepo(t, h.repo, "add", "-A")
	gitInRepo(t, h.repo, "commit", "-m", "ambiguous definitions")

	if code := h.up(); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	// Nothing may have been created or patched.
	assertAbsent(t, filepath.Join(h.worktree, "workshop.yaml"))
	for _, rel := range []string{"golang.yaml", "notebook.yaml"} {
		if strings.Contains(readFile(t, filepath.Join(h.worktree, ".workshop", rel)), "git-dir") {
			t.Errorf(".workshop/%s was modified", rel)
		}
	}
}

// TestUpDefinitionFlagSelectsFile: --definition resolves an otherwise ambiguous
// layout.
func TestUpDefinitionFlagSelectsFile(t *testing.T) {
	h := newHarness(t)
	gitInRepo(t, h.repo, "rm", "-q", "workshop.yaml")
	for rel, body := range map[string]string{
		".workshop/golang.yaml":   "name: golang\nbase: ubuntu@24.04\nsdks:\n  - name: vscode-remote\n",
		".workshop/notebook.yaml": "name: notebook\nbase: ubuntu@24.04\nsdks:\n  - name: vscode-remote\n",
	} {
		p := filepath.Join(h.repo, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitInRepo(t, h.repo, "add", "-A")
	gitInRepo(t, h.repo, "commit", "-m", "ambiguous definitions")
	t.Setenv("WT_STUB_NAME", "golang")

	if code := h.up("--definition", ".workshop/golang.yaml"); code != exitOK {
		t.Fatalf("exit = %d", code)
	}
	assertPatched(t, filepath.Join(h.worktree, ".workshop", "golang.yaml"), h.common)
	if strings.Contains(readFile(t, filepath.Join(h.worktree, ".workshop", "notebook.yaml")), "git-dir") {
		t.Error("notebook.yaml was modified")
	}
}

// TestUpDefinitionFlagMissingFileFails: a typo must not create a file.
func TestUpDefinitionFlagMissingFileFails(t *testing.T) {
	h := newHarness(t)
	if code := h.up("--definition", ".workshop/typo.yaml"); code != exitError {
		t.Fatalf("exit = %d, want %d", code, exitError)
	}
	assertAbsent(t, filepath.Join(h.worktree, ".workshop", "typo.yaml"))
}

func assertPatched(t *testing.T, path, common string) {
	t.Helper()
	body := readFile(t, path)
	for _, want := range []string{"interface: mount", "git-dir", common} {
		if !strings.Contains(body, want) {
			t.Errorf("%s missing %q:\n%s", path, want, body)
		}
	}
}

func assertAbsent(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err == nil {
		t.Errorf("%s should not exist", path)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("cannot read %s: %v", path, err)
	}
	return string(b)
}
