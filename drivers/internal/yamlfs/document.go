// Copyright 2026 Cloudfra
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package yamlfs

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// maxDepth caps how deeply a document may nest, so that a value aliased into
// itself is reported instead of overflowing the stack.
const maxDepth = 512

// maxEntrySlots caps how much a document may hold, counting each entry plus
// one slot in the directory that lists it, so that a short document cannot ask
// for an unbounded tree. A document with no aliases holds about half of it.
const maxEntrySlots = 1 << 16

// builder holds the state of one document: the entries an anchor contributes,
// kept so that aliases of it share one subtree, and the entry slots charged so
// far.
type builder struct {
	shared map[*yaml.Node][]*node
	used   int
}

// charge accounts for entry slots about to be held, and reports an error once
// the document goes past the budget.
func (b *builder) charge(slots int) error {
	b.used += slots
	if b.used > maxEntrySlots {
		return fmt.Errorf("document needs more than %d entries and directory slots", maxEntrySlots)
	}
	return nil
}

// node is one entry of the tree built from a document: either a directory
// holding children, or a file holding the text of a scalar value.
type node struct {
	info     fs.FileInfo
	entries  []fs.DirEntry
	children []*node
	content  []byte
}

// child returns the child of the entry called name, or nil if the entry has no
// such child. Children are kept sorted, so the lookup is a binary search.
func (entry *node) child(name string) *node {
	i, found := slices.BinarySearchFunc(entry.children, name, func(candidate *node, want string) int {
		return strings.Compare(candidate.info.Name(), want)
	})
	if !found {
		return nil
	}
	return entry.children[i]
}

// newDir returns the directory entry called name that holds children.
func newDir(name string, children []*node) *node {
	// The children of a shared subtree reach here more than once, and sorting
	// them again keeps the order they already have.
	slices.SortFunc(children, func(a, b *node) int {
		return strings.Compare(a.info.Name(), b.info.Name())
	})
	entry := &node{
		info:     types.NewFileInfo(name, osutil.EmptyDirSize, dirMode, documentModTime),
		children: children,
		entries:  make([]fs.DirEntry, len(children)),
	}
	for i, child := range children {
		entry.entries[i] = fs.FileInfoToDirEntry(child.info)
	}
	return entry
}

// newFile returns the file entry called name that holds content.
func newFile(name string, content []byte) *node {
	return &node{
		info:    types.NewFileInfo(name, int64(len(content)), fileMode, documentModTime),
		content: content,
	}
}

