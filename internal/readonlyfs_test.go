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
	"strings"
	"testing"
)

func TestReadOnlyWriteOperationsReturnPermissionDenied(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := ReadOnly(inner)

	tests := []struct {
		name string
		op   func() error
	}{
		{"Create", func() error { _, err := fsys.Create("a.txt"); return err }},
		{"MkdirAll", func() error { return fsys.MkdirAll("subdir", fs.ModePerm) }},
		{"Remove", func() error { return fsys.Remove("a.txt") }},
		{"RemoveAll", func() error { return fsys.RemoveAll("subdir") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.op(); !errors.Is(err, fs.ErrPermission) {
				t.Errorf("%s returned %v, want fs.ErrPermission", tc.name, err)
			}
		})
	}
}

func TestReadOnlyWriteOperationsInvalidPath(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := ReadOnly(inner)

	for _, badPath := range []string{"/absolute", "../parent", "bad/../path"} {
		t.Run(badPath, func(t *testing.T) {
			t.Parallel()
			if _, err := fsys.Create(badPath); err == nil {
				t.Errorf("Create(%q) returned nil error, want error", badPath)
			}
			if err := fsys.MkdirAll(badPath, fs.ModePerm); err == nil {
				t.Errorf("MkdirAll(%q) returned nil error, want error", badPath)
			}
			if err := fsys.Remove(badPath); err == nil {
				t.Errorf("Remove(%q) returned nil error, want error", badPath)
			}
			if err := fsys.RemoveAll(badPath); err == nil {
				t.Errorf("RemoveAll(%q) returned nil error, want error", badPath)
			}
		})
	}
}

func TestReadOnlyDelegatesReadsToInner(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := ReadOnly(inner)

	if _, err := fsys.Open("."); err != nil {
		t.Errorf("Open(.) = %v, want nil", err)
	}
	if _, err := fsys.Stat("."); err != nil {
		t.Errorf("Stat(.) = %v, want nil", err)
	}
	if _, err := fsys.ReadDir("."); err != nil {
		t.Errorf("ReadDir(.) = %v, want nil", err)
	}
	if _, err := fsys.ReadFile("a.txt"); err != nil {
		t.Errorf("ReadFile(a.txt) = %v, want nil", err)
	}
}

func TestReadOnlyString(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := ReadOnly(inner)

	if got := fsys.String(); !strings.Contains(got, nullFSPrefix) {
		t.Errorf("String() should contain %q, got: %q", nullFSPrefix, got)
	}
}
