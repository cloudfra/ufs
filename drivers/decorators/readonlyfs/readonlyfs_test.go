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

package readonlyfs

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
)

const (
	nullFSPrefix = "null://test"
)

// newInner returns the file system that the tests decorate.
func newInner(t *testing.T) ufs.WriteFS {
	t.Helper()
	fsys, err := ufs.New(t.Context(), nullFSPrefix)
	if err != nil {
		t.Fatalf("New(%q) = %v, want nil", nullFSPrefix, err)
	}
	return fsys
}

func TestReadOnlyWriteOperationsReturnPermissionDenied(t *testing.T) {
	t.Parallel()

	inner := newInner(t)
	fsys := New(inner)

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

	inner := newInner(t)
	fsys := New(inner)

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

	inner := newInner(t)
	fsys := New(inner)

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

	inner := newInner(t)
	fsys := New(inner)

	if got := fsys.String(); !strings.Contains(got, nullFSPrefix) {
		t.Errorf("String() should contain %q, got: %q", nullFSPrefix, got)
	}
}

func TestOptionsDecode(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		raw     any
		want    Options
		wantErr bool
	}{
		{name: "nil", raw: nil, want: Options{}},
		{name: "bare true", raw: true, want: Options{Enabled: true}},
		{name: "bare false", raw: false, want: Options{}},
		{name: "mapping enabled", raw: map[string]any{"enabled": true}, want: Options{Enabled: true}},
		{name: "mapping disabled", raw: map[string]any{"enabled": false}, want: Options{}},
		{name: "empty mapping", raw: map[string]any{}, want: Options{}},
		{name: "typed", raw: Options{Enabled: true}, want: Options{Enabled: true}},
		{name: "bare non-bool", raw: "sometimes", wantErr: true},
		{name: "mapping non-bool", raw: map[string]any{"enabled": []int{1}}, wantErr: true},
		{name: "sequence", raw: []bool{true}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ufs.DecodeOptions[Options](tc.raw)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("DecodeOptions(%#v) = %+v, want error", tc.raw, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("DecodeOptions(%#v) = %v, want nil", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("DecodeOptions(%#v) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}
}

func TestWrap(t *testing.T) {
	t.Parallel()
	inner := newInner(t)

	fsys, err := wrap(t.Context(), inner, Options{})
	if err != nil {
		t.Fatal(err)
	}
	if fsys != inner {
		t.Errorf("wrap with Enabled=false = %v, want the inner FS unchanged", fsys)
	}

	fsys, err = wrap(t.Context(), inner, Options{Enabled: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := fsys.(*readOnlyFS); !ok {
		t.Errorf("wrap with Enabled=true = %T, want *readOnlyFS", fsys)
	}
}
