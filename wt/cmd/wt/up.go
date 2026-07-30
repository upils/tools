package main

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"

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

	// Step 0: resolve the whole path layout in one place (§5.2).
	layout, err := resolvePaths(ex, o)
	if err != nil {
		return err
	}
	gitCommonDir, worktreeDir, branch := layout.GitCommonDir, layout.WorktreeDir, layout.Branch

	// Step 1: serialise runs against this worktree, before anything is created.
	// The lock is keyed by a hash of the path and lives outside the worktree
	// (D16, R10), so it needs no existing directory — which is what lets it cover
	// `git worktree add` itself. Locking after creation would leave two
	// concurrent runs racing to create the same worktree.
	lk, err := lock.Acquire(worktreeDir, o.force)
	if err != nil {
		return err
	}
	defer lk.Release()

	// Step 2: ensure the worktree exists.
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

	client := &ws.Client{Exec: ex, Dir: worktreeDir}

	// Step 4: ensure the plug is declared in the workshop definition,
	// bootstrapping a minimal one only when the project has none at all. The
	// definition may live at workshop.yaml, .workshop.yaml or .workshop/<NAME>.yaml
	// (D19), so the file to edit is resolved rather than assumed.
	//
	// This is done before the fast path because it is a local file read, and
	// because a live-but-undeclared mount is not a converged state: the next
	// `workshop refresh` would drop it.
	//
	// wroteDef records the definition this run actually wrote, so that the
	// "do not commit" reminder of R5 is printed only when there is something to
	// not commit, and names the file that was really touched (D20; D19 allows
	// three locations, so it is not necessarily workshop.yaml).
	var wroteDef string
	projectName := layout.ProjectName
	sel, err := wsdef.Select(worktreeDir, projectName, o.definition, o.workshop)
	if err != nil {
		return err
	}
	if sel.Bootstrap {
		if o.dryRun {
			fmt.Printf("would create %s (workshop %s-dev, base %s, sdk %s)\n",
				sel.Rel, projectName, wsdef.DefaultBase, wsdef.DefaultSDK)
			return nil
		}
		createdDef, berr := wsdef.Bootstrap(sel.Path, projectName)
		if berr != nil {
			return berr
		}
		if createdDef {
			fmt.Printf("created %s (workshop %s-dev, base %s, sdk %s)\n",
				sel.Rel, projectName, wsdef.DefaultBase, wsdef.DefaultSDK)
			wroteDef = sel.Rel
		}
	}
	def, err := wsdef.Load(sel.Path)
	if err != nil {
		return err
	}
	// A definition under .workshop/ must have `name` equal to its filename, so
	// the filename is authoritative for the workshop identity.
	name := o.workshop
	if name == "" {
		name = def.Name()
	}
	if sel.Name != "" && def.Name() != "" && sel.Name != def.Name() {
		fmt.Fprintf(os.Stderr,
			"wt: warning: %s declares name %q but must be named after the file (%q); "+
				"workshop will reject it\n", sel.Rel, def.Name(), sel.Name)
	}
	yamlChanged, err := def.EnsureMountPlug(o.sdk, o.plug, gitCommonDir)
	if err != nil {
		if errors.Is(err, wsdef.ErrSDKNotFound) {
			return fmt.Errorf("%s: %w; add the sdk or pass --sdk", sel.Rel, err)
		}
		return err
	}
	if yamlChanged {
		if o.dryRun {
			fmt.Printf("would patch %s: sdk %s gains mount plug %s -> %s\n",
				sel.Rel, o.sdk, o.plug, gitCommonDir)
		} else {
			if err := def.Write(); err != nil {
				return err
			}
			fmt.Printf("patched %s (mount plug %q -> %s)\n", sel.Rel, o.plug, gitCommonDir)
			wroteDef = sel.Rel
		}
	}

	// Step 3: fast path — a single read-only query in the steady state (D8).
	// Only valid when the definition needed no change, so that the declared and
	// the live state agree.
	if !yamlChanged {
		if info, ierr := client.Info(name); ierr == nil {
			if info.Status == ws.StatusReady && info.MountIs(o.sdk, o.plug, gitCommonDir) {
				report(info, worktreeDir, gitCommonDir, wroteDef, o)
				return nil
			}
			if name == "" && info.Name != "" {
				name = info.Name
			}
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

	if o.dryRun {
		steps, perr := plan.Plan(plan.State{Status: status, MountOK: mountOK, YAMLChanged: yamlChanged})
		if perr != nil {
			return perr
		}
		fmt.Printf("plan for workshop %s (status %s):\n", name, status)
		if len(steps) == 0 {
			fmt.Println("  nothing to do")
		}
		for i, s := range steps {
			fmt.Printf("  %d. %s\n", i+1, s)
		}
		return nil
	}

	// Step 6: apply the definition, if it changed. This says nothing about the
	// binding — the remount override survives refresh and stop/start (§1.3).
	prep, _, err := plan.Prepare(status, yamlChanged)
	if err != nil {
		return err
	}
	for _, s := range prep {
		if err := execute(client, s, name, o.sdk, o.plug, gitCommonDir); err != nil {
			return err
		}
	}

	// Step 7: re-read the *live* binding before deciding whether to rebind, so
	// that a correct mount is never torn down (D9; regression: a failed patch
	// followed by a manual workshop.yaml used to force a needless stop).
	info, err := client.Info(name)
	if err != nil {
		return err
	}
	status, mountOK = info.Status, info.MountIs(o.sdk, o.plug, gitCommonDir)

	steps, err := plan.Bracket(status, mountOK)
	if err != nil {
		return err
	}
	if len(prep) == 0 && len(steps) == 0 {
		fmt.Printf("workshop %s is already ready with %s mounted\n", name, gitCommonDir)
	}
	// Warn only when stopping a workshop the user could actually be connected to
	// (R4). A workshop this run just launched cannot have a live session.
	if status == ws.StatusReady && !mountOK && !justLaunched(prep) {
		fmt.Fprintf(os.Stderr,
			"wt: warning: workshop %s is running and must be stopped to rebind the mount; "+
				"any live VS Code session will be interrupted\n", name)
	}
	for _, s := range steps {
		if err := execute(client, s, name, o.sdk, o.plug, gitCommonDir); err != nil {
			return err
		}
	}

	// Step 8: verify the post-state rather than trusting the transitions (R1).
	//
	// A failure here means the transitions were accepted but the result is not
	// what was asked for, which is the signature of a parser that has drifted
	// from the real output. So each diagnosis quotes what workshop actually said
	// (info.Raw), rather than leaving the user to re-run `workshop info` and
	// guess what wt saw (R1).
	info, err = client.Info(name)
	if err != nil {
		return err
	}
	if info.Status != ws.StatusReady {
		return withRaw(info, fmt.Errorf(
			"workshop %s is %q after convergence, expected ready", name, info.Status,
		))
	}
	if !info.MountIs(o.sdk, o.plug, gitCommonDir) {
		src, _ := info.MountSource(o.sdk, o.plug)
		return withRaw(info, fmt.Errorf("mount %s/%s:%s is bound to %q, expected %s",
			name, o.sdk, o.plug, src, gitCommonDir))
	}
	if info.Hostname == "" {
		return withRaw(info, fmt.Errorf(
			"workshop %s reports no hostname; cannot connect", name,
		))
	}
	if info.Project != "" && !ws.SamePath(info.Project, worktreeDir) {
		return withRaw(info, fmt.Errorf("workshop %s is bound to project %s, expected %s (R8)",
			name, info.Project, worktreeDir))
	}

	// Step 9: report.
	report(info, worktreeDir, gitCommonDir, wroteDef, o)
	return nil
}

// withRaw appends the captured `workshop info` output to a verification error,
// so that a post-state mismatch shows what workshop reported rather than only
// what wt concluded from it (R1).
func withRaw(info *ws.Info, err error) error {
	if info == nil || info.Raw == "" {
		return err
	}
	return fmt.Errorf("%w\n--- workshop info ---\n%s\n--- end ---",
		err, strings.TrimRight(info.Raw, "\n"))
}

// justLaunched reports whether this run created the container, in which case no
// pre-existing session can be interrupted.
func justLaunched(prep []plan.Step) bool {
	for _, s := range prep {
		if s.Kind == plan.Launch {
			return true
		}
	}
	return false
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
//
// It returns the whole layout rather than loose strings, so that every path a
// run needs — including ProjectName, which names the workshop definition (D19) —
// has exactly one derivation.
func resolvePaths(ex *ws.Exec, o *options) (gitwt.Layout, error) {
	common, err := gitwt.CommonDir(ex, o.repo)
	if err != nil {
		return gitwt.Layout{}, err
	}

	ov := gitwt.Override{WorktreeDir: o.worktree, Branch: o.branch}

	// With neither a branch nor an explicit worktree, the current directory must
	// itself be a linked worktree; use it and its checked-out branch (§5.1).
	if ov.Branch == "" && ov.WorktreeDir == "" {
		root, linked, cerr := gitwt.CurrentWorktree(ex, o.repo)
		if cerr != nil {
			return gitwt.Layout{}, cerr
		}
		if !linked {
			return gitwt.Layout{}, errors.New(
				"a branch name is required: the current directory is the main worktree, " +
					"not a linked worktree",
			)
		}
		ov.WorktreeDir = root
		ov.Branch, _ = gitwt.CurrentBranch(ex, root)
	}

	return gitwt.Resolve(common, ov)
}

// report prints the connection details. wroteDef is the worktree-relative path
// of the definition this run wrote, or "" when none was touched.
func report(info *ws.Info, worktreeDir, gitCommonDir, wroteDef string, o *options) {
	uri := fmt.Sprintf("vscode-remote://ssh-remote+workshop@%s/project", info.Hostname)
	fmt.Printf("\nworkshop:  %s\n", info.Name)
	fmt.Printf("project:   %s\n", worktreeDir)
	fmt.Printf("git-dir:   %s  (mounted)\n", gitCommonDir)
	fmt.Printf("hostname:  %s\n", info.Hostname)
	fmt.Printf("\ncode --folder-uri %s\n", uri)
	// Only warn when this run actually wrote the file (D20, R5). Printing it on
	// every steady-state run would be untrue and train the user to ignore it.
	if wroteDef != "" {
		fmt.Printf("\nnote: %s is intentionally left modified in this worktree; "+
			"the injected path is machine-specific — do not commit it.\n", wroteDef)
	}

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
