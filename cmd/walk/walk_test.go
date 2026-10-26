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
	"archive/zip"
	"bytes"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/google/go-cmp/cmp"
)

const (
	testDirectoryPermission = 0o750
	testFilePermission      = 0o600
)

func TestRunLocalDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Clean(filepath.Join(dir, name)), []byte("data"), testFilePermission); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(io.Discard, dir); err != nil {
		t.Fatalf("run(%q) = %v, want nil", dir, err)
	}
}

func TestRunLocalDirNested(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(filepath.Clean(sub), testDirectoryPermission); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root.txt", filepath.Join("sub", "nested.txt")} {
		if err := os.WriteFile(filepath.Clean(filepath.Join(dir, name)), []byte("content"), testFilePermission); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(io.Discard, dir); err != nil {
		t.Fatalf("run(%q) = %v, want nil", dir, err)
	}
}

func TestRunEmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := run(io.Discard, dir); err != nil {
		t.Fatalf("run(%q) = %v, want nil", dir, err)
	}
}

func TestRunNullFS(t *testing.T) {
	t.Parallel()
	if err := run(io.Discard, "null://"); err != nil {
		t.Fatalf("run(null://) = %v, want nil", err)
	}
}

func TestRunInvalidPath(t *testing.T) {
	t.Parallel()
	if err := run(io.Discard, "/no/such/directory/exists"); err == nil {
		t.Fatal("run(nonexistent) = nil, want error")
	}
}

// TestRunLocalDirReportsAbsolutePaths covers the reported names for a local
// directory, which is reachable outside the virtual file system and is
// therefore reported as an absolute path.
func TestRunLocalDirReportsAbsolutePaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := osutil.MkdirAll(filepath.Clean(sub)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root.txt", filepath.Join("sub", "nested.txt")} {
		if err := os.WriteFile(filepath.Clean(filepath.Join(dir, name)), []byte("content"), testFilePermission); err != nil {
			t.Fatal(err)
		}
	}

	var out bytes.Buffer
	if err := run(&out, dir); err != nil {
		t.Fatalf("run(%q) = %v, want nil", dir, err)
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	sort.Strings(got)
	want := []string{"root.txt", filepath.Join("sub", "nested.txt")}
	if len(got) != len(want) {
		t.Fatalf("run(%q) reported %d names, want %d:\n%s", dir, len(got), len(want), out.String())
	}
	for i, line := range got {
		if !filepath.IsAbs(line) {
			t.Errorf("run(%q) reported %q, want an absolute path for %q", dir, line, want[i])
			continue
		}
		rel, err := filepath.Rel(dir, line)
		if err != nil || rel != want[i] {
			t.Errorf("run(%q) reported %q, want %q below %q", dir, line, want[i], dir)
		}
	}
}

// TestRunArchiveReportsVirtualPaths covers a file that has no path outside the
// virtual file system: an archive's contents. It must be reported as its path
// within the virtual file system, not with a scheme no other tool understands.
func TestRunArchiveReportsVirtualPaths(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	zipPath := filepath.Join(dir, "data.zip")
	zf, err := osutil.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("inner.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "inner"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	var out bytes.Buffer
	if err := run(&out, zipPath); err != nil {
		t.Fatalf("run(%q) = %v, want nil", zipPath, err)
	}
	got := strings.Split(strings.TrimSpace(out.String()), "\n")
	want := []string{"inner.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("run(%q) output mismatch (-want +got):\n%s", zipPath, diff)
	}
	for _, line := range got {
		if !strings.HasPrefix(line, "ufs://") {
			continue
		}
		t.Errorf("run(%q) reported %q with the ufs:// scheme, want a path inside the virtual file system", zipPath, line)
	}
}