// build returns the root directory that the YAML or JSON document in data
// describes. An empty document yields an empty directory.
func build(data []byte) (*node, error) {
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	var doc yaml.Node
	if err := decoder.Decode(&doc); err != nil {
		if errors.Is(err, io.EOF) {
			return newDir(pathutil.CwdPath, nil), nil
		}
		return nil, fmt.Errorf("cannot parse document, %w", err)
	}
	switch err := decoder.Decode(&yaml.Node{}); {
	case err == nil:
		return nil, errors.New("document holds more than one YAML document")
	case !errors.Is(err, io.EOF):
		return nil, fmt.Errorf("cannot parse document, %w", err)
	}
	if len(doc.Content) != 1 {
		return nil, fmt.Errorf("document holds %d roots, want one", len(doc.Content))
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode && root.Kind != yaml.SequenceNode {
		return nil, fmt.Errorf("%s at line %d cannot be a file system, want a mapping or a sequence", kindName(root.Kind), root.Line)
	}
	b := &builder{shared: make(map[*yaml.Node][]*node)}
	children, err := b.childrenOf(root, 1)
	if err != nil {
		return nil, err
	}
	if err := b.charge(1 + len(children)); err != nil {
		return nil, err
	}
	return newDir(pathutil.CwdPath, children), nil
}

// childrenOf returns the entries that a mapping or sequence node contributes.
// The entries built for a node are kept, so every alias of that node shares one
// subtree instead of building the same entries over again.
func (b *builder) childrenOf(n *yaml.Node, depth int) ([]*node, error) {
	if depth > maxDepth {
		return nil, fmt.Errorf("document nests deeper than %d levels", maxDepth)
	}
	if children, ok := b.shared[n]; ok {
		return children, nil
	}
	var children []*node
	var err error
	if n.Kind == yaml.SequenceNode {
		children, err = b.sequenceChildren(n, depth)
	} else {
		children, err = b.mappingChildren(n, depth)
	}
	if err != nil {
		return nil, err
	}
	// A node aliased into itself is only ever stored once its entries are
	// complete, so such a node keeps recursing until the depth limit ends it.
	b.shared[n] = children
	return children, nil
}

// sequenceChildren names the entries of a sequence from zero, so that
// [0] holds its first value.
func (b *builder) sequenceChildren(n *yaml.Node, depth int) ([]*node, error) {
	children := make([]*node, 0, len(n.Content))
	for i, item := range n.Content {
		child, err := b.childNode(strconv.Itoa(i), item, depth+1)
		if err != nil {
			return nil, err
		}
		children = append(children, child)
	}
	return children, nil
}

// mappingChildren returns the entries of a mapping. A "<<" key merges the
// mappings it names instead of adding an entry called "<<": the mapping's own
// keys win over merged ones, and an earlier merge wins over a later one.
func (b *builder) mappingChildren(n *yaml.Node, depth int) ([]*node, error) {
	content := n.Content
	if len(content)%2 != 0 {
		return nil, fmt.Errorf("mapping at line %d holds %d nodes, want key and value pairs", n.Line, len(content))
	}
	own := make([]*node, 0, len(content)/2)
	var merged []*node
	for i := 0; i < len(content); i += 2 {
		key, value := content[i], content[i+1]
		if isMerge(key) {
			entries, err := b.mergeChildren(value, depth)
			if err != nil {
				return nil, err
			}
			merged = append(merged, entries...)
			continue
		}
		name, err := keyName(key)
		if err != nil {
			return nil, err
		}
		child, err := b.childNode(name, value, depth+1)
		if err != nil {
			return nil, err
		}
		own = append(own, child)
	}
	return uniqueChildren(own, merged)
}

// mergeChildren returns the entries contributed by the value of a "<<" key,
// which names either one mapping or a sequence of mappings.
func (b *builder) mergeChildren(value *yaml.Node, depth int) ([]*node, error) {
	target := follow(value)
	if target.Kind == yaml.SequenceNode {
		var merged []*node
		for _, item := range target.Content {
			entries, err := b.mergeChildren(item, depth)
			if err != nil {
				return nil, err
			}
			merged = append(merged, entries...)
		}
		return merged, nil
	}
	if target.Kind == yaml.MappingNode {
		return b.childrenOf(target, depth+1)
	}
	return nil, fmt.Errorf("%s at line %d cannot be merged, want a mapping", kindName(target.Kind), target.Line)
}

// uniqueChildren returns children with one entry per name. own comes first, so
// a mapping's own key shadows a merged key, while two own keys of the same name
// is a document error.
func uniqueChildren(own, merged []*node) ([]*node, error) {
	seen := make(map[string]struct{}, len(own)+len(merged))
	children := make([]*node, 0, len(own)+len(merged))
	for _, child := range own {
		name := child.info.Name()
		if _, ok := seen[name]; ok {
			return nil, fmt.Errorf("mapping key %q is defined twice", name)
		}
		seen[name] = struct{}{}
		children = append(children, child)
	}
	for _, child := range merged {
		name := child.info.Name()
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		children = append(children, child)
	}
	return children, nil
}

// childNode returns the entry that a mapping value or sequence item
// contributes under name.
func (b *builder) childNode(name string, value *yaml.Node, depth int) (*node, error) {
	target := follow(value)
	switch target.Kind {
	case yaml.MappingNode, yaml.SequenceNode:
		children, err := b.childrenOf(target, depth)
		if err != nil {
			return nil, err
		}
		// A reused subtree costs its own entry and one directory slot per
		// child, even though its children were built once. A merge key copies
		// the entries of its anchor the same way, so this charge is what holds
		// a document that merges one anchor repeatedly inside the budget.
		if err := b.charge(1 + len(children)); err != nil {
			return nil, err
		}
		return newDir(name, children), nil
	case yaml.ScalarNode:
		content, err := scalarContent(target)
		if err != nil {
			return nil, err
		}
		if err := b.charge(1); err != nil {
			return nil, err
		}
		return newFile(name, content), nil
	}
	return nil, fmt.Errorf("%s at line %d cannot be a file or directory, want a mapping, sequence or scalar", kindName(target.Kind), target.Line)
}

// follow walks alias nodes to the node an alias names. The parser rejects an
// unknown anchor, so an alias always resolves.
func follow(n *yaml.Node) *yaml.Node {
	for n.Kind == yaml.AliasNode && n.Alias != nil {
		n = n.Alias
	}
	return n
}

// isMerge reports whether key is a YAML merge key.
func isMerge(key *yaml.Node) bool {
	return key.Kind == yaml.ScalarNode && key.Value == "<<" && (key.Tag == "" || key.Tag == "!" || key.Tag == "!!merge")
}

// keyName returns the entry name that a mapping key gives its value. A key
// cannot be an empty name, a path, or the current or parent directory.
func keyName(key *yaml.Node) (string, error) {
	if key.Kind != yaml.ScalarNode {
		return "", fmt.Errorf("%s key at line %d is not a scalar", kindName(key.Kind), key.Line)
	}
	name := key.Value
	switch {
	case name == "":
		return "", fmt.Errorf("empty key at line %d", key.Line)
	case name == pathutil.CwdPath || name == "..":
		return "", fmt.Errorf("key %q at line %d cannot name an entry", name, key.Line)
	case strings.ContainsRune(name, '/') || strings.ContainsRune(name, 0):
		return "", fmt.Errorf("key %q at line %d cannot hold a path separator", name, key.Line)
	}
	return name, nil
}

// scalarContent returns the file content of a scalar node: its text, nothing
// for a null value, and the decoded bytes for a !!binary value.
func scalarContent(n *yaml.Node) ([]byte, error) {
	switch n.ShortTag() {
	case "!!null":
		return nil, nil
	case "!!binary":
		data, err := base64.StdEncoding.DecodeString(strings.Join(strings.Fields(n.Value), ""))
		if err != nil {
			return nil, fmt.Errorf("cannot decode the binary value at line %d, %w", n.Line, err)
		}
		return data, nil
	}
	return []byte(n.Value), nil
}

// kindName names a node kind for error messages.
func kindName(kind yaml.Kind) string {
	switch kind {
	case yaml.DocumentNode:
		return "document"
	case yaml.MappingNode:
		return "mapping"
	case yaml.SequenceNode:
		return "sequence"
	case yaml.ScalarNode:
		return "scalar"
	case yaml.AliasNode:
		return "alias"
	}
	return "node"
}
