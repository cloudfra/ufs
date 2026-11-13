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

package types

import (
	"io"
	"io/fs"
	"testing"
)

// passThroughFS presents the data of another file system and stores none of it
// itself, which is what [WrapperFS] describes.
type passThroughFS struct {
	inner fs.FS
}

func (fsys *passThroughFS) Open(name string) (fs.File, error) {
	return fsys.inner.Open(name)
}

func (fsys *passThroughFS) Close() error {
	if closer, ok := fsys.inner.(io.Closer); ok {
		return closer.Close()
	}
	return nil
}

func (fsys *passThroughFS) InnerFS() fs.FS {
	return fsys.inner
}

func (fsys *passThroughFS) IsSame(other fs.FS) bool {
	return SameFS(fsys.inner, other)
}

// selfWrappingFS reports itself as its inner file system, which must not be
// unwrapped forever.
type selfWrappingFS struct{}

func (selfWrappingFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (*selfWrappingFS) Close() error {
	return nil
}

func (fsys *selfWrappingFS) InnerFS() fs.FS {
	return fsys
}

// emptyWrapperFS presents no file system, which [WrapperFS.InnerFS] reports as a
// nil file system.
type emptyWrapperFS struct{}

func (emptyWrapperFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (*emptyWrapperFS) Close() error {
	return nil
}

func (*emptyWrapperFS) InnerFS() fs.FS {
	return nil
}

var (
	_ WrapperFS    = (*passThroughFS)(nil)
	_ WrapperFS    = (*selfWrappingFS)(nil)
	_ ComparableFS = (*passThroughFS)(nil)
)

// TestInnermostFS checks that unwrapping reaches the file system holding the
// data, and stops at a file system that wraps nothing, wraps nothing at all, or
// wraps itself.
func TestInnermostFS(t *testing.T) {
	t.Parallel()

	store := &taggedFS{tag: "one"}
	one := &passThroughFS{inner: store}
	two := &passThroughFS{inner: one}
	none := &emptyWrapperFS{}
	self := &selfWrappingFS{}

	testCases := []struct {
		name string
		fsys fs.FS
		want fs.FS
	}{
		{
			name: "a file system that wraps nothing",
			fsys: store,
			want: store,
		},
		{
			name: "one wrapper",
			fsys: one,
			want: store,
		},
		{
			name: "two wrappers",
			fsys: two,
			want: store,
		},
		{
			name: "a wrapper with no inner file system",
			fsys: none,
			want: none,
		},
		{
			name: "a wrapper over itself",
			fsys: self,
			want: self,
		},
		{
			name: "a nil file system",
			fsys: nil,
			want: nil,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := InnermostFS(tc.fsys); got != tc.want {
				t.Errorf("InnermostFS(%v) = %v, want %v", tc.fsys, got, tc.want)
			}
		})
	}
}
