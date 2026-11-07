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

// Package readonlyfs provides the "readOnly" file system decorator, which
// rejects every write to the file system it wraps and reports its files and
// directories without write permission bits. Importing the package registers the
// decorator.
package readonlyfs

import (
	"context"
	"io/fs"
	"net/url"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/fsutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"gopkg.in/yaml.v3"
)

var _ types.WriteFS = (*readOnlyFS)(nil)

const (
	optionName = "readOnly"
)

// writableModeBits are the permission bits a read-only file system must never
// report, because its own write operations always fail.
const writableModeBits fs.FileMode = 0o222

// Options configures the readOnly decorator. In a mount spec it is written
// either as a bare bool or as a mapping:
//
//	options:
//	  - readOnly: true
//
//	options:
//	  - readOnly:
//	      enabled: true
type Options struct {
	// Enabled makes the file system read-only.
	Enabled bool `yaml:"enabled"`
}

// UnmarshalYAML accepts both the bare bool and the mapping form of [Options].
func (o *Options) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&o.Enabled)
	}
	type plain Options
	return node.Decode((*plain)(o))
}

// MarshalYAML writes [Options] in its bare bool form.
func (o Options) MarshalYAML() (any, error) {
	return o.Enabled, nil
}

func init() {
	ufs.RegisterDecorator(ufs.NewDecorator(optionName, wrap))
}

func wrap(_ context.Context, inner types.WriteFS, opts Options) (types.WriteFS, error) {
	if !opts.Enabled {
		return inner, nil
	}
	return New(inner), nil
}

// readOnlyFS wraps a [ReadFS] and satisfies [FS] by returning
// [fs.ErrPermission] for all write operations.
type readOnlyFS struct {
	types.ReadFS
}

// IsMountedArchiveDir forwards to the wrapped file system, see
// [types.MountedArchiveDirFS].
func (fsys *readOnlyFS) IsMountedArchiveDir(name string) bool {
	m, ok := fsys.ReadFS.(types.MountedArchiveDirFS)
	return ok && m.IsMountedArchiveDir(name)
}

// New wraps inner as an [WriteFS] whose write operations (Create, MkdirAll,
// Remove, RemoveAll, Rename) always return [fs.ErrPermission], and whose Stat,
// Lstat, ReadDir and Open results never report write permission bits, so an
// emulator such as a FUSE server does not advertise the files as writable. Every
// other read operation delegates to inner unchanged.
func New(inner types.ReadFS) types.WriteFS {
	return &readOnlyFS{
		ReadFS: inner,
	}
}

func (fsys *readOnlyFS) URI() (*url.URL, error) {
	u, err := fsys.ReadFS.URI()
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	// Edit a copy: the wrapped file system may hand out the URL it keeps.
	out := *u
	v := out.Query()
	v.Set("ro", "true")
	out.RawQuery = v.Encode()
	return ufs.AppendURIOption(&out, optionName, Options{Enabled: true})
}

func (fsys *readOnlyFS) String() string {
	return fsutil.String(fsys.ReadFS)
}

// Stat masks the wrapped file system's mode bits.
func (fsys *readOnlyFS) Stat(name string) (fs.FileInfo, error) {
	return maskFileInfo(fsys.ReadFS.Stat(name))
}

// Lstat masks the wrapped file system's mode bits. It is required because
// [types.ReadFS] is a [fs.ReadLinkFS], whose Lstat result is reported next to
// Stat's by emulators such as FUSE.
func (fsys *readOnlyFS) Lstat(name string) (fs.FileInfo, error) {
	return maskFileInfo(fsys.ReadFS.Lstat(name))
}

// ReadDir returns copies of the wrapped entries whose Info mode is masked. The
// entries are copied because a wrapped file system may cache the slice it
// returns, and its own view must keep its real mode bits.
func (fsys *readOnlyFS) ReadDir(name string) ([]fs.DirEntry, error) {
	return maskDirEntries(fsys.ReadFS.ReadDir(name))
}

// Open returns a handle whose Stat mode is masked, keeping whatever interfaces
// the wrapped handle already satisfied.
func (fsys *readOnlyFS) Open(name string) (fs.File, error) {
	f, err := fsys.ReadFS.Open(name)
	if err != nil {
		return nil, err
	}
	return maskFileMode(f), nil
}

func (fsys *readOnlyFS) Create(name string) (types.File, error) {
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("create", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("mkdir", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) Remove(name string) error {
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("remove", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) Rename(oldPath, newPath string) error {
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	return ufserrors.NewLinkError("rename", oldPath, newPath, fs.ErrPermission)
}

func (fsys *readOnlyFS) RemoveAll(name string) error {
	if err := pathutil.Validate("removeall", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("removeall", name, fs.ErrPermission)
}

func maskFileInfo(info fs.FileInfo, err error) (fs.FileInfo, error) {
	if err != nil {
		return nil, err
	}
	return &readOnlyFileInfo{FileInfo: info}, nil
}

func maskDirEntries(entries []fs.DirEntry, err error) ([]fs.DirEntry, error) {
	if err != nil {
		return nil, err
	}
	masked := make([]fs.DirEntry, len(entries))
	for i, entry := range entries {
		masked[i] = &readOnlyDirEntry{DirEntry: entry}
	}
	return masked, nil
}

// readOnlyFileInfo reports the wrapped info's mode without its write bits. Sys
// and Size are the wrapped values: Sys is the wrapped file system's own metadata,
// whose layout only that file system understands.
type readOnlyFileInfo struct {
	fs.FileInfo
}

func (info *readOnlyFileInfo) Mode() fs.FileMode {
	return info.FileInfo.Mode() &^ writableModeBits
}

// readOnlyDirEntry masks the mode of its Info result. Type needs no override
// because it reports only the type bits, never permission bits.
type readOnlyDirEntry struct {
	fs.DirEntry
}

func (entry *readOnlyDirEntry) Info() (fs.FileInfo, error) {
	return maskFileInfo(entry.DirEntry.Info())
}

// readDirTypesFile is a handle that is both a [types.File] and a directory.
type readDirTypesFile interface {
	types.File
	fs.ReadDirFile
}

// maskFileMode hides the write bits reported by a handle's Stat and masks the
// modes of the entries of a directory handle. The handle is adapted with
// [polyfills.ToFile] first, because it never drops a capability: io.ReaderAt and
// io.Seeker are how the FUSE and ProjFS hosts serve a read at an offset, and
// fs.ReadDirFile is how nestFS recognizes a mounted directory. A handle that is
// already a [types.File] is returned by ToFile unchanged, so the usual case costs
// one wrapper. Masking is idempotent, so a handle that already reports no write
// bits is wrapped only to report the same mode.
func maskFileMode(f fs.File) fs.File {
	adapted := polyfills.ToFile(f)
	if dir, ok := adapted.(readDirTypesFile); ok {
		return &readOnlyReadDirFile{readDirTypesFile: dir}
	}
	return &readOnlyFile{File: adapted}
}

type readOnlyFile struct {
	types.File
}

func (f *readOnlyFile) Stat() (fs.FileInfo, error) {
	return maskFileInfo(f.File.Stat())
}

type readOnlyReadDirFile struct {
	readDirTypesFile
}

func (f *readOnlyReadDirFile) Stat() (fs.FileInfo, error) {
	return maskFileInfo(f.readDirTypesFile.Stat())
}

func (f *readOnlyReadDirFile) ReadDir(count int) ([]fs.DirEntry, error) {
	return maskDirEntries(f.readDirTypesFile.ReadDir(count))
}
