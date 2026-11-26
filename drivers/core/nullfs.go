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

package core

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

const (
	nullFSPrefix = "null:"
)

var (
	_ types.File     = (*nullFile)(nil)
	_ types.WriteFS  = (*nullFS)(nil)
	_ fs.GlobFS      = (*nullFS)(nil)
	_ fs.ReadDirFile = (*nullReadDirFile)(nil)

	// nullModTime is the modification time of every nullFS entry.
	nullModTime = time.Time{}

	nullDirStat = types.NewFileInfo(".", osutil.EmptyDirSize, fs.ModeDir|fs.ModePerm, nullModTime)

	nullDeviceInfo    = types.NewDeviceInfo("null", "null", 1, false)
	nullDeviceInfoMap = types.NewDeviceMap(nullDeviceInfo)
)

func init() {
	ufs.Register(ufs.NewDriver("null", newNullFS, isNullFSUri, 1, false, true))
}

type nullFile struct {
	name string
}

func (n *nullFile) Stat() (fs.FileInfo, error) {
	isDir := pathutil.IsDirName(n.name)
	mode := fs.ModePerm
	size := int64(0)
	if isDir {
		mode = fs.ModeDir | fs.ModePerm
		size = osutil.EmptyDirSize
	}
	return types.NewFileInfo(path.Base(n.name), size, mode, nullModTime), nil
}

func (n *nullFile) Read(_ []byte) (int, error) {
	return 0, io.EOF
}

func (n *nullFile) Close() error {
	return nil
}

func (n *nullFile) Write(p []byte) (n2 int, err error) {
	return len(p), nil
}

func (n *nullFile) Seek(_ int64, _ int) (int64, error) {
	return 0, nil
}

func (n *nullFile) ReadAt(_ []byte, _ int64) (int, error) {
	return 0, io.EOF
}

func (n *nullFile) WriteString(s string) (int, error) {
	return len(s), nil
}

func newNullFile(name string) *nullFile {
	return &nullFile{
		name: name,
	}
}

type nullReadDirFile struct{}

func (vrd *nullReadDirFile) Stat() (fs.FileInfo, error) {
	return nullDirStat, nil
}

func (vrd *nullReadDirFile) Read(_ []byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: ".", Err: errors.New("is a directory")}
}

func (vrd *nullReadDirFile) Close() error {
	return nil
}

// ReadDir reports an empty listing: a nullFS directory holds no entries, so a
// bounded read is already at the end of the directory.
func (vrd *nullReadDirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if n > 0 {
		return nil, io.EOF
	}
	return []fs.DirEntry{}, nil
}

type nullFS struct {
	name string
}

func (fsys *nullFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	if pathutil.IsDirName(name) {
		return &nullReadDirFile{}, nil
	}
	return newNullFile(name), nil
}

func (fsys *nullFS) Close() error {
	return nil
}

func newNullFS(_ context.Context, name string) (types.WriteFS, error) {
	return makeNullFS(name), nil
}

// MakeNullFS creates a null FS without a context.
//
// Deprecated: MakeNullFS exists only to support the shared driver tests in
// drivers/testing and will be removed soon. Use New instead.
func MakeNullFS(name string) types.WriteFS {
	return makeNullFS(name)
}

func makeNullFS(name string) *nullFS {
	return &nullFS{
		name: name,
	}
}

func isNullFSUri(name string) bool {
	return strings.HasPrefix(name, nullFSPrefix)
}
