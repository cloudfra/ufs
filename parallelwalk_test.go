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
	"archive/zip"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/google/go-cmp/cmp"
)

func TestParallelWalk(t *testing.T) {
	t.Parallel()
	fsys := setupListFS(t)

	var mu sync.Mutex
	var got []string
	err := ParallelWalk(fsys, cwdPath, WalkArgs{}, func(name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("ParallelWalk() = %v, want nil", err)
	}
	sort.Strings(got)
	want := []string{"a.txt", "dir/b.txt", "dir/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParallelWalk() mismatch (-want +got):\n%s", diff)
	}
}

func TestParallelWalkMatchesWalk(t *testing.T) {
	t.Parallel()
	lfs := buildTree(t, 500, 10, 64)

	var serialFiles []string
	if err := Walk(lfs, cwdPath, WalkArgs{}, func(name string) error {
		serialFiles = append(serialFiles, name)
		return nil
	}); err != nil {
		t.Fatalf("Walk() = %v", err)
	}

	var mu sync.Mutex
	var parallelFiles []string
	if err := ParallelWalk(lfs, cwdPath, WalkArgs{}, func(name string) error {
		mu.Lock()
		parallelFiles = append(parallelFiles, name)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("ParallelWalk() = %v", err)
	}

	sort.Strings(serialFiles)
	sort.Strings(parallelFiles)
	if diff := cmp.Diff(serialFiles, parallelFiles); diff != "" {
		t.Errorf("ParallelWalk produced different files than Walk (-walk +parallel):\n%s", diff)
	}
}

func TestParallelWalkCallbackError(t *testing.T) {
	t.Parallel()
	fsys := setupListFS(t)
	sentinel := errors.New("stop")

	var count atomic.Int32
	err := ParallelWalk(fsys, cwdPath, WalkArgs{}, func(_ string) error {
		count.Add(1)
		return sentinel
	})
	if !errors.Is(err, sentinel) {
		t.Errorf("ParallelWalk() = %v, want sentinel error", err)
	}
}

func TestParallelWalkExcludeDirectory(t *testing.T) {
	t.Parallel()
	fsys := setupListFS(t)

	var mu sync.Mutex
	var got []string
	err := ParallelWalk(fsys, cwdPath, WalkArgs{ExcludeDirectory: []string{"dir"}}, func(name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("ParallelWalk() = %v, want nil", err)
	}
	want := []string{"a.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParallelWalk() ExcludeDirectory mismatch (-want +got):\n%s", diff)
	}
}

func TestParallelWalkExcludeDirectoryGlob(t *testing.T) {
	t.Parallel()
	fsys := setupListFS(t)

	var mu sync.Mutex
	var got []string
	err := ParallelWalk(fsys, cwdPath, WalkArgs{ExcludeDirectory: []string{"d*"}}, func(name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("ParallelWalk() = %v, want nil", err)
	}
	want := []string{"a.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParallelWalk() ExcludeDirectory glob mismatch (-want +got):\n%s", diff)
	}
}

func TestParallelWalkIncludeMountedArchiveDefault(t *testing.T) {
	t.Parallel()
	nfs := setupNestFSWithArchive(t)

	var mu sync.Mutex
	var got []string
	err := ParallelWalk(nfs, cwdPath, WalkArgs{}, func(name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("ParallelWalk() = %v, want nil", err)
	}
	sort.Strings(got)
	want := []string{"data.zip", "readme.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParallelWalk() default (no archives) mismatch (-want +got):\n%s", diff)
	}
}

func TestParallelWalkIncludeMountedArchive(t *testing.T) {
	t.Parallel()
	nfs := setupNestFSWithArchive(t)

	var mu sync.Mutex
	var got []string
	err := ParallelWalk(nfs, cwdPath, WalkArgs{IncludeMountedArchive: true}, func(name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("ParallelWalk() = %v, want nil", err)
	}
	sort.Strings(got)
	want := []string{"data.zip", "data.zip.d/inside.txt", "readme.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParallelWalk() IncludeMountedArchive mismatch (-want +got):\n%s", diff)
	}
}

func TestParallelWalkRegularSubdirNotSkipped(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()

	if err := os.MkdirAll(filepath.Join(dir, "subdir"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "subdir", "nested.txt"), []byte("nested"), 0o644); err != nil {
		t.Fatal(err)
	}

	zipPath := filepath.Join(dir, "data.zip")
	zf, err := os.Create(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	zw := zip.NewWriter(zf)
	w, err := zw.Create("inside.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(w, "content"); err != nil {
		t.Fatal(err)
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := zf.Close(); err != nil {
		t.Fatal(err)
	}

	lfs, err := newLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	nfs := makeNestFS(t.Context(), lfs)
	t.Cleanup(func() { nfs.Close() })

	var mu sync.Mutex
	var got []string
	err = ParallelWalk(nfs, cwdPath, WalkArgs{}, func(name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("ParallelWalk() = %v, want nil", err)
	}
	sort.Strings(got)
	want := []string{"data.zip", "subdir/nested.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParallelWalk() regular subdir mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildDeviceSemaphores(t *testing.T) {
	t.Parallel()
	deviceMap := map[string]deviceInfo{
		".":     {name: "nvme0n1", deviceType: "nvme", threadCount: 2},
		"mnt/c": {name: "hda1", deviceType: "hdd", threadCount: 1},
		"tmp":   {name: "tmpfs", deviceType: "memory", threadCount: 4},
	}
	sems := buildDeviceSemaphores(deviceMap)

	if cap(sems["nvme0n1"]) != 2 {
		t.Errorf("nvme semaphore capacity = %d, want 2", cap(sems["nvme0n1"]))
	}
	if cap(sems["hda1"]) != 1 {
		t.Errorf("hdd semaphore capacity = %d, want 1", cap(sems["hda1"]))
	}
	if cap(sems["tmpfs"]) != 4 {
		t.Errorf("tmpfs semaphore capacity = %d, want 4", cap(sems["tmpfs"]))
	}
}

func TestBuildDeviceSemaphoresZeroThreadCount(t *testing.T) {
	t.Parallel()
	deviceMap := map[string]deviceInfo{
		".": {name: "unknown", deviceType: "unknown", threadCount: 0},
	}
	sems := buildDeviceSemaphores(deviceMap)
	if cap(sems["unknown"]) != 1 {
		t.Errorf("zero threadCount semaphore capacity = %d, want 1", cap(sems["unknown"]))
	}
}

func TestDeviceInfoForPath(t *testing.T) {
	t.Parallel()
	m := map[string]deviceInfo{
		".":     {name: "nvme0n1", deviceType: "nvme", threadCount: 2},
		"mnt/c": {name: "hda1", deviceType: "hdd", threadCount: 1},
		"tmp":   {name: "tmpfs", deviceType: "memory", threadCount: 4},
	}

	tests := []struct {
		path     string
		wantName string
	}{
		{".", "nvme0n1"},
		{"foo", "nvme0n1"},
		{"mnt/c", "hda1"},
		{"mnt/c/subdir", "hda1"},
		{"mnt/c/deep/nested", "hda1"},
		{"tmp", "tmpfs"},
		{"tmp/scratch", "tmpfs"},
		{"mnt", "nvme0n1"},
	}
	for _, tc := range tests {
		t.Run(tc.path, func(t *testing.T) {
			got := deviceInfoForPath(m, tc.path)
			if got.name != tc.wantName {
				t.Errorf("deviceInfoForPath(%q) = %q, want %q", tc.path, got.name, tc.wantName)
			}
		})
	}
}

func TestParallelWalkDeviceSemaphoreBounds(t *testing.T) {
	t.Parallel()

	lfs := buildTree(t, 200, 5, 32)

	deviceMap := getDeviceInfoOrDefault(lfs)
	sems := buildDeviceSemaphores(deviceMap)

	for name, sem := range sems {
		if cap(sem) < 1 {
			t.Errorf("device %q semaphore has capacity %d, want >= 1", name, cap(sem))
		}
	}

	var mu sync.Mutex
	var got []string
	if err := ParallelWalk(lfs, cwdPath, WalkArgs{}, func(name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		return nil
	}); err != nil {
		t.Fatalf("ParallelWalk() = %v", err)
	}

	if len(got) != 200 {
		t.Errorf("ParallelWalk visited %d files, want 200", len(got))
	}
}

func TestParallelWalkEmptyFS(t *testing.T) {
	t.Parallel()
	fsys, err := newMemFS("memory://empty")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fsys.Close() })

	var got []string
	if err := ParallelWalk(fsys, cwdPath, WalkArgs{}, func(name string) error {
		got = append(got, name)
		return nil
	}); err != nil {
		t.Fatalf("ParallelWalk on empty FS = %v, want nil", err)
	}
	if len(got) != 0 {
		t.Errorf("ParallelWalk on empty FS visited %d files, want 0", len(got))
	}
}

func TestParallelWalkExcludeDirectoryNil(t *testing.T) {
	t.Parallel()
	fsys := setupListFS(t)

	var mu sync.Mutex
	var got []string
	err := ParallelWalk(fsys, cwdPath, WalkArgs{ExcludeDirectory: nil}, func(name string) error {
		mu.Lock()
		got = append(got, name)
		mu.Unlock()
		return nil
	})
	if err != nil {
		t.Fatalf("ParallelWalk() = %v, want nil", err)
	}
	sort.Strings(got)
	want := []string{"a.txt", "dir/b.txt", "dir/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ParallelWalk() nil ExcludeDirectory mismatch (-want +got):\n%s", diff)
	}
}

// --- Benchmarks ---

func BenchmarkParallelWalkSmall(b *testing.B) {
	lfs := buildTree(b, 100, 5, 512)

	b.ResetTimer()
	for b.Loop() {
		var mu sync.Mutex
		count := 0
		if err := ParallelWalk(lfs, ".", WalkArgs{}, func(_ string) error {
			mu.Lock()
			count++
			mu.Unlock()
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParallelWalkMedium(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		var mu sync.Mutex
		count := 0
		if err := ParallelWalk(lfs, ".", WalkArgs{}, func(_ string) error {
			mu.Lock()
			count++
			mu.Unlock()
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkParallelWalkWithExcludes(b *testing.B) {
	lfs := buildTree(b, 10_000, 128, 128)

	b.ResetTimer()
	for b.Loop() {
		var mu sync.Mutex
		count := 0
		if err := ParallelWalk(lfs, ".", WalkArgs{ExcludeDirectory: []string{"d1", "d2"}}, func(_ string) error {
			mu.Lock()
			count++
			mu.Unlock()
			return nil
		}); err != nil {
			b.Fatal(err)
		}
	}
}
