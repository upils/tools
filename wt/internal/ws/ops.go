package ws

import (
	"fmt"
	"strings"
)

// Client issues `workshop` commands scoped to one project directory.
//
// Every command runs with the process CWD set to the project directory, and
// `-p <dir>` is added for the subcommands that honour it. `launch` deliberately
// gets no `-p` (design D4).
type Client struct {
	Exec *Exec
	// Dir is the worktree, used both as CWD and as the -p value.
	Dir string
	// Bin is the workshop executable name. Defaults to "workshop".
	Bin string
}

func (c *Client) bin() string {
	if c.Bin != "" {
		return c.Bin
	}
	return "workshop"
}

// withProject appends -p <dir> for the subcommands documented to honour it.
func (c *Client) withProject(args []string) []string {
	return append(args, "-p", c.Dir)
}

// Info runs `workshop info [name]` and parses the result. present is false when
// workshop reports that the workshop does not exist; the error is still returned
// so that a daemon failure is never mistaken for absence (D7).
func (c *Client) Info(name string) (*Info, error) {
	args := []string{"info"}
	if name != "" {
		args = append(args, name)
	}
	out, err := c.Exec.Output(c.Dir, c.bin(), c.withProject(args)...)
	if err != nil {
		return nil, err
	}
	return ParseInfo(out)
}

// List runs `workshop list --no-headers` scoped to the project. It knows about
// `Off` workshops, which `info` does not (D7).
func (c *Client) List() ([]ListEntry, error) {
	out, err := c.Exec.Output(c.Dir, c.bin(), c.withProject([]string{"list", "--no-headers"})...)
	if err != nil {
		return nil, err
	}
	return ParseList(out)
}

// Status resolves the workshop name and its lowercased status.
func (c *Client) Status(name string) (workshop, status string, err error) {
	entries, err := c.List()
	if err != nil {
		return "", "", err
	}
	return FindStatus(entries, name)
}

// Launch ties the workshop to the project and starts it. No -p is passed (D4).
func (c *Client) Launch(name string) error {
	args := []string{"launch"}
	if name != "" {
		args = append(args, name)
	}
	return c.Exec.Run(c.Dir, c.bin(), args...)
}

// Refresh applies definition changes, binding newly added plugs.
func (c *Client) Refresh() error {
	return c.Exec.Run(c.Dir, c.bin(), c.withProject([]string{"refresh"})...)
}

// Start starts a stopped workshop.
func (c *Client) Start(name string) error {
	return c.Exec.Run(c.Dir, c.bin(), c.withProject(withName([]string{"start"}, name))...)
}

// Stop stops a running workshop.
func (c *Client) Stop(name string) error {
	return c.Exec.Run(c.Dir, c.bin(), c.withProject(withName([]string{"stop"}, name))...)
}

// Remount binds source as the host source of <name>/<sdk>:<plug>. It requires
// the workshop to be Stopped when source is populated (C2).
func (c *Client) Remount(name, sdk, plug, source string) error {
	if name == "" {
		return fmt.Errorf("remount requires an explicit workshop name")
	}
	target := fmt.Sprintf("%s/%s:%s", name, sdk, plug)
	return c.Exec.Run(c.Dir, c.bin(), c.withProject([]string{"remount", target, strings.TrimRight(source, "/")})...)
}

func withName(args []string, name string) []string {
	if name != "" {
		return append(args, name)
	}
	return args
}
