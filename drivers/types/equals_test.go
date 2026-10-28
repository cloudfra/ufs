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

// comparableFS holds only comparable fields, so == on two file systems holding
// it compares the values.
type comparableFS struct {
	id int
}

func (comparableFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

// wrapperFS holds a func field, so == on two file systems holding it panics
// rather than reporting a result. The value is stored by value; a pointer to it
// would be comparable.
type wrapperFS struct {
	hook func()
}

func (wrapperFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

// sliceFS holds a slice field, which is not comparable either.
type sliceFS struct {
	entries []string
}

func (sliceFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func TestEquals(t *testing.T) {
	t.Parallel()

	hook := func() {}

	testCases := []struct {
		name string
		a    fs.FS
		b    fs.FS
		want bool
	}{
		{
			name: "the same value",
			a:    comparableFS{id: 1},
			b:    comparableFS{id: 1},
			want: true,
		},
		{
			name: "different values of the same type",
			a:    comparableFS{id: 1},
			b:    comparableFS{id: 2},
			want: false,
		},
		{
			name: "different types",
			a:    comparableFS{id: 1},
			b:    sliceFS{},
			want: false,
		},
		{
			name: "a func field is not comparable",
			a:    wrapperFS{hook: hook},
			b:    wrapperFS{hook: hook},
			want: false,
		},
		{
			name: "a slice field is not comparable",
			a:    sliceFS{entries: []string{"a"}},
			b:    sliceFS{entries: []string{"a"}},
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
			b:    comparableFS{id: 1},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Equals(tc.a, tc.b); got != tc.want {
				t.Errorf("Equals(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestEqualsPointer checks that a file system passed as a pointer keeps pointer
// identity, which is how drivers and decorators are usually passed to the ops
// helpers.
func TestEqualsPointer(t *testing.T) {
	t.Parallel()

	mem := &comparableFS{id: 1}
	if !Equals(mem, mem) {
		t.Error("Equals(same pointer) = false, want true")
	}
	if Equals(mem, &comparableFS{id: 1}) {
		t.Error("Equals(two pointers to equal values) = true, want false")
	}
}
