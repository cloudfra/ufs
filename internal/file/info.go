// Copyright 2026 Jeremy Edwards
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

// Package file provides plain, backend-agnostic implementations of
// [fs.FileInfo] and [fs.DirEntry] for file systems that need to synthesize
// one — from a stat-like record read from a backing store ([Info]), or for
// an implicit/virtual directory that has no record of its own
// ([VirtualDirEntry]).
package file

import (
	"io/fs"
	"time"
)

// unixEpochTime is the zero-cost stand-in ModTime for entries that have no
// real modification time to report, such as [VirtualDirEntry].
var unixEpochTime = time.Time{}

// Params holds the fields of a synthesized [Info]. Fields left at their zero
// value produce the natural default: empty name, size 0, mode 0, the zero
// time, not a directory, nil Sys.
//
// Params is a separate type from [Info] (rather than exported fields on Info
// itself) because Info's method set (Name, Size, Mode, ModTime, IsDir, Sys)
// would otherwise collide with same-named exported fields.
type Params struct {
	Name    string
	Size    int64
	Mode    fs.FileMode
	ModTime time.Time
	IsDir   bool
	Sys     any
}

// Info is a plain [fs.FileInfo] built from a fixed [Params] snapshot.
type Info struct {
	p Params
}

// New returns an [Info] reporting exactly the fields in p.
func New(p Params) *Info {
	return &Info{p: p}
}

// Name implements [fs.FileInfo].
func (i *Info) Name() string { return i.p.Name }

// Size implements [fs.FileInfo].
func (i *Info) Size() int64 { return i.p.Size }

// Mode implements [fs.FileInfo].
func (i *Info) Mode() fs.FileMode { return i.p.Mode }

// ModTime implements [fs.FileInfo].
func (i *Info) ModTime() time.Time { return i.p.ModTime }

// IsDir implements [fs.FileInfo].
func (i *Info) IsDir() bool { return i.p.IsDir }

// Sys implements [fs.FileInfo].
func (i *Info) Sys() any { return i.p.Sys }

var (
	_ fs.FileInfo = (*Info)(nil)

	_ fs.DirEntry = (*VirtualDirEntry)(nil)
	_ fs.FileInfo = (*VirtualDirEntry)(nil)
)

// VirtualDirEntry is a synthetic [fs.DirEntry] and [fs.FileInfo] for a
// directory that exists only implicitly — for example, a path that is a
// prefix of some other stored path but was never itself created.
type VirtualDirEntry struct {
	name string
}

// NewVirtualDirEntry returns a VirtualDirEntry for the given base name.
func NewVirtualDirEntry(name string) *VirtualDirEntry {
	return &VirtualDirEntry{name: name}
}

// Name implements [fs.DirEntry] and [fs.FileInfo].
func (entry *VirtualDirEntry) Name() string { return entry.name }

// IsDir implements [fs.DirEntry] and [fs.FileInfo]. Always true.
func (entry *VirtualDirEntry) IsDir() bool { return true }

// Type implements [fs.DirEntry]. Always [fs.ModeDir].
func (entry *VirtualDirEntry) Type() fs.FileMode {
	return fs.ModeDir
}

// Mode implements [fs.FileInfo].
func (entry *VirtualDirEntry) Mode() fs.FileMode { return entry.Type() }

// Info implements [fs.DirEntry]. It always succeeds, returning entry itself.
func (entry *VirtualDirEntry) Info() (fs.FileInfo, error) {
	return entry, nil
}

// Size implements [fs.FileInfo]. Always 0.
func (entry *VirtualDirEntry) Size() int64 { return 0 }

// ModTime implements [fs.FileInfo]. Always the zero time.
func (entry *VirtualDirEntry) ModTime() time.Time { return unixEpochTime }

// Sys implements [fs.FileInfo]. Always nil.
func (entry *VirtualDirEntry) Sys() any { return nil }
