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

// Mount spec tests that need the readOnly and fault decorators, kept in a
// separate external (ufs_test) package because drivers/decorators imports ufs.
package ufs_test

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	_ "github.com/cloudfra/ufs/drivers/decorators/faultfs"
	_ "github.com/cloudfra/ufs/drivers/decorators/readonlyfs"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestNewFromYAMLReadOnly(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  options:
    readOnly: true`
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on read-only FS succeeded, want error")
	}
}

func TestNewFromYAMLReadOnlyMount(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
- source: "memory://"
  mountPoint: "ro-data"
  options:
    readOnly: true`
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("Create at root: %v", err)
	}

	_, err = fsys.Create("ro-data/file.txt")
	if err == nil {
		t.Fatal("Create on read-only mount succeeded, want error")
	}
}

func TestNewFromYAMLNoRoot(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "data"`
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on implicit read-only null root succeeded, want error")
	}

	if _, err := fsys.Create("data/file.txt"); err != nil {
		t.Fatalf("Create in mount: %v", err)
	}
}

func TestNewFromFstabReadOnly(t *testing.T) {
	t.Parallel()
	input := "memory:// . auto ro 0 0"
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on read-only FS succeeded, want error")
	}
}

func TestNewFromFstabReadOnlyMount(t *testing.T) {
	t.Parallel()
	input := "memory:// . auto rw 0 0\nmemory:// /data auto ro 0 0"
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("Create at root: %v", err)
	}

	_, err = fsys.Create("data/file.txt")
	if err == nil {
		t.Fatal("Create on read-only mount succeeded, want error")
	}
}

func TestNewFromFstabNoRoot(t *testing.T) {
	t.Parallel()
	input := "memory:// data auto rw 0 0"
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on implicit read-only null root succeeded, want error")
	}

	if _, err := fsys.Create("data/file.txt"); err != nil {
		t.Fatalf("Create in mount: %v", err)
	}
}

func TestNewFromYAMLFaultInjector(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
  options:
    fault:
      errorRate: 1.0
      errorMessage: "disk on fire"`
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on fault-injected FS succeeded, want error")
	}
}

func TestNewFromYAMLFaultInjectorMount(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
- source: "memory://"
  mountPoint: "unstable"
  options:
    fault:
      errorRate: 1.0`
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("Create at root: %v", err)
	}

	_, err = fsys.Create("unstable/file.txt")
	if err == nil {
		t.Fatal("Create on fault-injected mount succeeded, want error")
	}
}

func TestNewFromYAMLFaultAndReadOnly(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
  options:
    readOnly: true
    fault:
      latency: 1ms`
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on read-only + fault FS succeeded, want error")
	}
}

func TestNewFromYAMLDecoratorOptionForms(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name         string
		options      string
		wantReadOnly bool
	}{
		{"bare bool", "readOnly: true", true},
		{"bare bool disabled", "readOnly: false", false},
		{"mapping", "readOnly:\n      enabled: true", true},
		{"mapping disabled", "readOnly:\n      enabled: false", false},
		{"empty section", "readOnly:", false},
		{"name is case-insensitive", "readonly: true", true},
		{"zero fault section", "fault: {}", false},
		{"fault below read-only", "fault:\n      latency: 1ms\n    readOnly:\n      enabled: true", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			input := "- source: \"memory://\"\n  options:\n    " + tc.options
			fsys, err := ufs.New(t.Context(), input)
			if err != nil {
				t.Fatalf("New(%q) = %v, want nil", input, err)
			}
			defer ufsTesting.ValidateClose(t, fsys)()

			f, err := fsys.Create("file.txt")
			if tc.wantReadOnly {
				if !errors.Is(err, fs.ErrPermission) {
					t.Fatalf("Create() = %v, want fs.ErrPermission", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("Create() = %v, want nil", err)
			}
			if err := f.Close(); err != nil {
				t.Errorf("Close() = %v, want nil", err)
			}
		})
	}
}

func TestNewFromYAMLDecoratorOptionErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantErr string
	}{
		{
			name: "unknown root option",
			input: `- source: "memory://"
  options:
    noSuchDecorator: true`,
			wantErr: "cannot find a ufs file system decorator",
		},
		{
			name: "unknown mount option",
			input: `- source: "memory://"
- source: "memory://"
  mountPoint: "data"
  options:
    noSuchDecorator: true`,
			wantErr: "cannot find a ufs file system decorator",
		},
		{
			name: "readOnly is not a bool",
			input: `- source: "memory://"
  options:
    readOnly: sometimes`,
			wantErr: `invalid options for file system decorator "readOnly"`,
		},
		{
			name: "readOnly mapping has the wrong type",
			input: `- source: "memory://"
  options:
    readOnly:
      enabled: [1, 2]`,
			wantErr: `invalid options for file system decorator "readOnly"`,
		},
		{
			name: "fault latency is not a duration",
			input: `- source: "memory://"
  options:
    fault:
      latency: soon`,
			wantErr: `invalid options for file system decorator "fault"`,
		},
		{
			name: "fault is not a mapping",
			input: `- source: "memory://"
  options:
    fault: true`,
			wantErr: `invalid options for file system decorator "fault"`,
		},
		{
			name: "same decorator twice",
			input: `- source: "memory://"
  options:
    readOnly: true
    readonly: true`,
			wantErr: "configured more than once",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, err := ufs.New(t.Context(), tc.input)
			if err == nil {
				ufsTesting.ValidateClose(t, fsys)()
				t.Fatalf("New(%q) = nil error, want %q", tc.input, tc.wantErr)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("New(%q) = %q, want substring %q", tc.input, err, tc.wantErr)
			}
		})
	}
}
