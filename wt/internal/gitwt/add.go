package gitwt

import (
	"fmt"
	"path/filepath"
)

// Mutator runs a command for its side effects, streaming diagnostics.
type Mutator interface {
	Run(dir string, name string, args ...string) error
}

// GitRunner combines the read and write halves needed by this package.
type GitRunner interface {
	Runner
	Mutator
}

// BranchExists reports whether branch exists locally in the repository at dir.
func BranchExists(r Runner, dir, branch string) bool {
	_, err := r.Output(dir, "git", "rev-parse", "--verify", "--quiet", "refs/heads/"+branch)
	return err == nil
}

// EnsureWorktree makes worktreeDir a linked worktree of the repository whose
// shared git directory is gitCommonDir (design §5.3 step 2).
//
// When worktreeDir already exists it is verified to belong to the same
// repository and left alone; a branch mismatch is reported through mismatch
// rather than corrected. When it does not exist, the worktree is created,
// creating the branch too when it does not exist yet.
//
// created reports whether a new worktree was added.
func EnsureWorktree(r GitRunner, repoDir, gitCommonDir, worktreeDir, branch, from string) (created bool, mismatch string, err error) {
	worktreeDir = filepath.Clean(worktreeDir)

	if DirExists(worktreeDir) {
		got, cerr := CommonDir(r, worktreeDir)
		if cerr != nil {
			return false, "", fmt.Errorf(
				"%s exists but is not a git worktree; refusing to touch it: %w", worktreeDir, cerr,
			)
		}
		if got != filepath.Clean(gitCommonDir) {
			return false, "", fmt.Errorf(
				"%s is a worktree of a different repository (%s, expected %s); refusing to touch it",
				worktreeDir, got, gitCommonDir,
			)
		}
		if branch != "" {
			if cur, berr := CurrentBranch(r, worktreeDir); berr == nil && cur != "" && cur != branch {
				mismatch = cur
			}
		}
		return false, mismatch, nil
	}

	if branch == "" {
		return false, "", fmt.Errorf("a branch name is required to create %s", worktreeDir)
	}

	args := []string{"worktree", "add"}
	if BranchExists(r, repoDir, branch) {
		args = append(args, worktreeDir, branch)
	} else {
		args = append(args, "-b", branch, worktreeDir)
		if from != "" {
			args = append(args, from)
		}
	}
	if err := r.Run(repoDir, "git", args...); err != nil {
		return false, "", fmt.Errorf("git worktree add failed: %w", err)
	}
	return true, "", nil
}
