package gitwt

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// execRunner is the minimal real-git runner used by these tests; production code
// passes ws.Exec, which satisfies the same interfaces.
type execRunner struct{ t *testing.T }

func (e execRunner) Output(dir, name string, args ...string) (string, error) {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.Output()
	return string(out), err
}

func (e execRunner) Run(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
	out, err := cmd.CombinedOutput()
	if err != nil {
		e.t.Logf("%s %v: %s", name, args, out)
	}
	return err
}

// initRepo creates a repository with one commit at <tmp>/projects/<name>.
func initRepo(t *testing.T, name string) (repo string, r execRunner) {
	t.Helper()
	r = execRunner{t: t}
	base := t.TempDir()
	repo = filepath.Join(base, "projects", name)
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{
		{"init", "-b", "main"},
		{"config", "user.email", "t@example.com"},
		{"config", "user.name", "T"},
		{"commit", "--allow-empty", "-m", "initial"},
	} {
		if err := r.Run(repo, "git", args...); err != nil {
			t.Fatalf("git %v: %v", args, err)
		}
	}
	return repo, r
}

func TestCommonDirFromMainAndLinkedWorktree(t *testing.T) {
	repo, r := initRepo(t, "chisel")

	common, err := CommonDir(r, repo)
	if err != nil {
		t.Fatalf("CommonDir: %v", err)
	}
	if filepath.Base(common) != ".git" {
		t.Fatalf("common = %q", common)
	}

	layout, err := LayoutFromCommonDir(common, "feature")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureWorktree(r, repo, common, layout.WorktreeDir, "feature", ""); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}

	// §2.5: the common dir is identical seen from the linked worktree.
	fromLinked, err := CommonDir(r, layout.WorktreeDir)
	if err != nil {
		t.Fatalf("CommonDir from linked: %v", err)
	}
	if fromLinked != common {
		t.Errorf("common dir differs: %q vs %q", fromLinked, common)
	}

	// The linked worktree has a .git *file*, not a directory (§1.2).
	st, err := os.Stat(filepath.Join(layout.WorktreeDir, ".git"))
	if err != nil {
		t.Fatal(err)
	}
	if st.IsDir() {
		t.Error("linked worktree unexpectedly has a .git directory")
	}

	root, linked, err := CurrentWorktree(r, layout.WorktreeDir)
	if err != nil {
		t.Fatal(err)
	}
	if !linked || root != layout.WorktreeDir {
		t.Errorf("CurrentWorktree = %q, linked=%v", root, linked)
	}
	if root, linked, err = CurrentWorktree(r, repo); err != nil || linked {
		t.Errorf("main worktree reported as linked: %q %v %v", root, linked, err)
	}

	if b, err := CurrentBranch(r, layout.WorktreeDir); err != nil || b != "feature" {
		t.Errorf("CurrentBranch = %q, %v", b, err)
	}
}

func TestLayoutFromCommonDir(t *testing.T) {
	// A home directory containing "@" must round-trip (§8).
	common := "/home/user@example.com/projects/chisel/.git"
	l, err := LayoutFromCommonDir(common, "improve-workshop")
	if err != nil {
		t.Fatal(err)
	}
	if l.MainRoot != "/home/user@example.com/projects/chisel" {
		t.Errorf("MainRoot = %q", l.MainRoot)
	}
	if l.ProjectName != "chisel" {
		t.Errorf("ProjectName = %q", l.ProjectName)
	}
	want := "/home/user@example.com/projects/chisel-worktrees/improve-workshop"
	if l.WorktreeDir != want {
		t.Errorf("WorktreeDir = %q, want %q", l.WorktreeDir, want)
	}
}

func TestLayoutRejectsBareRepo(t *testing.T) {
	_, err := LayoutFromCommonDir("/home/u/projects/chisel.git", "x")
	if err == nil || !strings.Contains(err.Error(), "not named .git") {
		t.Fatalf("expected a bare-repo rejection, got %v", err)
	}
}

