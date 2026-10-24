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

package main

import (
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testDirectoryPermission = 0o750
	testFilePermission      = 0o600
)

func TestRunMemoryFS(t *testing.T) {
	t.Parallel()
	if err := run("memory:", "ls"); err != nil {
		t.Fatalf("run(memory:, ls) = %v, want nil", err)
	}
}

func TestRunNullFS(t *testing.T) {
	t.Parallel()
	if err := run("null://", "ls"); err != nil {
		t.Fatalf("run(null://, ls) = %v, want nil", err)
	}
}

func TestRunLocalDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(filepath.Clean(sub), testDirectoryPermission); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root.txt", filepath.Join("sub", "nested.txt")} {
		if err := os.WriteFile(filepath.Clean(filepath.Join(dir, name)), []byte("data"), testFilePermission); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(dir, "ls"); err != nil {
		t.Fatalf("run(%q, ls) = %v, want nil", dir, err)
	}
}

// TestRunNestedMount checks that a URI that mounts another file system inside
// the mount the agent runs against is accepted, which is how a caller exposes
// a writable scratch mount over a read-only source.
func TestRunNestedMount(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Clean(filepath.Join(dir, "a.txt")), []byte("data"), testFilePermission); err != nil {
		t.Fatal(err)
	}
	uri := "memory:?data=" + url.QueryEscape("file://"+filepath.ToSlash(dir))
	if err := run(uri, "ls"); err != nil {
		t.Fatalf("run(%q, ls) = %v, want nil", uri, err)
	}
}

// TestRunDecoratorOption checks that the decorators blank-imported by
// drivers/all are usable through the URI flag, here a read-only mount.
func TestRunDecoratorOption(t *testing.T) {
	t.Parallel()
	uri := "memory://?options=" + url.QueryEscape("[{readOnly: true}]")
	if err := run(uri, "ls"); err != nil {
		t.Fatalf("run(%q, ls) = %v, want nil", uri, err)
	}
}

func TestRunErrors(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		uri     string
		wantErr string
	}{
		{
			name:    "no driver for the scheme",
			uri:     "no-such-scheme://host/dir",
			wantErr: "check that the driver package is imported",
		},
		{
			name:    "missing local directory",
			uri:     "/no/such/directory/exists",
			wantErr: `failed to mount "/no/such/directory/exists" with driver "local"`,
		},
		{
			name:    "mount inside the URI cannot be opened",
			uri:     "memory:?data=file:///no/such/directory/exists",
			wantErr: `failed to mount "file:///no/such/directory/exists" with driver "local"`,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := run(tc.uri, "ls")
			if err == nil {
				t.Fatalf("run(%q, ls) = nil, want an error", tc.uri)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("run(%q, ls) = %v, want an error containing %q", tc.uri, err, tc.wantErr)
			}
		})
	}
}
