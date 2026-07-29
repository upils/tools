// Package wsdef reads and patches a workshop definition file (workshop.yaml)
// while preserving comments and key order (design D11, §5.6).
package wsdef

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// File is a loaded workshop.yaml together with its document node.
type File struct {
	Path string
	Doc  *yaml.Node // document node
	raw  []byte
}

// ErrSDKNotFound is returned when the requested SDK is absent from the
// definition. The caller turns this into a clear abort message.
var ErrSDKNotFound = errors.New("sdk not found in workshop.yaml")

// Load reads and parses the definition at path.
func Load(path string) (*File, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("cannot read %s: %w", path, err)
	}
	var doc yaml.Node
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return nil, fmt.Errorf("cannot parse %s: %w", path, err)
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return nil, fmt.Errorf("%s is empty or not a YAML document", path)
	}
	return &File{Path: path, Doc: &doc, raw: raw}, nil
}

// Name returns the workshop name declared in the definition, or "".
func (f *File) Name() string {
	root := f.Doc.Content[0]
	if n := mapValue(root, "name"); n != nil {
		return n.Value
	}
	return ""
}

// SDKNames lists the SDK names declared in the definition, in order.
func (f *File) SDKNames() []string {
	var names []string
	sdks := mapValue(f.Doc.Content[0], "sdks")
	if sdks == nil || sdks.Kind != yaml.SequenceNode {
		return nil
	}
	for _, item := range sdks.Content {
		if n := mapValue(item, "name"); n != nil {
			names = append(names, n.Value)
		}
	}
	return names
}

// EnsureMountPlug makes sdk declare a mount plug named plug with the given
// workshop-target (§5.6). Sibling keys of an existing plug are preserved.
//
// changed reports whether the in-memory document was modified; when false the
// file must not be rewritten so that it stays byte-identical.
func (f *File) EnsureMountPlug(sdk, plug, target string) (changed bool, err error) {
	target = strings.TrimRight(target, "/")
	if target == "" || !filepath.IsAbs(target) {
		return false, fmt.Errorf("workshop-target must be an absolute path, got %q", target)
	}

	root := f.Doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return false, fmt.Errorf("%s: top level is not a mapping", f.Path)
	}

	sdks := mapValue(root, "sdks")
	if sdks == nil || sdks.Kind != yaml.SequenceNode {
		return false, fmt.Errorf("%s: no sdks list; %w: %s", f.Path, ErrSDKNotFound, sdk)
	}

	var sdkNode *yaml.Node
	for _, item := range sdks.Content {
		if n := mapValue(item, "name"); n != nil && n.Value == sdk {
			sdkNode = item
			break
		}
	}
	if sdkNode == nil {
		return false, fmt.Errorf("%w: %s (declared: %s)",
			ErrSDKNotFound, sdk, strings.Join(f.SDKNames(), ", "))
	}

	plugs := mapValue(sdkNode, "plugs")
	if plugs == nil {
		plugs = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapValue(sdkNode, "plugs", plugs)
		changed = true
	}
	if plugs.Kind != yaml.MappingNode {
		return false, fmt.Errorf("%s: sdk %s has a non-mapping plugs value", f.Path, sdk)
	}

	plugNode := mapValue(plugs, plug)
	if plugNode == nil {
		plugNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		setMapValue(plugs, plug, plugNode)
		changed = true
	}
	if plugNode.Kind != yaml.MappingNode {
		return false, fmt.Errorf("%s: plug %s is not a mapping", f.Path, plug)
	}

	iface := mapValue(plugNode, "interface")
	switch {
	case iface == nil:
		setMapValue(plugNode, "interface", scalar("mount", 0))
		changed = true
	case iface.Value != "mount":
		return false, fmt.Errorf(
			"%s: plug %s already uses interface %q, expected \"mount\"; refusing to change it",
			f.Path, plug, iface.Value,
		)
	}

	// The target contains "@" and "." and is quoted for safety and to match
	// the existing style (§5.6).
	if tgt := mapValue(plugNode, "workshop-target"); tgt != nil {
		if strings.TrimRight(tgt.Value, "/") != target || tgt.Style != yaml.DoubleQuotedStyle {
			tgt.Kind = yaml.ScalarNode
			tgt.Tag = "!!str"
			tgt.Value = target
			tgt.Style = yaml.DoubleQuotedStyle
			changed = true
		}
	} else {
		setMapValue(plugNode, "workshop-target", scalar(target, yaml.DoubleQuotedStyle))
		changed = true
	}

	return changed, nil
}

// Marshal renders the (possibly patched) document.
func (f *File) Marshal() ([]byte, error) {
	var sb strings.Builder
	enc := yaml.NewEncoder(&sb)
	enc.SetIndent(2)
	if err := enc.Encode(f.Doc); err != nil {
		return nil, err
	}
	if err := enc.Close(); err != nil {
		return nil, err
	}
	return []byte(sb.String()), nil
}

// Write atomically replaces the file on disk with the current document
// (temp file in the same directory, fsync, rename — §5.6).
func (f *File) Write() error {
	data, err := f.Marshal()
	if err != nil {
		return fmt.Errorf("cannot render %s: %w", f.Path, err)
	}
	return writeAtomic(f.Path, data)
}

func writeAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName) // no-op after a successful rename

	mode := os.FileMode(0o644)
	if st, serr := os.Stat(path); serr == nil {
		mode = st.Mode().Perm()
	}

	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmpName, mode); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

// --- yaml.Node helpers ------------------------------------------------------

func mapValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func setMapValue(m *yaml.Node, key string, val *yaml.Node) {
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content[i+1] = val
			return
		}
	}
	m.Content = append(m.Content, scalar(key, 0), val)
}

func scalar(v string, style yaml.Style) *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: v, Style: style}
}
