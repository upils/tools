package ws

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Status values reported by workshop (design §2.2). Compared
// case-insensitively because `info` lowercases them and `list` does not.
const (
	StatusOff     = "off"
	StatusReady   = "ready"
	StatusStopped = "stopped"
	StatusPending = "pending"
	StatusWaiting = "waiting"
	StatusError   = "error"
)

// Mount is one mount plug as reported by `workshop info`.
type Mount struct {
	HostSource     string `yaml:"host-source"`
	WorkshopTarget string `yaml:"workshop-target"`
}

// SDK is one SDK entry of `workshop info`. Unknown keys (tracking, installed…)
// are ignored, which is what makes the model tolerant (D6).
type SDK struct {
	Mounts map[string]Mount `yaml:"mounts"`
}

// Info is the narrow model of `workshop info` (§5.4).
type Info struct {
	Name     string         `yaml:"name"`
	Hostname string         `yaml:"hostname"`
	Status   string         `yaml:"status"` // lowercase
	Project  string         `yaml:"project"`
	SDKs     map[string]SDK `yaml:"sdks"`

	// Raw is the captured output, kept for diagnostics (M1).
	Raw string `yaml:"-"`
}

// ParseInfo unmarshals captured (non-TTY) `workshop info` output. On failure the
// error includes the raw text so that a layout change upstream is obvious rather
// than silent (R1).
func ParseInfo(out string) (*Info, error) {
	var i Info
	if err := yaml.Unmarshal([]byte(out), &i); err != nil {
		return nil, fmt.Errorf("cannot parse `workshop info` output: %w\n--- raw output ---\n%s\n--- end ---", err, out)
	}
	i.Raw = out
	i.Status = strings.ToLower(strings.TrimSpace(i.Status))
	if i.Name == "" && i.Status == "" {
		return nil, fmt.Errorf("unrecognised `workshop info` output (no name/status)\n--- raw output ---\n%s\n--- end ---", out)
	}
	return &i, nil
}

// MountSource returns the host source bound to <sdk>:<plug>, and whether the
// mount is present at all.
func (i *Info) MountSource(sdk, plug string) (string, bool) {
	if i == nil {
		return "", false
	}
	s, ok := i.SDKs[sdk]
	if !ok {
		return "", false
	}
	m, ok := s.Mounts[plug]
	if !ok {
		return "", false
	}
	return m.HostSource, true
}

// MountIs reports whether <sdk>:<plug> is bound to want. This is the
// idempotency oracle of D9.
func (i *Info) MountIs(sdk, plug, want string) bool {
	src, ok := i.MountSource(sdk, plug)
	return ok && SamePath(src, want)
}

// ListEntry is one row of `workshop list`.
type ListEntry struct {
	Project  string
	Workshop string
	Status   string // as printed, not lowercased
	Notes    string
}

// ParseList parses `workshop list [--no-headers]` output. Columns are
// whitespace-aligned, so fields are split on runs of two or more spaces (§5.4).
// Both the project-scoped (WORKSHOP STATUS NOTES) and global
// (PROJECT WORKSHOP STATUS NOTES) shapes are accepted.
func ParseList(out string) ([]ListEntry, error) {
	var entries []ListEntry
	for _, line := range strings.Split(out, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		fields := splitColumns(line)
		if len(fields) == 0 {
			continue
		}
		up := strings.ToUpper(fields[0])
		if up == "WORKSHOP" || up == "PROJECT" {
			continue // header row
		}
		var e ListEntry
		switch {
		case len(fields) >= 3 && looksLikeStatus(fields[2]):
			// PROJECT WORKSHOP STATUS [NOTES]
			e = ListEntry{Project: fields[0], Workshop: fields[1], Status: fields[2]}
			if len(fields) > 3 {
				e.Notes = fields[3]
			}
		case len(fields) >= 2 && looksLikeStatus(fields[1]):
			// WORKSHOP STATUS [NOTES]
			e = ListEntry{Workshop: fields[0], Status: fields[1]}
			if len(fields) > 2 {
				e.Notes = fields[2]
			}
		default:
			return nil, fmt.Errorf("cannot parse `workshop list` row %q\n--- raw output ---\n%s\n--- end ---", line, out)
		}
		entries = append(entries, e)
	}
	return entries, nil
}

// FindStatus returns the lowercased status of workshop name. When name is empty
// and exactly one workshop is listed, that one is used (workshop itself allows
// omitting the name in that case).
func FindStatus(entries []ListEntry, name string) (workshop, status string, err error) {
	if len(entries) == 0 {
		return "", "", fmt.Errorf("`workshop list` reported no workshop for this project; " +
			"is there a workshop.yaml here?")
	}
	if name == "" {
		if len(entries) != 1 {
			names := make([]string, 0, len(entries))
			for _, e := range entries {
				names = append(names, e.Workshop)
			}
			return "", "", fmt.Errorf("several workshops defined (%s); pass --workshop",
				strings.Join(names, ", "))
		}
		return entries[0].Workshop, strings.ToLower(entries[0].Status), nil
	}
	for _, e := range entries {
		if e.Workshop == name {
			return e.Workshop, strings.ToLower(e.Status), nil
		}
	}
	return "", "", fmt.Errorf("workshop %q is not defined for this project", name)
}

func looksLikeStatus(s string) bool {
	switch strings.ToLower(s) {
	case StatusOff, StatusReady, StatusStopped, StatusPending, StatusWaiting, StatusError:
		return true
	}
	return false
}

// splitColumns splits a tabwriter-aligned row on runs of two or more spaces (or
// tabs), so that values containing a single space survive.
func splitColumns(line string) []string {
	line = strings.TrimRight(line, " \t\r")
	var fields []string
	var cur strings.Builder
	spaces := 0
	for _, r := range line {
		switch r {
		case ' ':
			spaces++
		case '\t':
			spaces += 2
		default:
			if spaces >= 2 && cur.Len() > 0 {
				fields = append(fields, cur.String())
				cur.Reset()
			} else if spaces > 0 && cur.Len() > 0 {
				cur.WriteRune(' ')
			}
			spaces = 0
			cur.WriteRune(r)
		}
	}
	if cur.Len() > 0 {
		fields = append(fields, cur.String())
	}
	return fields
}

// ExpandHome replaces a leading "~" with $HOME, mirroring workshop's
// cmdutil.ContractHome (§2.3).
func ExpandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil && home != "" {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}

// SamePath compares two paths after ~ expansion and cleaning (§5.4).
func SamePath(a, b string) bool {
	if strings.TrimSpace(a) == "" || strings.TrimSpace(b) == "" {
		return false
	}
	return filepath.Clean(ExpandHome(strings.TrimSpace(a))) ==
		filepath.Clean(ExpandHome(strings.TrimSpace(b)))
}
