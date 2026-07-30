// Package gitwt wraps the git plumbing needed to discover and create linked
// worktrees (design §5.2, §2.5).
package gitwt

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Runner executes a command and returns its trimmed stdout.
//
// It is an interface so that tests can drive the package without a real git
// binary, and so that the single exec helper of §5.5 can be reused.
type Runner interface {
	Output(dir string, name string, args ...string) (string, error)
}

// Layout is the set of paths derived from a repository (design §5.2).
type Layout struct {
	// GitCommonDir is the shared .git directory of the repository, absolute
	// and cleaned. It is both the workshop-target and the remount source.
	GitCommonDir string
	// MainRoot is the working tree of the main worktree, i.e. the parent of
	// GitCommonDir.
	MainRoot string
	// ProjectName is the base name of MainRoot.
	ProjectName string
	// WorktreesRoot is "<siblings>/<project>-worktrees".
	WorktreesRoot string
	// Branch is the branch/worktree name the layout was computed for. Empty
	// when no branch was requested.
	Branch string
	// WorktreeDir is the directory of the linked worktree for Branch.
	WorktreeDir string
}

// CommonDir returns the absolute shared .git directory for the repository
// containing dir. It works from the main worktree and from any linked worktree
// (§2.5).
func CommonDir(r Runner, dir string) (string, error) {
	out, err := r.Output(dir, "git", "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", fmt.Errorf("not a git repository: %s: %w", dir, err)
	}
	out = strings.TrimSpace(out)
	if out == "" {
		return "", fmt.Errorf("git returned an empty --git-common-dir for %s", dir)
	}
	return filepath.Clean(out), nil
}

// SanitizeBranch maps a branch name to a single path segment. Branch names may
// contain "/" (e.g. feat/x), which would otherwise create nested directories or
// collide (risk R7).
func SanitizeBranch(branch string) string {
	return strings.ReplaceAll(branch, "/", "-")
}

// LayoutFromCommonDir derives the layout for branch from an already-resolved
// common dir (§5.2). It is pure, which is what makes the derivation
// unit-testable.
//
// A "-worktrees" sibling directory of the main worktree is used, and the branch
// name is flattened to one segment. It refuses repositories whose common dir is
// not named ".git" (bare repositories, core.worktree oddities) rather than
// guessing; `--worktree` is the documented override for those.
func LayoutFromCommonDir(common, branch string) (Layout, error) {
	common = filepath.Clean(common)
	if filepath.Base(common) != ".git" {
		return Layout{}, fmt.Errorf(
			"unsupported repository layout: git common dir %q is not named .git "+
				"(bare repository?); pass --worktree to override", common,
		)
	}
	mainRoot := filepath.Dir(common)
	name := filepath.Base(mainRoot)
	if name == "" || name == "." || name == string(filepath.Separator) {
		return Layout{}, fmt.Errorf("cannot derive a project name from %q", mainRoot)
	}
	l := Layout{
		GitCommonDir:  common,
		MainRoot:      mainRoot,
		ProjectName:   name,
		WorktreesRoot: filepath.Join(filepath.Dir(mainRoot), name+"-worktrees"),
		Branch:        branch,
	}
	if branch != "" {
		l.WorktreeDir = filepath.Join(l.WorktreesRoot, SanitizeBranch(branch))
	}
	return l, nil
}

// Override carries the user's explicit choices, which win over derivation
// (§5.1: --worktree, and the branch positional).
type Override struct {
	// WorktreeDir, when set, replaces the derived worktree directory.
	WorktreeDir string
	// Branch is the branch the layout is for. It may be empty when the worktree
	// is given explicitly or was discovered from the CWD.
	Branch string
}

// Resolve produces the final layout for a run: the derivation of §5.2 with the
// user's overrides applied. It is pure, so the precedence rules are testable
// without a repository.
//
// An explicit WorktreeDir also relaxes the ".git" check: a bare repository or a
// core.worktree oddity cannot be derived from, and --worktree is precisely the
// documented escape hatch for it. The rest of the layout is still filled in
// best-effort, because ProjectName is needed to name the workshop definition
// (D19) even when the worktree path came from the user.
func Resolve(common string, ov Override) (Layout, error) {
	if ov.WorktreeDir == "" {
		return LayoutFromCommonDir(common, ov.Branch)
	}

	l, err := LayoutFromCommonDir(common, ov.Branch)
	if err != nil {
		// The layout is not derivable (a bare repository, say), but the project
		// name still is: for "<name>.git" it is "<name>". MainRoot and
		// WorktreesRoot are left empty rather than guessed, because such a
		// repository has no main working tree to be the parent of.
		clean := filepath.Clean(common)
		l = Layout{
			GitCommonDir: clean,
			ProjectName:  strings.TrimSuffix(filepath.Base(clean), ".git"),
			Branch:       ov.Branch,
		}
	}
	l.WorktreeDir = filepath.Clean(ov.WorktreeDir)
	return l, nil
}

// CurrentWorktree returns the top-level directory of the worktree containing
// dir, and whether that worktree is a linked one (as opposed to the main
// worktree).
func CurrentWorktree(r Runner, dir string) (root string, linked bool, err error) {
	out, err := r.Output(dir, "git", "rev-parse", "--show-toplevel")
	if err != nil {
		return "", false, fmt.Errorf("not inside a git worktree: %s: %w", dir, err)
	}
	root = filepath.Clean(strings.TrimSpace(out))
	common, err := CommonDir(r, dir)
	if err != nil {
		return "", false, err
	}
	// In the main worktree, the common dir sits directly inside the top level.
	return root, filepath.Dir(common) != root, nil
}

// CurrentBranch returns the checked-out branch of the worktree containing dir,
// or "" when detached.
func CurrentBranch(r Runner, dir string) (string, error) {
	out, err := r.Output(dir, "git", "rev-parse", "--abbrev-ref", "HEAD")
	if err != nil {
		return "", err
	}
	b := strings.TrimSpace(out)
	if b == "HEAD" {
		return "", nil
	}
	return b, nil
}

// DirExists reports whether path exists and is a directory.
func DirExists(path string) bool {
	st, err := os.Stat(path)
	return err == nil && st.IsDir()
}
