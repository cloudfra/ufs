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
	"io/fs"
	"testing"
)

// readFSIsComparable pins that every read-only file system must answer IsSame,
// which is what makes the implementations in the drivers mandatory.
var _ ComparableFS = ReadFS(nil)

// tagGet is carried by a file system that names the store holding its data, so
// that one store can be named by two different file system types.
type tagGet interface {
	dataTag() string
}

// taggedFS compares its data through the tag naming its store, which a file
// system that cannot compare its own data also carries.
type taggedFS struct {
	tag string
}

func (taggedFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (fsys taggedFS) Close() error {
	return nil
}

func (fsys taggedFS) IsSame(other fs.FS) bool {
	otherTag, ok := InnermostFS(other).(tagGet)
	return ok && fsys.tag == otherTag.dataTag()
}

func (fsys taggedFS) dataTag() string {
	return fsys.tag
}

// plainFS cannot compare its data; it only names the store holding it.
type plainFS struct {
	tag string
}

func (plainFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (plainFS) Close() error {
	return nil
}

func (fsys plainFS) dataTag() string {
	return fsys.tag
}

func TestSameFS(t *testing.T) {
	t.Parallel()

	store := &plainFS{tag: "one"}

	testCases := []struct {
		name string
		a    fs.FS
		b    fs.FS
		want bool
	}{
		{
			name: "one store named twice",
			a:    taggedFS{tag: "one"},
			b:    taggedFS{tag: "one"},
			want: true,
		},
		{
			name: "two stores",
			a:    taggedFS{tag: "one"},
			b:    taggedFS{tag: "two"},
			want: false,
		},
		{
			name: "one store named by a file system that cannot compare",
			a:    taggedFS{tag: "one"},
			b:    plainFS{tag: "one"},
			want: true,
		},
		{
			name: "two pointers to one value",
			a:    &taggedFS{tag: "one"},
			b:    &taggedFS{tag: "one"},
			want: true,
		},
		{
			name: "a file system that cannot compare reports only itself",
			a:    plainFS{tag: "one"},
			b:    plainFS{tag: "one"},
			want: false,
		},
		{
			name: "two pointers to equal values",
			a:    &plainFS{tag: "one"},
			b:    &plainFS{tag: "one"},
			want: false,
		},
		{
			name: "a wrapper over the file system it presents",
			a:    &passThroughFS{inner: taggedFS{tag: "one"}},
			b:    taggedFS{tag: "one"},
			want: true,
		},
		{
			name: "a file system under a wrapper over it",
			a:    taggedFS{tag: "one"},
			b:    &passThroughFS{inner: taggedFS{tag: "one"}},
			want: true,
		},
		{
			name: "wrappers over one store",
			a:    &passThroughFS{inner: &passThroughFS{inner: taggedFS{tag: "one"}}},
			b:    &passThroughFS{inner: taggedFS{tag: "one"}},
			want: true,
		},
		{
			name: "wrappers over two stores",
			a:    &passThroughFS{inner: taggedFS{tag: "one"}},
			b:    &passThroughFS{inner: taggedFS{tag: "two"}},
			want: false,
		},
		{
			name: "a wrapper over a file system that cannot compare",
			a:    &passThroughFS{inner: store},
			b:    store,
			want: true,
		},
		{
			name: "a file system that cannot compare under a wrapper over it",
			a:    store,
			b:    &passThroughFS{inner: store},
			want: true,
		},
		{
			name: "wrappers over equal file systems that cannot compare",
			a:    &passThroughFS{inner: &plainFS{tag: "one"}},
			b:    &passThroughFS{inner: &plainFS{tag: "one"}},
			want: false,
		},
		{
			name: "a func field cannot be compared",
			a:    &wrapperFS{hook: func() {}},
			b:    &wrapperFS{hook: func() {}},
			want: false,
		},
		{
			name: "two nil file systems",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "one nil file system",
			a:    nil,
			b:    taggedFS{tag: "one"},
			want: false,
		},
		{
			name: "a typed nil file system",
			a:    (*plainFS)(nil),
			b:    nil,
			want: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := SameFS(tc.a, tc.b); got != tc.want {
				t.Errorf("SameFS(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestSameFSAsksBothFileSystems checks that the comparison runs whatever the
// order of the two file systems, so that a driver's answer is not lost because
// it was passed second.
func TestSameFSAsksBothFileSystems(t *testing.T) {
	t.Parallel()

	tagged := taggedFS{tag: "one"}
	plain := plainFS{tag: "one"}
	if !SameFS(tagged, plain) {
		t.Error("SameFS(comparable, plain) = false, want true")
	}
	if !SameFS(plain, tagged) {
		t.Error("SameFS(plain, comparable) = false, want true")
	}
	if SameFS(taggedFS{tag: "one"}, plainFS{tag: "two"}) {
		t.Error("SameFS(two stores) = true, want false")
	}
	if SameFS(plainFS{tag: "one"}, taggedFS{tag: "two"}) {
		t.Error("SameFS(two stores, reversed) = true, want false")
	}
}

// TestSameFSEqualsDiffer pins that comparing data and comparing locations are
// different questions: two separate stores can report one URI.
func TestSameFSEqualsDiffer(t *testing.T) {
	t.Parallel()

	a, b := uriFS{path: "file:///one"}, uriFS{path: "file:///one"}
	if !Equals(a, b) {
		t.Error("Equals(two file systems with one URI) = false, want true")
	}
	if SameFS(a, b) {
		t.Error("SameFS(two file systems with one URI) = true, want false, because a URI does not name one set of data")
	}
}
