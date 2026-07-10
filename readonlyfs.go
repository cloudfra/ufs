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

import "io/fs"

var _ FS = (*readOnlyFS)(nil)

// readOnlyFS wraps a [ReadFS] and satisfies [FS] by returning
// [fs.ErrPermission] for all write operations.
type readOnlyFS struct {
	ReadFS
}

// ReadOnly wraps inner as an [FS] whose write operations (Create, MkdirAll,
// Remove, RemoveAll) always return [fs.ErrPermission]. All read operations
// delegate to inner unchanged.
func ReadOnly(inner ReadFS) FS {
	return &readOnlyFS{
		ReadFS: inner,
	}
}

func (fsys *readOnlyFS) Create(name string) (File, error) {
	if err := validPath("create", name); err != nil {
		return nil, err
	}
	return nil, pathError("create", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := validPath("mkdir", name); err != nil {
		return err
	}
	return pathError("mkdir", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) Remove(name string) error {
	if err := validPath("remove", name); err != nil {
		return err
	}
	return pathError("remove", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) RemoveAll(name string) error {
	if err := validPath("removeall", name); err != nil {
		return err
	}
	return pathError("removeall", name, fs.ErrPermission)
}
