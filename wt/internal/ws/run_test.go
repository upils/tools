package ws

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// script writes an executable shell script into a temp dir and returns its path.
// It lets the exec-helper contract of §5.5 be tested without any real binary.
func script(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fake")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

// syncBuffer is a Log that tolerates concurrent writes: os/exec copies the
// child's stdout and stderr from two separate goroutines.
type syncBuffer struct {
	mu sync.Mutex
	sb strings.Builder
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.sb.String()
}

func TestQuote(t *testing.T) {
	tests := []struct {
		name string
		args []string
		want string
	}{
		{"workshop", nil, "workshop"},
		{"workshop", []string{"info", "a-dev"}, "workshop info a-dev"},
		{"workshop", []string{""}, `workshop ""`},
		{"workshop", []string{"a b"}, `workshop "a b"`},
		{"workshop", []string{"/p/a'b"}, `workshop "/p/a'b"`},
		{"workshop", []string{"/home/user@example.com/.git"}, "workshop /home/user@example.com/.git"},
	}
	for _, tc := range tests {
		if got := Quote(tc.name, tc.args...); got != tc.want {
			t.Errorf("Quote(%q, %q) = %q, want %q", tc.name, tc.args, got, tc.want)
		}
	}
}

// TestOutputForcesMachineReadableEnv pins D6/§2.3: the child must always see
// NO_COLOR, LC_ALL=C and TERM=dumb, whatever the parent environment says.
func TestOutputForcesMachineReadableEnv(t *testing.T) {
	// A hostile parent environment: colour forced on, a UTF-8 locale, a smart
	// terminal. The forced values must win.
	restore := inheritEnv
	inheritEnv = func() []string {
		return []string{"NO_COLOR=", "LC_ALL=en_US.UTF-8", "TERM=xterm-256color", "KEEP=yes"}
	}
	t.Cleanup(func() { inheritEnv = restore })

	bin := script(t, `printf '%s|%s|%s|%s\n' "$NO_COLOR" "$LC_ALL" "$TERM" "$KEEP"`)
	e := &Exec{}
	out, err := e.Output("", bin)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out), "1|C|dumb|yes"; got != want {
		t.Errorf("child env = %q, want %q", got, want)
	}
}

// TestExecEnvIsAppended checks that Exec.Env overrides the forced defaults, which
// is the only reason the field exists.
func TestExecEnvIsAppended(t *testing.T) {
	bin := script(t, `printf '%s|%s\n' "$TERM" "$EXTRA"`)
	e := &Exec{Env: []string{"TERM=vt100", "EXTRA=x"}}
	out, err := e.Output("", bin)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out), "vt100|x"; got != want {
		t.Errorf("child env = %q, want %q", got, want)
	}
}

// TestOutputRunsInDir pins the CWD scoping of D4.
func TestOutputRunsInDir(t *testing.T) {
	dir := t.TempDir()
	bin := script(t, `pwd`)
	e := &Exec{}
	out, err := e.Output(dir, bin)
	if err != nil {
		t.Fatal(err)
	}
	// macOS resolves /var to /private/var, so compare after EvalSymlinks.
	got, _ := filepath.EvalSymlinks(strings.TrimSpace(out))
	want, _ := filepath.EvalSymlinks(dir)
	if got != want {
		t.Errorf("cwd = %q, want %q", got, want)
	}
}

// TestOutputSeparatesStdoutFromStderr matters because stdout is the parse target
// and stderr is only diagnostic (§5.5); mixing them corrupts every parser.
func TestOutputSeparatesStdoutFromStderr(t *testing.T) {
	bin := script(t, `echo "status: ready"; echo "warning: noise" >&2`)
	var log syncBuffer
	e := &Exec{Log: &log}
	out, err := e.Output("", bin)
	if err != nil {
		t.Fatal(err)
	}
	if got, want := strings.TrimSpace(out), "status: ready"; got != want {
		t.Errorf("stdout = %q, want %q", got, want)
	}
}

// TestOutputFoldsStderrIntoError pins C7: a failure must be reported verbatim,
// with the exact argv, never swallowed.
func TestOutputFoldsStderrIntoError(t *testing.T) {
	bin := script(t, `echo "workshop: daemon is not running" >&2; exit 4`)
	e := &Exec{}
	_, err := e.Output("", bin, "info", "a-dev")
	if err == nil {
		t.Fatal("expected an error")
	}
	msg := err.Error()
	for _, want := range []string{"info", "a-dev", "daemon is not running", "exit status 4"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q does not mention %q", msg, want)
		}
	}
}

