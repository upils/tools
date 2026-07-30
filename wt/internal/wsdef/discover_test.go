package wsdef

import (
	"errors"
	"os"
	"path/filepath"
	"testing"
)

// write creates a file with the given project-relative path.
func write(t *testing.T, dir, rel, body string) string {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestDiscoverFindsAllDocumentedLocations(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "workshop.yaml", "name: a\n")
	write(t, dir, ".workshop.yaml", "name: b\n")
	write(t, dir, ".workshop/zeta.yaml", "name: zeta\n")
	write(t, dir, ".workshop/alpha.yaml", "name: alpha\n")
	// Noise that must be ignored: a non-yaml file and an in-project SDK dir.
	write(t, dir, ".workshop/notes.txt", "hi\n")
	if err := os.MkdirAll(filepath.Join(dir, ".workshop", "cache"), 0o755); err != nil {
		t.Fatal(err)
	}

	found, err := Discover(dir)
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	var rels []string
	for _, c := range found {
		rels = append(rels, c.Rel)
	}
	want := []string{
		"workshop.yaml",
		".workshop.yaml",
		filepath.Join(".workshop", "alpha.yaml"), // sorted by name
		filepath.Join(".workshop", "zeta.yaml"),
	}
	if len(rels) != len(want) {
		t.Fatalf("got %v, want %v", rels, want)
	}
	for i := range want {
		if rels[i] != want[i] {
			t.Fatalf("got %v, want %v", rels, want)
		}
	}
	// Names come from the filename only for files under .workshop/.
	if found[0].Name != "" || found[0].InDir {
		t.Errorf("root candidate = %+v", found[0])
	}
	if found[2].Name != "alpha" || !found[2].InDir {
		t.Errorf("dir candidate = %+v", found[2])
	}
}

func TestDiscoverEmptyProject(t *testing.T) {
	found, err := Discover(t.TempDir())
	if err != nil {
		t.Fatalf("Discover: %v", err)
	}
	if len(found) != 0 {
		t.Fatalf("got %+v", found)
	}
}

// TestSelectBootstrapsOnlyWhenNoneFound is the core of the request: a new file
// is created only if no definition exists in any documented location.
func TestSelectBootstrapsOnlyWhenNoneFound(t *testing.T) {
	t.Run("none found", func(t *testing.T) {
		dir := t.TempDir()
		sel, err := Select(dir, "chisel", "", "")
		if err != nil {
			t.Fatal(err)
		}
		if !sel.Bootstrap {
			t.Error("expected Bootstrap")
		}
		if sel.Rel != RootName {
			t.Errorf("Rel = %q, want %q", sel.Rel, RootName)
		}
	})

	// Each existing location must suppress bootstrapping.
	for _, rel := range []string{"workshop.yaml", ".workshop.yaml", ".workshop/chisel-dev.yaml"} {
		t.Run("existing "+rel, func(t *testing.T) {
			dir := t.TempDir()
			write(t, dir, rel, "name: x\n")
			sel, err := Select(dir, "chisel", "", "")
			if err != nil {
				t.Fatal(err)
			}
			if sel.Bootstrap {
				t.Errorf("must not bootstrap when %s exists", rel)
			}
			if sel.Rel != filepath.FromSlash(rel) {
				t.Errorf("Rel = %q, want %q", sel.Rel, rel)
			}
		})
	}
}

// TestSelectPrefersProjectNamedDefinition covers the requested rule: with
// several definitions under .workshop/, only the one named after the project is
// edited.
func TestSelectPrefersProjectNamedDefinition(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".workshop/golang.yaml", "name: golang\n")
	write(t, dir, ".workshop/chisel-dev.yaml", "name: chisel-dev\n")
	write(t, dir, ".workshop/notebook.yaml", "name: notebook\n")

	sel, err := Select(dir, "chisel", "", "")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Name != "chisel-dev" {
		t.Errorf("Name = %q, want chisel-dev", sel.Name)
	}
	if sel.Bootstrap {
		t.Error("must not bootstrap")
	}
}

