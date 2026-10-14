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

package fsutil

import (
	"os"
	"testing"
)

type stringerFS struct{ name string }

func (s stringerFS) String() string { return "stringer://" + s.name }

type ptrStringerFS struct{ name string }

func (s *ptrStringerFS) String() string { return "ptr://" + s.name }

type plainFS struct{ A int }

func TestString(t *testing.T) {
	tests := []struct {
		name string
		fsys any
		want string
	}{
		{name: "Stringer", fsys: stringerFS{name: "a"}, want: "stringer://a"},
		{name: "PointerStringer", fsys: &ptrStringerFS{name: "b"}, want: "ptr://b"},
		{name: "PointerStringerByValue", fsys: ptrStringerFS{name: "c"}, want: "unknown [fsutil.ptrStringerFS] {name:c}"},
		{name: "Unknown", fsys: plainFS{A: 1}, want: "unknown [fsutil.plainFS] {A:1}"},
		{name: "UnknownSlice", fsys: []string{"x"}, want: "unknown [[]string] [x]"},
		{name: "Nil", fsys: nil, want: "unknown [<nil>] <nil>"},
		{name: "NilPointerStringer", fsys: (*ptrStringerFS)(nil), want: "unknown [*fsutil.ptrStringerFS] <nil>"},
		{name: "NilRoot", fsys: (*os.Root)(nil), want: "unknown [*os.Root] <nil>"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := String(tc.fsys); got != tc.want {
				t.Errorf("String(%#v) = %q, want %q", tc.fsys, got, tc.want)
			}
		})
	}
}

func TestStringRoot(t *testing.T) {
	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("os.OpenRoot(%q) failed, %s", dir, err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("Close() failed, %s", err)
		}
	})

	if got := String(root); got != dir {
		t.Errorf("String(os.Root) = %q, want %q", got, dir)
	}
}

func TestIsWriteFlag(t *testing.T) {
	for _, tc := range []struct {
		name string
		flag int
		want bool
	}{
		{name: "O_RDONLY", flag: os.O_RDONLY, want: false},
		{name: "O_RDONLY|O_SYNC", flag: os.O_RDONLY | os.O_SYNC, want: false},
		{name: "O_WRONLY", flag: os.O_WRONLY, want: true},
		{name: "O_RDWR", flag: os.O_RDWR, want: true},
		{name: "O_RDONLY|O_CREATE", flag: os.O_RDONLY | os.O_CREATE, want: true},
		{name: "O_RDONLY|O_TRUNC", flag: os.O_RDONLY | os.O_TRUNC, want: true},
		{name: "O_RDONLY|O_APPEND", flag: os.O_RDONLY | os.O_APPEND, want: true},
		{name: "O_RDONLY|O_EXCL", flag: os.O_RDONLY | os.O_EXCL, want: true},
		{name: "O_WRONLY|O_CREATE|O_TRUNC", flag: os.O_WRONLY | os.O_CREATE | os.O_TRUNC, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsWriteFlag(tc.flag); got != tc.want {
				t.Errorf("IsWriteFlag(%#x) = %t, want %t", tc.flag, got, tc.want)
			}
		})
	}
}