// TestRunFoldsStderrIntoError is the mutating counterpart of the above (C7).
func TestRunFoldsStderrIntoError(t *testing.T) {
	bin := script(t, `echo "workshop: cannot remount a populated source" >&2; exit 1`)
	var log syncBuffer
	e := &Exec{Log: &log}
	err := e.Run("", bin, "remount", "a-dev/vscode-remote:git-dir")
	if err == nil {
		t.Fatal("expected an error")
	}
	if !strings.Contains(err.Error(), "cannot remount a populated source") {
		t.Errorf("error does not carry the child's stderr: %v", err)
	}
	if !strings.Contains(err.Error(), "remount") {
		t.Errorf("error does not carry the argv: %v", err)
	}
}

// TestRunStreamsStderr pins C8/R9: a long operation's progress must stay visible
// as it happens, not only after it finishes.
func TestRunStreamsStderr(t *testing.T) {
	bin := script(t, `echo "launching..." >&2; echo "done"`)
	var log syncBuffer
	e := &Exec{Log: &log}
	if err := e.Run("", bin); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(log.String(), "launching...") {
		t.Errorf("stderr was not streamed to the log: %q", log.String())
	}
	if !strings.Contains(log.String(), "+ "+bin) {
		t.Errorf("the argv of a mutating command was not echoed: %q", log.String())
	}
}

// TestDryRunSuppressesMutationsOnly is the core of D14: a mutating command must
// not run, while read-only commands still do, so that the plan can be computed.
func TestDryRunSuppressesMutationsOnly(t *testing.T) {
	marker := filepath.Join(t.TempDir(), "ran")
	bin := script(t, `: >`+marker+`; echo ok`)

	var log syncBuffer
	e := &Exec{DryRun: true, Log: &log}

	if err := e.Run("", bin, "stop"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(marker); err == nil {
		t.Error("a mutating command ran under --dry-run")
	}
	if !strings.Contains(log.String(), "would run:") {
		t.Errorf("the skipped command was not reported: %q", log.String())
	}

	out, err := e.Output("", bin, "info")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(out) != "ok" {
		t.Errorf("a read-only command was suppressed under --dry-run: %q", out)
	}
	if _, err := os.Stat(marker); err != nil {
		t.Error("the read-only command did not actually run")
	}
}

// TestTimeoutIsReportedDistinctly matters for R9: "it timed out" and "it failed"
// call for different user action, so the message must say which.
func TestTimeoutIsReportedDistinctly(t *testing.T) {
	// `exec` so the killed process is `sleep` itself: a forked child would
	// inherit the output pipe and make Wait block past the kill.
	bin := script(t, `exec sleep 5`)
	e := &Exec{Timeout: 20 * time.Millisecond}

	_, err := e.Output("", bin, "info")
	if err == nil || !strings.Contains(err.Error(), "timed out after 20ms") {
		t.Errorf("Output error = %v, want a timeout mentioning the duration", err)
	}

	err = e.Run("", bin, "launch")
	if err == nil || !strings.Contains(err.Error(), "timed out after 20ms") {
		t.Errorf("Run error = %v, want a timeout mentioning the duration", err)
	}
}

// TestDefaultTimeoutApplies guards against a zero Timeout meaning "no timeout".
func TestDefaultTimeoutApplies(t *testing.T) {
	if got := (&Exec{}).timeout(); got != defaultTimeout {
		t.Errorf("zero Timeout = %v, want %v", got, defaultTimeout)
	}
	if got := (&Exec{Timeout: time.Second}).timeout(); got != time.Second {
		t.Errorf("Timeout = %v, want 1s", got)
	}
}

// TestNilLogIsSafe: the Log writer is optional, and a nil one must not panic.
func TestNilLogIsSafe(t *testing.T) {
	bin := script(t, `echo out; echo err >&2`)
	e := &Exec{Verbose: true}
	if _, err := e.Output("", bin); err != nil {
		t.Fatal(err)
	}
	if err := e.Run("", bin); err != nil {
		t.Fatal(err)
	}
}
