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

package ufs

import (
	"errors"
	"io/fs"
	"time"
)

var (
	_ FileInfo = (*fsInfo)(nil)

	_ fs.DirEntry = (*virtualDirEntry)(nil)
	_ fs.FileInfo = (*virtualDirEntry)(nil)

	_ fs.ReadDirFile = (*readDirFile)(nil)

	unixEpochTime = time.Time{}
)

type fsInfo struct {
	name    string
	size    int64
	mode    fs.FileMode
	modTime time.Time
	isDir   bool
	sys     any
}

func (finfo *fsInfo) Name() string {
	return finfo.name
}

func (finfo *fsInfo) Size() int64 {
	return finfo.size
}

func (finfo *fsInfo) Mode() fs.FileMode {
	return finfo.mode
}

func (finfo *fsInfo) ModTime() time.Time {
	return finfo.modTime
}

func (finfo *fsInfo) IsDir() bool {
	return finfo.isDir
}

func (finfo *fsInfo) Sys() any {
	return finfo.sys
}

type virtualDirEntry struct {
	name string
}

func (entry *virtualDirEntry) Name() string {
	return entry.name
}

func (entry *virtualDirEntry) IsDir() bool {
	return true
}

func (entry *virtualDirEntry) Type() fs.FileMode {
	return fs.ModeDir
}

func (entry *virtualDirEntry) Mode() fs.FileMode {
	return entry.Type()
}

func (entry *virtualDirEntry) Info() (fs.FileInfo, error) {
	return entry, nil
}

func (entry *virtualDirEntry) Size() int64 {
	return emptyDirSize
}

func (entry *virtualDirEntry) ModTime() time.Time {
	return unixEpochTime
}

func (entry *virtualDirEntry) Sys() any {
	return nil
}

func makeVirtualDirEntry(name string) *virtualDirEntry {
	return &virtualDirEntry{
		name: name,
	}
}

type readDirFile struct {
	fsys ReadFS
	name string
}

func (rdf *readDirFile) Stat() (fs.FileInfo, error) {
	return fs.Stat(rdf.fsys, rdf.name)
}

func (rdf *readDirFile) Read(_ []byte) (int, error) {
	return 0, &fs.PathError{Op: "read", Path: rdf.name, Err: errors.New("is a directory")}
}

func (rdf *readDirFile) Close() error {
	return nil
}

func (rdf *readDirFile) ReadDir(_ int) ([]fs.DirEntry, error) {
	return rdf.fsys.ReadDir(rdf.name)
}

func makeReadDirFile(fsys ReadFS, name string) *readDirFile {
	return &readDirFile{
		fsys: fsys,
		name: name,
	}
}