// A bare <project>.yaml is accepted as a fallback when <project>-dev.yaml is absent.
func TestSelectFallsBackToBareProjectName(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".workshop/golang.yaml", "name: golang\n")
	write(t, dir, ".workshop/chisel.yaml", "name: chisel\n")

	sel, err := Select(dir, "chisel", "", "")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Name != "chisel" {
		t.Errorf("Name = %q", sel.Name)
	}
}

// TestSelectWorkshopFlagWins: an explicit --workshop selects the definition.
func TestSelectWorkshopFlagWins(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".workshop/chisel-dev.yaml", "name: chisel-dev\n")
	write(t, dir, ".workshop/notebook.yaml", "name: notebook\n")

	sel, err := Select(dir, "chisel", "", "notebook")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Name != "notebook" {
		t.Errorf("Name = %q, want notebook", sel.Name)
	}
}

// TestSelectAmbiguousRefuses: several definitions and no way to choose must fail
// clearly rather than editing an arbitrary one.
func TestSelectAmbiguousRefuses(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".workshop/golang.yaml", "name: golang\n")
	write(t, dir, ".workshop/notebook.yaml", "name: notebook\n")

	_, err := Select(dir, "chisel", "", "")
	var amb *ErrAmbiguous
	if !errors.As(err, &amb) {
		t.Fatalf("expected ErrAmbiguous, got %v", err)
	}
	for _, want := range []string{"golang.yaml", "notebook.yaml", "--definition"} {
		if !contains(err.Error(), want) {
			t.Errorf("error must mention %q: %v", want, err)
		}
	}
	// It must not have silently picked one.
	if amb.Candidates == nil {
		t.Error("candidates not reported")
	}
}

// A root definition alongside .workshop/ entries is also ambiguous unless one is
// named after the project.
func TestSelectRootPlusDirIsAmbiguous(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "workshop.yaml", "name: a\n")
	write(t, dir, ".workshop/other.yaml", "name: other\n")

	if _, err := Select(dir, "chisel", "", ""); err == nil {
		t.Fatal("expected an ambiguity error")
	}
}

func TestSelectExplicitDefinition(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".workshop/golang.yaml", "name: golang\n")
	write(t, dir, ".workshop/notebook.yaml", "name: notebook\n")

	sel, err := Select(dir, "chisel", ".workshop/golang.yaml", "")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	if sel.Name != "golang" {
		t.Errorf("Name = %q", sel.Name)
	}
	if sel.Bootstrap {
		t.Error("explicit selection must not bootstrap")
	}
}

// An explicit --definition that does not exist must fail, not be created: the
// user asked for a specific file and a typo should not silently make a new one.
func TestSelectExplicitMissingFails(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "workshop.yaml", "name: a\n")

	_, err := Select(dir, "chisel", ".workshop/typo.yaml", "")
	if err == nil {
		t.Fatal("expected an error for a missing --definition target")
	}
	if !contains(err.Error(), "--definition") {
		t.Errorf("error should explain the flag: %v", err)
	}
}

func TestSelectExplicitRootFile(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, ".workshop.yaml", "name: a\n")

	sel, err := Select(dir, "chisel", ".workshop.yaml", "")
	if err != nil {
		t.Fatalf("Select: %v", err)
	}
	// A root definition's name is not implied by its filename.
	if sel.Name != "" {
		t.Errorf("Name = %q, want empty for a root definition", sel.Name)
	}
}

// A directory named workshop.yaml must not be mistaken for a definition.
func TestDiscoverIgnoresDirectories(t *testing.T) {
	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "workshop.yaml"), 0o755); err != nil {
		t.Fatal(err)
	}
	found, err := Discover(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(found) != 0 {
		t.Fatalf("got %+v", found)
	}
	// ...and it should therefore bootstrap, but Bootstrap's O_EXCL will fail
	// safely rather than clobber the directory.
	sel, err := Select(dir, "chisel", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !sel.Bootstrap {
		t.Error("expected Bootstrap")
	}
	if _, err := Bootstrap(sel.Path, "chisel"); err == nil {
		t.Error("Bootstrap must fail rather than replace a directory")
	}
}

func contains(hay, needle string) bool {
	return len(hay) >= len(needle) && func() bool {
		for i := 0; i+len(needle) <= len(hay); i++ {
			if hay[i:i+len(needle)] == needle {
				return true
			}
		}
		return false
	}()
}
