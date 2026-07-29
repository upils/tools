package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/upils/tools/wt/internal/gitwt"
	"github.com/upils/tools/wt/internal/lock"
	"github.com/upils/tools/wt/internal/plan"
	"github.com/upils/tools/wt/internal/ws"
	"github.com/upils/tools/wt/internal/wsdef"
)

// up implements the algorithm of design §5.3.
func up(o *options) error {
	ex := &ws.Exec{
		Timeout: o.timeout,
		Verbose: o.verbose,
		DryRun:  o.dryRun,
		Log:     os.Stderr,
	}

	// Step 0: resolve repo, gitCommonDir, branch, worktreeDir.
	gitCommonDir, worktreeDir, branch, err := resolvePaths(ex, o)
	if err != nil {
		return err
	}

	// Step 2: ensure the worktree exists (before locking it, D16/step 1).
	created := false
	if !gitwt.DirExists(worktreeDir) && o.dryRun {
		fmt.Printf("would create worktree %s for branch %s\n", worktreeDir, branch)
		return nil
	}
	var mismatch string
	created, mismatch, err = gitwt.EnsureWorktree(ex, o.repo, gitCommonDir, worktreeDir, branch, o.from)
	if err != nil {
		return err
	}
	if created {
		fmt.Printf("created worktree %s (branch %s)\n", worktreeDir, branch)
	}
	if mismatch != "" {
		fmt.Fprintf(os.Stderr,
			"wt: warning: %s has %s checked out, not %s; leaving it as is\n",
			worktreeDir, mismatch, branch)
	}

	// Step 1: serialise runs against this worktree.
	lk, err := lock.Acquire(worktreeDir, o.force)
	if err != nil {
		return err
	}
	defer lk.Release()

	client := &ws.Client{Exec: ex, Dir: worktreeDir}

	// Step 3: fast path — one read-only query in the steady state (D8).
	name := o.workshop
	if info, ierr := client.Info(name); ierr == nil {
		if info.Status == ws.StatusReady && info.MountIs(o.sdk, o.plug, gitCommonDir) {
			report(info, worktreeDir, gitCommonDir, o)
			return nil
		}
		if name == "" && info.Name != "" {
			name = info.Name
		}
	}

	// Step 4: ensure the plug is declared in workshop.yaml.
	defPath := filepath.Join(worktreeDir, "workshop.yaml")
	def, err := wsdef.Load(defPath)
	if err != nil {
		return err
	}
	if name == "" {
		name = def.Name()
	}
	yamlChanged, err := def.EnsureMountPlug(o.sdk, o.plug, gitCommonDir)
	if err != nil {
		if errors.Is(err, wsdef.ErrSDKNotFound) {
			return fmt.Errorf("%s: %w; add the sdk or pass --sdk", defPath, err)
		}
		return err
	}
	if yamlChanged {
		if o.dryRun {
			fmt.Printf("would patch %s: sdk %s gains mount plug %s -> %s\n",
				defPath, o.sdk, o.plug, gitCommonDir)
		} else {
			if err := def.Write(); err != nil {
				return err
			}
			fmt.Printf("patched %s (mount plug %q -> %s)\n", defPath, o.plug, gitCommonDir)
		}
	}

	// Step 5: resolve status from `list`, which knows about Off (D7).
	name, status, err := client.Status(name)
	if err != nil {
		return err
	}

	// Steps 5–7: compute the plan, refusing invalid states (D10).
	mountOK := false
	if status == ws.StatusReady || status == ws.StatusStopped {
		info, ierr := client.Info(name)
		if ierr != nil {
			return ierr
		}
		mountOK = info.MountIs(o.sdk, o.plug, gitCommonDir)
	}

	steps, err := plan.Plan(plan.State{Status: status, MountOK: mountOK, YAMLChanged: yamlChanged})
	if err != nil {
		return err
	}

	if len(steps) == 0 {
		fmt.Printf("workshop %s is already ready with %s mounted\n", name, gitCommonDir)
	}
	if o.dryRun {
		fmt.Printf("plan for workshop %s (status %s):\n", name, status)
		if len(steps) == 0 {
			fmt.Println("  nothing to do")
		}
		for i, s := range steps {
			fmt.Printf("  %d. %s\n", i+1, s)
		}
		return nil
	}

	if status == ws.StatusReady && !mountOK {
		fmt.Fprintf(os.Stderr,
			"wt: warning: workshop %s is running and must be stopped to rebind the mount; "+
				"any live VS Code session will be interrupted\n", name)
	}

	// Step 6/7: execute.
	for _, s := range steps {
		if err := execute(client, s, name, o.sdk, o.plug, gitCommonDir); err != nil {
			return err
		}
	}

	// Step 8: verify the post-state rather than trusting the transitions (R1).
	info, err := client.Info(name)
	if err != nil {
		return err
	}
	if info.Status != ws.StatusReady {
		return fmt.Errorf("workshop %s is %q after convergence, expected ready", name, info.Status)
	}
	if !info.MountIs(o.sdk, o.plug, gitCommonDir) {
		src, _ := info.MountSource(o.sdk, o.plug)
		return fmt.Errorf("mount %s/%s:%s is bound to %q, expected %s",
			name, o.sdk, o.plug, src, gitCommonDir)
	}
	if info.Hostname == "" {
		return fmt.Errorf("workshop %s reports no hostname; cannot connect", name)
	}
	if info.Project != "" && !ws.SamePath(info.Project, worktreeDir) {
		return fmt.Errorf("workshop %s is bound to project %s, expected %s (R8)",
			name, info.Project, worktreeDir)
	}

	// Step 9: report.
	report(info, worktreeDir, gitCommonDir, o)
	return nil
}

