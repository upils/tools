package wsdef

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Definition file locations, per the workshop definition reference:
//
//   - a single workshop: workshop.yaml or .workshop.yaml in the project root;
//   - several workshops: .workshop/<NAME>.yaml, one per workshop, where <NAME>
//     must equal the workshop's `name` field.
const (
	// RootName is the conventional single-workshop definition.
	RootName = "workshop.yaml"
	// HiddenRootName is the accepted hidden alternative to RootName.
	HiddenRootName = ".workshop.yaml"
	// Dir holds one definition per workshop.
	Dir = ".workshop"
)

// Candidate is a definition file found in a project.
type Candidate struct {
	// Path is the absolute path to the file.
	Path string
	// Rel is the path relative to the project directory, for messages.
	Rel string
	// Name is the workshop name implied by the location: the filename stem for
	// files under .workshop/, empty for the root definitions (whose name is only
	// known after parsing).
	Name string
	// InDir reports whether the file lives under .workshop/.
	InDir bool
}

// Discover lists the definition files present in projectDir, in precedence
// order: the root definitions first, then .workshop/*.yaml sorted by name.
//
// Files are only reported if they are regular files, so a stray directory named
// workshop.yaml is ignored rather than causing a confusing parse error.
func Discover(projectDir string) ([]Candidate, error) {
	var found []Candidate

	for _, base := range []string{RootName, HiddenRootName} {
		p := filepath.Join(projectDir, base)
		if isRegular(p) {
			found = append(found, Candidate{Path: p, Rel: base})
		}
	}

	dir := filepath.Join(projectDir, Dir)
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, fmt.Errorf("cannot read %s: %w", dir, err)
	}
	var inDir []Candidate
	for _, e := range entries {
		name := e.Name()
		// Only .yaml files are definitions; .workshop/ also holds in-project SDK
		// directories, which must not be mistaken for workshops.
		if e.IsDir() || filepath.Ext(name) != ".yaml" {
			continue
		}
		p := filepath.Join(dir, name)
		if !isRegular(p) {
			continue
		}
		inDir = append(inDir, Candidate{
			Path:  p,
			Rel:   filepath.Join(Dir, name),
			Name:  strings.TrimSuffix(name, ".yaml"),
			InDir: true,
		})
	}
	sort.Slice(inDir, func(i, j int) bool { return inDir[i].Name < inDir[j].Name })

	return append(found, inDir...), nil
}

// ErrAmbiguous reports that several definitions exist and none could be singled
// out. It lists the candidates so the user can pick one.
type ErrAmbiguous struct {
	Candidates []Candidate
	ProjectDir string
}

func (e *ErrAmbiguous) Error() string {
	rels := make([]string, 0, len(e.Candidates))
	for _, c := range e.Candidates {
		rels = append(rels, c.Rel)
	}
	return fmt.Sprintf(
		"several workshop definitions found in %s (%s); "+
			"pick one with --definition <relative-path>, or name the workshop with --workshop",
		e.ProjectDir, strings.Join(rels, ", "),
	)
}

// Selection is the outcome of resolving which definition to edit.
type Selection struct {
	// Path is the absolute path of the definition to edit.
	Path string
	// Rel is that path relative to the project directory.
	Rel string
	// Name is the workshop name implied by the filename, or "" when it must be
	// read from the file.
	Name string
	// Bootstrap reports that no definition exists and Path must be created.
	Bootstrap bool
}

// Select decides which definition file to edit for a project.
//
// Precedence:
//
//  1. explicit — a project-relative path given by the user; it must exist.
//  2. exactly one definition — use it.
//  3. several — the one matching workshopName if given, else the one named after
//     the project (`<project>-dev`, then `<project>`); otherwise ErrAmbiguous.
//  4. none — bootstrap the conventional root definition.
//
// A definition is only ever created in case 4, so an existing project layout is
// never second-guessed.
func Select(projectDir, projectName, explicit, workshopName string) (*Selection, error) {
	if explicit != "" {
		p := explicit
		if !filepath.IsAbs(p) {
			p = filepath.Join(projectDir, p)
		}
		p = filepath.Clean(p)
		if !isRegular(p) {
			return nil, fmt.Errorf("no workshop definition at %s "+
				"(--definition takes a path relative to the worktree, and the file must exist)", p)
		}
		rel, err := filepath.Rel(projectDir, p)
		if err != nil {
			rel = p
		}
		return &Selection{Path: p, Rel: rel, Name: nameFromPath(projectDir, p)}, nil
	}

	found, err := Discover(projectDir)
	if err != nil {
		return nil, err
	}

	switch len(found) {
	case 0:
		return &Selection{
			Path:      filepath.Join(projectDir, RootName),
			Rel:       RootName,
			Bootstrap: true,
		}, nil
	case 1:
		return &Selection{Path: found[0].Path, Rel: found[0].Rel, Name: found[0].Name}, nil
	}

	// Several definitions: only edit the one that is unambiguously ours.
	for _, want := range preferredNames(projectName, workshopName) {
		for _, c := range found {
			if c.InDir && c.Name == want {
				return &Selection{Path: c.Path, Rel: c.Rel, Name: c.Name}, nil
			}
		}
	}
	return nil, &ErrAmbiguous{Candidates: found, ProjectDir: projectDir}
}

// preferredNames lists the workshop names to look for, most specific first. An
// explicit --workshop wins; otherwise the convention is "<project>-dev", with a
// bare "<project>" accepted as a fallback.
func preferredNames(projectName, workshopName string) []string {
	if workshopName != "" {
		return []string{workshopName}
	}
	if projectName == "" {
		return nil
	}
	return []string{projectName + "-dev", projectName}
}

// nameFromPath returns the workshop name implied by a path under .workshop/.
func nameFromPath(projectDir, path string) string {
	if filepath.Dir(path) != filepath.Join(projectDir, Dir) {
		return ""
	}
	if filepath.Ext(path) != ".yaml" {
		return ""
	}
	return strings.TrimSuffix(filepath.Base(path), ".yaml")
}

func isRegular(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.Mode().IsRegular()
}
