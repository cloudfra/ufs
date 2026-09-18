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
)

var _ fs.ReadDirFile = (*readDirFile)(nil)

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