func execute(c *ws.Client, s plan.Step, name, sdk, plug, source string) error {
	switch s.Kind {
	case plan.Launch:
		return c.Launch(name)
	case plan.Refresh:
		return c.Refresh()
	case plan.Start:
		return c.Start(name)
	case plan.Stop:
		return c.Stop(name)
	case plan.Remount:
		return c.Remount(name, sdk, plug, source)
	default:
		return fmt.Errorf("unknown plan step %q", s.Kind)
	}
}

// resolvePaths implements design §5.2 plus the "already inside a worktree" case.
func resolvePaths(ex *ws.Exec, o *options) (gitCommonDir, worktreeDir, branch string, err error) {
	gitCommonDir, err = gitwt.CommonDir(ex, o.repo)
	if err != nil {
		return "", "", "", err
	}
	branch = o.branch

	if o.worktree != "" {
		return gitCommonDir, o.worktree, branch, nil
	}

	if branch == "" {
		// Use the current worktree when it is a linked one (§5.1).
		root, linked, cerr := gitwt.CurrentWorktree(ex, o.repo)
		if cerr != nil {
			return "", "", "", cerr
		}
		if !linked {
			return "", "", "", errors.New(
				"a branch name is required: the current directory is the main worktree, " +
					"not a linked worktree",
			)
		}
		cur, _ := gitwt.CurrentBranch(ex, root)
		return gitCommonDir, root, cur, nil
	}

	layout, err := gitwt.LayoutFromCommonDir(gitCommonDir, branch)
	if err != nil {
		return "", "", "", err
	}
	return gitCommonDir, layout.WorktreeDir, branch, nil
}

func report(info *ws.Info, worktreeDir, gitCommonDir string, o *options) {
	uri := fmt.Sprintf("vscode-remote://ssh-remote+workshop@%s/project", info.Hostname)
	fmt.Printf("\nworkshop:  %s\n", info.Name)
	fmt.Printf("project:   %s\n", worktreeDir)
	fmt.Printf("git-dir:   %s  (mounted)\n", gitCommonDir)
	fmt.Printf("hostname:  %s\n", info.Hostname)
	fmt.Printf("\ncode --folder-uri %s\n", uri)
	fmt.Printf("\nnote: workshop.yaml is intentionally left modified in this worktree; " +
		"the injected path is machine-specific — do not commit it.\n")

	if !o.code {
		return
	}
	cmd := exec.Command("code", "--folder-uri", uri)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		fmt.Fprintf(os.Stderr, "wt: warning: cannot launch VS Code: %v\n", err)
		return
	}
	_ = cmd.Process.Release()
}