func TestLayoutFlattensBranchWithSlash(t *testing.T) {
	// Risk R7: "feat/x" must not create nested directories.
	l, err := LayoutFromCommonDir("/home/u/projects/chisel/.git", "feat/x")
	if err != nil {
		t.Fatal(err)
	}
	if l.WorktreeDir != "/home/u/projects/chisel-worktrees/feat-x" {
		t.Errorf("WorktreeDir = %q", l.WorktreeDir)
	}
}

func TestLayoutNoBranch(t *testing.T) {
	l, err := LayoutFromCommonDir("/home/u/projects/chisel/.git", "")
	if err != nil {
		t.Fatal(err)
	}
	if l.WorktreeDir != "" {
		t.Errorf("WorktreeDir = %q, want empty", l.WorktreeDir)
	}
	if l.WorktreesRoot != "/home/u/projects/chisel-worktrees" {
		t.Errorf("WorktreesRoot = %q", l.WorktreesRoot)
	}
}

// TestResolvePrecedence covers the override rules of §5.1 that used to live
// inline in cmd/wt and were therefore untestable.
func TestResolvePrecedence(t *testing.T) {
	const common = "/home/u/projects/chisel/.git"

	tests := []struct {
		name         string
		common       string
		ov           Override
		wantWorktree string
		wantProject  string
		wantErr      bool
	}{{
		name:         "derived from the branch",
		common:       common,
		ov:           Override{Branch: "feature"},
		wantWorktree: "/home/u/projects/chisel-worktrees/feature",
		wantProject:  "chisel",
	}, {
		name:         "explicit worktree wins over derivation",
		common:       common,
		ov:           Override{Branch: "feature", WorktreeDir: "/tmp/elsewhere"},
		wantWorktree: "/tmp/elsewhere",
		wantProject:  "chisel",
	}, {
		name:         "explicit worktree is cleaned",
		common:       common,
		ov:           Override{Branch: "feature", WorktreeDir: "/tmp/a/../elsewhere/"},
		wantWorktree: "/tmp/elsewhere",
		wantProject:  "chisel",
	}, {
		// --worktree is the documented escape hatch for a layout that cannot be
		// derived from, so it must relax the ".git" check rather than inherit it.
		name:         "explicit worktree rescues a bare repository",
		common:       "/home/u/projects/chisel.git",
		ov:           Override{Branch: "feature", WorktreeDir: "/tmp/elsewhere"},
		wantWorktree: "/tmp/elsewhere",
		wantProject:  "chisel",
	}, {
		name:    "bare repository without an override is refused",
		common:  "/home/u/projects/chisel.git",
		ov:      Override{Branch: "feature"},
		wantErr: true,
	}, {
		name:         "no branch and no override yields no worktree",
		common:       common,
		ov:           Override{},
		wantWorktree: "",
		wantProject:  "chisel",
	}}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			l, err := Resolve(tc.common, tc.ov)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("expected an error, got %+v", l)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if l.WorktreeDir != tc.wantWorktree {
				t.Errorf("WorktreeDir = %q, want %q", l.WorktreeDir, tc.wantWorktree)
			}
			// ProjectName must always be derivable: it names the workshop
			// definition (D19) even when the worktree came from the user.
			if l.ProjectName != tc.wantProject {
				t.Errorf("ProjectName = %q, want %q", l.ProjectName, tc.wantProject)
			}
			if l.GitCommonDir != filepath.Clean(tc.common) {
				t.Errorf("GitCommonDir = %q", l.GitCommonDir)
			}
			if l.Branch != tc.ov.Branch {
				t.Errorf("Branch = %q, want %q", l.Branch, tc.ov.Branch)
			}
		})
	}
}

func TestEnsureWorktreeIdempotent(t *testing.T) {
	repo, r := initRepo(t, "chisel")
	common, err := CommonDir(r, repo)
	if err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(filepath.Dir(filepath.Dir(common)), "chisel-worktrees", "feature")

	created, mismatch, err := EnsureWorktree(r, repo, common, wt, "feature", "")
	if err != nil || !created || mismatch != "" {
		t.Fatalf("first call: created=%v mismatch=%q err=%v", created, mismatch, err)
	}
	created, mismatch, err = EnsureWorktree(r, repo, common, wt, "feature", "")
	if err != nil || created || mismatch != "" {
		t.Fatalf("second call: created=%v mismatch=%q err=%v", created, mismatch, err)
	}
}

