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

// Mount spec tests that need the readOnly and fault decorators, kept in a
// separate external (ufs_test) package because drivers/decorators imports ufs.
package ufs_test

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	_ "github.com/cloudfra/ufs/drivers/core"
	_ "github.com/cloudfra/ufs/drivers/decorators/faultfs"
	_ "github.com/cloudfra/ufs/drivers/decorators/readonlyfs"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestNewFromYAMLReadOnly(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  options:
    - readOnly: true`
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
    - readOnly: true`
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

// writableModeBits are the permission bits a read-only file system must never
// report.
const writableModeBits fs.FileMode = 0o222

// TestNewFromYAMLReadOnlyMountMasksModeBits checks that the mode mask survives
// nestFS routing into a mount: the same directory mounted read-only reports no
// write permission bits, while a writable mount of that same directory keeps
// them.
func TestNewFromYAMLReadOnlyMountMasksModeBits(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	// The mode gives the owner write permission without any group or other bits,
	// so a normal umask cannot change it. The file has to be writable for the
	// mask to have anything to hide.
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("hello"), 0o600); err != nil {
		t.Fatalf("WriteFile = %v, want nil", err)
	}
	input := fmt.Sprintf("- source: %q\n  mountPoint: \"ro-data\"\n  options:\n    - readOnly: true\n- source: %q\n  mountPoint: \"rw-data\"", dir, dir)
	fsys, err := ufs.New(t.Context(), input)
	if err != nil {
		t.Fatalf("New = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	tests := []struct {
		accessor string
		mount    string
		// wantWritable is whether the mount must report write permission bits.
		wantWritable bool
	}{
		{accessor: "Stat", mount: "ro-data", wantWritable: false},
		{accessor: "Stat", mount: "rw-data", wantWritable: true},
		{accessor: "Lstat", mount: "ro-data", wantWritable: false},
		{accessor: "Lstat", mount: "rw-data", wantWritable: true},
		{accessor: "ReadDir entry", mount: "ro-data", wantWritable: false},
		{accessor: "ReadDir entry", mount: "rw-data", wantWritable: true},
		{accessor: "Open handle", mount: "ro-data", wantWritable: false},
		{accessor: "Open handle", mount: "rw-data", wantWritable: true},
	}
	for _, tc := range tests {
		// Not a parallel subtest: the file system is closed when this test
		// function returns, which is before a parallel subtest would run.
		t.Run(tc.accessor+" "+tc.mount, func(t *testing.T) {
			name := tc.mount + "/a.txt"
			info, err := infoOf(t, fsys, tc.accessor, name)
			if err != nil {
				t.Fatalf("%s(%q) = %v, want nil", tc.accessor, name, err)
			}
			writable := info.Mode().Perm()&writableModeBits != 0
			if writable != tc.wantWritable {
				t.Errorf("%s(%q) mode = %v, writable = %t, want writable = %t", tc.accessor, name, info.Mode(), writable, tc.wantWritable)
			}
			if info.Name() != "a.txt" {
				t.Errorf("%s(%q) name = %q, want %q", tc.accessor, name, info.Name(), "a.txt")
			}
			if info.Size() != 5 {
				t.Errorf("%s(%q) size = %d, want 5", tc.accessor, name, info.Size())
			}
		})
	}
}

// infoOf reports the mode of name through the given accessor of fsys.
func infoOf(t *testing.T, fsys types.WriteFS, accessor, name string) (fs.FileInfo, error) {
	t.Helper()
	switch accessor {
	case "Stat":
		return fsys.Stat(name)
	case "Lstat":
		return fsys.Lstat(name)
	case "ReadDir entry":
		return entryInfoOf(fsys, path.Dir(name), path.Base(name))
	case "Open handle":
		return handleStatOf(fsys, name)
	default:
		t.Fatalf("unknown accessor %q", accessor)
		return nil, nil
	}
}

// entryInfoOf returns the info that fsys reports for the named entry of dir.
func entryInfoOf(fsys fs.FS, dir, name string) (fs.FileInfo, error) {
	entries, err := fs.ReadDir(fsys, dir)
	if err != nil {
		return nil, err
	}
	for _, entry := range entries {
		if entry.Name() == name {
			return entry.Info()
		}
	}
	return nil, fmt.Errorf("%s does not contain %q: %w", dir, name, fs.ErrNotExist)
}

// handleStatOf returns the info that a handle opened by fsys reports.
func handleStatOf(fsys fs.FS, name string) (fs.FileInfo, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	info, statErr := f.Stat()
	if err := f.Close(); err != nil {
		return nil, err
	}
	return info, statErr
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
    - fault:
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
    - fault:
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
    - readOnly: true
    - fault:
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
		input        string
		wantReadOnly bool
	}{
		{
			name: "bare bool",
			input: `- source: "memory://"
  options:
    - readOnly: true`,
			wantReadOnly: true,
		},
		{
			name: "bare bool disabled",
			input: `- source: "memory://"
  options:
    - readOnly: false`,
		},
		{
			name: "mapping",
			input: `- source: "memory://"
  options:
    - readOnly:
        enabled: true`,
			wantReadOnly: true,
		},
		{
			name: "mapping disabled",
			input: `- source: "memory://"
  options:
    - readOnly:
        enabled: false`,
		},
		{
			name: "empty section",
			input: `- source: "memory://"
  options:
    - readOnly:`,
		},
		{
			name: "zero fault section",
			input: `- source: "memory://"
  options:
    - fault: {}`,
		},
		{
			name: "fault with read-only",
			input: `- source: "memory://"
  options:
    - fault:
        latency: 1ms
    - readOnly:
        enabled: true`,
			wantReadOnly: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, err := ufs.New(t.Context(), tc.input)
			if err != nil {
				t.Fatalf("New(%q) = %v, want nil", tc.input, err)
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
    - noSuchDecorator: true`,
			wantErr: "cannot find a ufs file system decorator",
		},
		{
			name: "unknown mount option",
			input: `- source: "memory://"
- source: "memory://"
  mountPoint: "data"
  options:
    - noSuchDecorator: true`,
			wantErr: "cannot find a ufs file system decorator",
		},
		{
			name: "readOnly is not a bool",
			input: `- source: "memory://"
  options:
    - readOnly: sometimes`,
			wantErr: `invalid options for file system decorator "readOnly"`,
		},
		{
			name: "readOnly mapping has the wrong type",
			input: `- source: "memory://"
  options:
    - readOnly:
        enabled: [1, 2]`,
			wantErr: `invalid options for file system decorator "readOnly"`,
		},
		{
			name: "fault latency is not a duration",
			input: `- source: "memory://"
  options:
    - fault:
        latency: soon`,
			wantErr: `invalid options for file system decorator "fault"`,
		},
		{
			name: "fault is not a mapping",
			input: `- source: "memory://"
  options:
    - fault: true`,
			wantErr: `invalid options for file system decorator "fault"`,
		},
		{
			name: "decorator listed twice",
			input: `- source: "memory://"
  options:
    - readOnly: true
    - readOnly: true`,
			wantErr: "listed more than once",
		},
		{
			name:    "invalid options in a URI",
			input:   "memory://?options=%5Bnot",
			wantErr: "invalid options query parameter",
		},
		{
			name:    "unknown decorator in a URI",
			input:   "memory://?options=%5B%7BnoSuchDecorator%3A+true%7D%5D",
			wantErr: "cannot find a ufs file system decorator",
		},
		{
			name: "option name with the wrong case",
			input: `- source: "memory://"
  options:
    - readonly: true`,
			wantErr: "cannot find a ufs file system decorator",
		},
		{
			name: "source cannot be mounted",
			input: `- source: "embed://assets"
  options:
    - readOnly: true`,
			wantErr: "embed:// file systems must be created with drivers/embedfs.New",
		},
		{
			name: "mount source cannot be mounted",
			input: `- source: "memory://"
- source: "embed://assets"
  mountPoint: "data"`,
			wantErr: "embed:// file systems must be created with drivers/embedfs.New",
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

// optionsOf returns the decoded options query parameter of fsys's URI.
func optionsOf(t *testing.T, fsys types.WriteFS) string {
	t.Helper()
	u, err := fsys.URI()
	if err != nil || u == nil {
		t.Fatalf("URI() = %v, %v, want a URL", u, err)
	}
	return u.Query().Get("options")
}

func TestDecoratorsApplyInListOrder(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name: "readOnly then fault",
			input: `- source: "memory://"
  options:
    - readOnly: true
    - fault:
        latency: 1ms`,
			want: "[{readOnly: true}, {fault: {latency: 1ms}}]",
		},
		{
			name: "fault then readOnly",
			input: `- source: "memory://"
  options:
    - fault:
        latency: 1ms
    - readOnly: true`,
			want: "[{fault: {latency: 1ms}}, {readOnly: true}]",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys, err := ufs.New(t.Context(), tc.input)
			if err != nil {
				t.Fatalf("New(%q) = %v, want nil", tc.input, err)
			}
			defer ufsTesting.ValidateClose(t, fsys)()
			if got := optionsOf(t, fsys); got != tc.want {
				t.Errorf("URI() options = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestDecoratedURIRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name  string
		input string
		// wantOptions is the options query parameter of the root.
		wantOptions string
		// readOnly and writable list paths whose Create must fail with
		// fs.ErrPermission or succeed.
		readOnly []string
		writable []string
	}{
		{
			name: "read-only root",
			input: `- source: "memory://"
  options:
    - readOnly: true`,
			wantOptions: "[{readOnly: true}]",
			readOnly:    []string{"file.txt"},
		},
		{
			name: "fault then read-only root",
			input: `- source: "memory://"
  options:
    - fault:
        latency: 1ms
        latencyJitter: 1ms
        log: true
    - readOnly: true`,
			wantOptions: "[{fault: {latency: 1ms, latencyJitter: 1ms, log: true}}, {readOnly: true}]",
			readOnly:    []string{"file.txt"},
		},
		{
			name: "read-only mount",
			input: `- source: "memory://"
- source: "memory://"
  mountPoint: "data"
  options:
    - readOnly: true`,
			readOnly: []string{"data/file.txt"},
			writable: []string{"file.txt"},
		},
		{
			name:        "fstab read-only root with writable mount",
			input:       "memory:// . auto ro 0 0\nmemory:// /data auto rw 0 0",
			wantOptions: "[{readOnly: true}]",
			readOnly:    []string{"file.txt"},
			writable:    []string{"data/file.txt"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			original, err := ufs.New(t.Context(), tc.input)
			if err != nil {
				t.Fatalf("New(%q) = %v, want nil", tc.input, err)
			}
			defer ufsTesting.ValidateClose(t, original)()
			if got := optionsOf(t, original); got != tc.wantOptions {
				t.Errorf("URI() options = %q, want %q", got, tc.wantOptions)
			}
			uri := ufs.URIOrDefault(original, "")

			rebuilt, err := ufs.New(t.Context(), uri)
			if err != nil {
				t.Fatalf("New(%q) = %v, want nil", uri, err)
			}
			defer ufsTesting.ValidateClose(t, rebuilt)()
			if got := ufs.URIOrDefault(rebuilt, ""); got != uri {
				t.Errorf("rebuilt URI() = %q, want %q", got, uri)
			}

			for _, fsys := range []types.WriteFS{original, rebuilt} {
				for _, name := range tc.readOnly {
					if _, err := fsys.Create(name); !errors.Is(err, fs.ErrPermission) {
						t.Errorf("Create(%q) = %v, want fs.ErrPermission", name, err)
					}
				}
				for _, name := range tc.writable {
					f, err := fsys.Create(name)
					if err != nil {
						t.Errorf("Create(%q) = %v, want nil", name, err)
						continue
					}
					if err := f.Close(); err != nil {
						t.Errorf("Close(%q) = %v, want nil", name, err)
					}
				}
			}
		})
	}
}
