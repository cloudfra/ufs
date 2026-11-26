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

// Package yamlfs presents a YAML or JSON document as a read-only file system.
// A mapping key becomes a file or a directory, and the content of a file is
// the text of the value it holds. A sequence becomes a directory whose entries
// are numbered from zero, so the first item of a sequence is at "0". A null
// value becomes an empty file, a !!binary value becomes the file it encodes,
// and a document that holds neither a mapping nor a sequence is rejected.
//
// The document is parsed once by [New], so the tree never changes and every
// operation is safe for concurrent use. Entries report no modification time
// and the read-only modes of an embedded file. Every write operation fails
// with [fs.ErrPermission].
//
// The implementation is split by the interface each method belongs to, one file
// per interface: this one holds the file-system value, the handles and what
// [types.SimpleFS] asks for, and the siblings named yamlfs_<interface>.go hold
// the rest. document.go, which is not part of the split, holds the tree those
// files walk.
package yamlfs

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"slices"
	"strings"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ types.WriteFS  = (*yamlFS)(nil)
	_ fs.ReadDirFile = (*dirFile)(nil)
)

const (
	// fileMode and dirMode are the modes of every entry, which is what an
	// embedded file system reports for content it cannot change.
	fileMode = fs.FileMode(0o444)
	dirMode  = fs.ModeDir | fs.FileMode(0o555)
)

// documentModTime is the modification time of every entry, because a document
// in memory has no modification time to report.
var documentModTime = time.Time{}

// errIsDir and errNotDir label the directory mistakes of an operation. They
// stay private until issue #548 decides which sentinel these errors present.
var (
	errIsDir  = errors.New("is a directory")
	errNotDir = errors.New("not a directory")
)

// yamlFS is a read-only file system whose tree was built from one document.
type yamlFS struct {
	name string
	root *node
}

// New returns a read-only file system holding the entries of the YAML or JSON
// document in data. name labels the file system in its URI and its description.
func New(name string, data []byte) (types.WriteFS, error) {
	root, err := build(data)
	if err != nil {
		return nil, fmt.Errorf("cannot make a file system from the document %q, %w", name, err)
	}
	return &yamlFS{name: name, root: root}, nil
}

func (fsys *yamlFS) Close() error {
	return nil
}

func (fsys *yamlFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	entry, err := fsys.lookup(name)
	if err != nil {
		return nil, ufserrors.NewPathError("open", name, err)
	}
	if entry.info.IsDir() {
		return &dirFile{path: name, info: entry.info, entries: slices.Clone(entry.entries)}, nil
	}
	return &leafFile{info: entry.info, Reader: bytes.NewReader(entry.content)}, nil
}

// lookup walks the components of name down the tree from the root.
func (fsys *yamlFS) lookup(name string) (*node, error) {
	entry := fsys.root
	if name == pathutil.CwdPath {
		return entry, nil
	}
	for _, part := range strings.Split(name, pathutil.UnixSeparator) {
		child := entry.child(part)
		if child == nil {
			return nil, fs.ErrNotExist
		}
		entry = child
	}
	return entry, nil
}

// leafFile is an open handle on the content of a leaf value.
type leafFile struct {
	info fs.FileInfo
	*bytes.Reader
}

func (f *leafFile) Stat() (fs.FileInfo, error) {
	return f.info, nil
}

func (f *leafFile) Close() error {
	return nil
}

// dirFile is an open handle on a directory. It keeps its own copy of the
// entries, so a caller reordering them cannot disturb the tree.
type dirFile struct {
	path    string
	info    fs.FileInfo
	entries []fs.DirEntry
	offset  int
}

func (d *dirFile) Stat() (fs.FileInfo, error) {
	return d.info, nil
}

func (d *dirFile) Read([]byte) (int, error) {
	return 0, ufserrors.NewPathError("read", d.path, errIsDir)
}

func (d *dirFile) Close() error {
	return nil
}

func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if n <= 0 {
		batch := d.entries[d.offset:]
		d.offset = len(d.entries)
		return batch, nil
	}
	if d.offset >= len(d.entries) {
		return nil, io.EOF
	}
	end := min(d.offset+n, len(d.entries))
	batch := d.entries[d.offset:end]
	d.offset = end
	return batch, nil
}
