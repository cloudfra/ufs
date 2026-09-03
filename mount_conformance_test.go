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
	"bytes"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"
)

// mountSetup holds the result of mounting a file system for testing.
type mountSetup struct {
	mountDir string
}

// mountBackend describes a host-mount backend for conformance testing.
type mountBackend struct {
	name  string
	mount func(t *testing.T, fsys ReadFS) mountSetup
	skip  func(t *testing.T)
}

// mountBackends is populated by platform-specific init() functions in
// mount_conformance_linux_test.go and mount_conformance_windows_test.go.
var mountBackends []mountBackend

// mountConformanceRun runs fn for each registered mount backend as a subtest.
func mountConformanceRun(t *testing.T, fn func(t *testing.T, backend mountBackend)) {
	t.Helper()
	if len(mountBackends) == 0 {
		t.Skip("no mount backends registered on this platform")
	}
	for _, backend := range mountBackends {
		t.Run(backend.name, func(t *testing.T) {
			t.Parallel()
			backend.skip(t)
			fn(t, backend)
		})
	}
}

func TestMountConformanceReadFile(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		srcDir := t.TempDir()
		if err := os.WriteFile(filepath.Join(srcDir, "hello.txt"), []byte("world"), 0o644); err != nil {
			t.Fatal(err)
		}

		fsys, err := New(t.Context(), srcDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fsys.Close() })

		m := backend.mount(t, fsys)

		data, err := os.ReadFile(filepath.Join(m.mountDir, "hello.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "world" {
			t.Errorf("ReadFile = %q, want %q", data, "world")
		}
	})
}

func TestMountConformanceStat(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		srcDir := t.TempDir()
		content := []byte("test content")
		if err := os.WriteFile(filepath.Join(srcDir, "file.txt"), content, 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(filepath.Join(srcDir, "subdir"), 0o755); err != nil {
			t.Fatal(err)
		}

		fsys, err := New(t.Context(), srcDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fsys.Close() })

		m := backend.mount(t, fsys)

		fi, err := os.Stat(filepath.Join(m.mountDir, "file.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if fi.IsDir() {
			t.Error("file.txt reported as directory")
		}
		if fi.Size() != int64(len(content)) {
			t.Errorf("Size = %d, want %d", fi.Size(), len(content))
		}

		di, err := os.Stat(filepath.Join(m.mountDir, "subdir"))
		if err != nil {
			t.Fatal(err)
		}
		if !di.IsDir() {
			t.Error("subdir not reported as directory")
		}
	})
}

func TestMountConformanceReadDir(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		srcDir := t.TempDir()
		for _, name := range []string{"a.txt", "b.txt", "c.txt"} {
			if err := os.WriteFile(filepath.Join(srcDir, name), []byte("data"), 0o644); err != nil {
				t.Fatal(err)
			}
		}
		if err := os.MkdirAll(filepath.Join(srcDir, "sub"), 0o755); err != nil {
			t.Fatal(err)
		}

		fsys, err := New(t.Context(), srcDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fsys.Close() })

		m := backend.mount(t, fsys)

		entries, err := os.ReadDir(m.mountDir)
		if err != nil {
			t.Fatal(err)
		}
		var names []string
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		want := []string{"a.txt", "b.txt", "c.txt", "sub"}
		if len(names) != len(want) {
			t.Fatalf("ReadDir got %v, want %v", names, want)
		}
		for i, n := range names {
			if n != want[i] {
				t.Errorf("entry[%d] = %q, want %q", i, n, want[i])
			}
		}
	})
}

func TestMountConformanceStatNotExist(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		srcDir := t.TempDir()

		fsys, err := New(t.Context(), srcDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fsys.Close() })

		m := backend.mount(t, fsys)

		_, err = os.Stat(filepath.Join(m.mountDir, "nonexistent.txt"))
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Stat(nonexistent) = %v, want ErrNotExist", err)
		}
	})
}

func TestMountConformanceNestedRead(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		srcDir := t.TempDir()
		nested := filepath.Join(srcDir, "a", "b")
		if err := os.MkdirAll(nested, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(nested, "deep.txt"), []byte("deep"), 0o644); err != nil {
			t.Fatal(err)
		}

		fsys, err := New(t.Context(), srcDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fsys.Close() })

		m := backend.mount(t, fsys)

		data, err := os.ReadFile(filepath.Join(m.mountDir, "a", "b", "deep.txt"))
		if err != nil {
			t.Fatal(err)
		}
		if string(data) != "deep" {
			t.Errorf("ReadFile = %q, want %q", data, "deep")
		}
	})
}

func TestMountConformanceLargeFile(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		srcDir := t.TempDir()
		size := 256 * 1024
		content := make([]byte, size)
		for i := range content {
			content[i] = byte(i % 251)
		}
		if err := os.WriteFile(filepath.Join(srcDir, "large.bin"), content, 0o644); err != nil {
			t.Fatal(err)
		}

		fsys, err := New(t.Context(), srcDir)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fsys.Close() })

		m := backend.mount(t, fsys)

		data, err := os.ReadFile(filepath.Join(m.mountDir, "large.bin"))
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != size {
			t.Fatalf("len = %d, want %d", len(data), size)
		}
		for i, b := range data {
			if b != byte(i%251) {
				t.Fatalf("byte[%d] = %d, want %d", i, b, byte(i%251))
			}
		}
	})
}

func TestMountConformanceNullFS(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		fsys, err := New(t.Context(), "null:")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fsys.Close() })

		m := backend.mount(t, fsys)

		entries, err := os.ReadDir(m.mountDir)
		if err != nil {
			t.Fatalf("ReadDir: %v", err)
		}
		if len(entries) != 0 {
			t.Errorf("ReadDir returned %d entries, want 0", len(entries))
		}
	})
}

func TestMountConformanceNestedOverlay(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		uri, err := CreateURI("memory://", map[string]string{
			"cache": "null:",
		})
		if err != nil {
			t.Fatal(err)
		}
		fsys, err := New(t.Context(), uri)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = fsys.Close() })

		m := backend.mount(t, fsys)

		cacheDir := filepath.Join(m.mountDir, "cache")
		fi, err := os.Stat(cacheDir)
		if err != nil {
			t.Fatalf("Stat cache: %v", err)
		}
		if !fi.IsDir() {
			t.Errorf("cache IsDir = false, want true")
		}
	})
}

func TestMountConformanceRsyncArchive(t *testing.T) {
	t.Parallel()
	mountConformanceRun(t, func(t *testing.T, backend mountBackend) {
		archivePath := filepath.Join("testing", "testassets", "archives", "testassets.tar.gz")
		archiveFS, err := New(t.Context(), "archive://"+archivePath)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = archiveFS.Close() })

		m := backend.mount(t, archiveFS)

		memFS, err := New(t.Context(), "memory:")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = memFS.Close() }()

		if err := Rsync(os.DirFS(m.mountDir), memFS, "."); err != nil {
			t.Fatalf("Rsync: %v", err)
		}

		wantFiles := loadTestAssets(t)
		for filePath, wantData := range wantFiles {
			got, err := fs.ReadFile(memFS, filePath)
			if err != nil {
				t.Errorf("ReadFile(%q): %v", filePath, err)
				continue
			}
			if !bytes.Equal(got, wantData) {
				t.Errorf("ReadFile(%q): got %d bytes, want %d bytes", filePath, len(got), len(wantData))
			}
		}
	})
}