func TestEnsureWorktreeExistingBranch(t *testing.T) {
	repo, r := initRepo(t, "chisel")
	if err := r.Run(repo, "git", "branch", "existing"); err != nil {
		t.Fatal(err)
	}
	common, _ := CommonDir(r, repo)
	if !BranchExists(r, repo, "existing") {
		t.Fatal("BranchExists should be true")
	}
	if BranchExists(r, repo, "nope") {
		t.Fatal("BranchExists should be false")
	}

	wt := filepath.Join(filepath.Dir(filepath.Dir(common)), "chisel-worktrees", "existing")
	if _, _, err := EnsureWorktree(r, repo, common, wt, "existing", ""); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	if b, _ := CurrentBranch(r, wt); b != "existing" {
		t.Errorf("branch = %q", b)
	}
}

func TestEnsureWorktreeBranchMismatchIsReported(t *testing.T) {
	repo, r := initRepo(t, "chisel")
	common, _ := CommonDir(r, repo)
	wt := filepath.Join(filepath.Dir(filepath.Dir(common)), "chisel-worktrees", "dir")

	if _, _, err := EnsureWorktree(r, repo, common, wt, "one", ""); err != nil {
		t.Fatal(err)
	}
	// Same directory, different requested branch: reported, not corrected.
	_, mismatch, err := EnsureWorktree(r, repo, common, wt, "two", "")
	if err != nil {
		t.Fatal(err)
	}
	if mismatch != "one" {
		t.Errorf("mismatch = %q, want %q", mismatch, "one")
	}
}

func TestEnsureWorktreeRejectsForeignDirectory(t *testing.T) {
	repo, r := initRepo(t, "chisel")
	common, _ := CommonDir(r, repo)

	// A plain non-git directory must not be touched.
	plain := filepath.Join(t.TempDir(), "plain")
	if err := os.MkdirAll(plain, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, _, err := EnsureWorktree(r, repo, common, plain, "x", ""); err == nil {
		t.Error("expected a refusal for a non-worktree directory")
	}

	// A worktree of another repository must not be touched either.
	other, r2 := initRepo(t, "other")
	otherCommon, _ := CommonDir(r2, other)
	if _, _, err := EnsureWorktree(r, repo, common, other, "x", ""); err == nil {
		t.Error("expected a refusal for a foreign repository")
	}
	_ = otherCommon
}

func TestEnsureWorktreeRequiresBranchToCreate(t *testing.T) {
	repo, r := initRepo(t, "chisel")
	common, _ := CommonDir(r, repo)
	wt := filepath.Join(filepath.Dir(filepath.Dir(common)), "chisel-worktrees", "nobranch")
	if _, _, err := EnsureWorktree(r, repo, common, wt, "", ""); err == nil {
		t.Fatal("expected an error when no branch is given for a new worktree")
	}
}

func TestEnsureWorktreeFrom(t *testing.T) {
	repo, r := initRepo(t, "chisel")
	common, _ := CommonDir(r, repo)
	head, err := r.Output(repo, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	head = strings.TrimSpace(head)

	wt := filepath.Join(filepath.Dir(filepath.Dir(common)), "chisel-worktrees", "fromrev")
	if _, _, err := EnsureWorktree(r, repo, common, wt, "fromrev", head); err != nil {
		t.Fatalf("EnsureWorktree: %v", err)
	}
	got, err := r.Output(wt, "git", "rev-parse", "HEAD")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(got) != head {
		t.Errorf("HEAD = %q, want %q", strings.TrimSpace(got), head)
	}
}

func TestCommonDirNotARepo(t *testing.T) {
	r := execRunner{t: t}
	dir := t.TempDir()
	if _, err := CommonDir(r, dir); err == nil {
		t.Fatal("expected an error outside a repository")
	}
}

func TestSanitizeBranch(t *testing.T) {
	if got := SanitizeBranch("a/b/c"); got != "a-b-c" {
		t.Errorf("got %q", got)
	}
	if got := SanitizeBranch("plain"); got != "plain" {
		t.Errorf("got %q", got)
	}
}
