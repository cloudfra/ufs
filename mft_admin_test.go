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

//go:build windows && root

// Tests in this file require Windows Administrator privileges. Run them with:
//
//	go test -tags root ./...
//
// They will fail (not skip) if MFT access is unavailable, making them
// suitable for gating on a privileged CI runner.

package ufs

import (
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/google/go-cmp/cmp"
	"golang.org/x/sys/windows"
)

// requireAdmin asserts that the current process is running with Windows
// Administrator (elevated) privileges. It calls t.Fatal immediately if not,
// giving a clear error rather than a cryptic access-denied from a syscall.
// Call this at the top of every test in this file.
func requireAdmin(tb testing.TB) {
	tb.Helper()
	token, err := windows.OpenCurrentProcessToken()
	if err != nil {
		tb.Fatalf("cannot open process token to check elevation: %v", err)
	}
	defer token.Close()
	if !token.IsElevated() {
		tb.Fatal("test requires Windows Administrator privileges; re-run as Administrator with: go test -tags root ./...")
	}
}

// ---- newMFTScanner ---------------------------------------------------------

func TestNewMFTScannerValidPath(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	scanner, err := newMFTScanner(dir)
	if err != nil {
		t.Fatalf("newMFTScanner(%q) = %v; test requires Administrator privileges", dir, err)
	}
	if scanner.volumePath == "" {
		t.Error("scanner.volumePath is empty")
	}
	if scanner.rootAbs != dir {
		t.Errorf("scanner.rootAbs = %q, want %q", scanner.rootAbs, dir)
	}
	if scanner.rootRef == 0 {
		t.Error("scanner.rootRef = 0, want non-zero MFT reference")
	}
}

// ---- MFT round-trip scan ---------------------------------------------------

func TestMFTScannerRoundTrip(t *testing.T) {
	t.Parallel()
	requireAdmin(t)

	dir := t.TempDir()
	createTestTree(t, dir)

	lfs, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer lfs.Close()

	// WalkDir baseline.
	wantPaths := walkDirFiles(t, lfs)

	// MFT scan.
	scanner, err := newMFTScanner(dir)
	if err != nil {
		t.Fatalf("newMFTScanner: %v", err)
	}
	records, err := scanner.enumSubtree()
	if err != nil {
		t.Fatalf("enumSubtree: %v", err)
	}
	gotPaths := scanner.buildPaths(records)
	sort.Strings(gotPaths)

	if diff := cmp.Diff(wantPaths, gotPaths); diff != "" {
		t.Errorf("MFT scan vs WalkDir mismatch (-want +got):\n%s", diff)
	}
}

// TestMFTScannerSubtreeIsolation verifies that the subtree filter correctly
// excludes files outside the scanner's root directory.
func TestMFTScannerSubtreeIsolation(t *testing.T) {
	t.Parallel()
	requireAdmin(t)

	// Two sibling directories on the same volume.
	rootA := t.TempDir()
	rootB := t.TempDir()

	if err := os.WriteFile(filepath.Join(rootA, "in-a.txt"), []byte("a"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rootB, "in-b.txt"), []byte("b"), 0o644); err != nil {
		t.Fatal(err)
	}

	scanner, err := newMFTScanner(rootA)
	if err != nil {
		t.Fatalf("newMFTScanner: %v", err)
	}
	records, err := scanner.enumSubtree()
	if err != nil {
		t.Fatalf("enumSubtree: %v", err)
	}
	paths := scanner.buildPaths(records)

	for _, p := range paths {
		if p == "in-b.txt" {
			t.Error("scanner rooted at rootA returned file from rootB")
		}
	}
	found := false
	for _, p := range paths {
		if p == "in-a.txt" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected in-a.txt in MFT scan of rootA; got: %v", paths)
	}
}

// ---- ForEachFilename via MFT (not fallback) --------------------------------

func TestLocalFSForEachFilenameMFT(t *testing.T) {
	t.Parallel()
	requireAdmin(t)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	// Verify mftScanPaths succeeds (i.e. MFT is actually used, not fallback).
	paths, err := fsys.mftScanPaths(cwdPath)
	if err != nil {
		t.Fatalf("mftScanPaths() = %v; expected MFT to be available with admin", err)
	}
	sort.Strings(paths)

	want := []string{"a.txt", "sub/b.txt"}
	if diff := cmp.Diff(want, paths); diff != "" {
		t.Errorf("mftScanPaths() mismatch (-want +got):\n%s", diff)
	}
}

// TestLocalFSForEachFilenameMFTSubdir verifies path filtering for subdirectory scans.
func TestLocalFSForEachFilenameMFTSubdir(t *testing.T) {
	t.Parallel()
	requireAdmin(t)

	dir := t.TempDir()
	createTestTree(t, dir)

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	paths, err := fsys.mftScanPaths("a")
	if err != nil {
		t.Fatalf("mftScanPaths(\"a\") = %v", err)
	}
	sort.Strings(paths)

	// Only files under "a/" should appear, with full FS-relative paths.
	want := []string{"a/b/c/three.txt", "a/b/two.txt", "a/one.txt"}
	if diff := cmp.Diff(want, paths); diff != "" {
		t.Errorf("mftScanPaths(\"a\") mismatch (-want +got):\n%s", diff)
	}
}

// ---- ForEachFileInfo via MFT -----------------------------------------------

func TestLocalFSForEachFileInfoMFT(t *testing.T) {
	t.Parallel()
	requireAdmin(t)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"a.txt", filepath.Join("sub", "b.txt")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	// Assert MFT is available.
	if _, err := fsys.mftScanPaths(cwdPath); err != nil {
		t.Fatalf("mftScanPaths() = %v; expected MFT to be available with admin", err)
	}

	var names []string
	if err := fsys.ForEachFileInfo(cwdPath, func(info fs.FileInfo) error {
		names = append(names, info.Name())
		return nil
	}); err != nil {
		t.Fatalf("ForEachFileInfo() = %v", err)
	}
	sort.Strings(names)

	want := []string{"a.txt", "b.txt"}
	if diff := cmp.Diff(want, names); diff != "" {
		t.Errorf("ForEachFileInfo() name mismatch (-want +got):\n%s", diff)
	}
}

// ---- ListFilenames via MFT -------------------------------------------------

func TestLocalFSListFilenamesMFT(t *testing.T) {
	t.Parallel()
	requireAdmin(t)

	dir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"b.txt", "a.txt", filepath.Join("sub", "c.txt")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	fsys, err := makeLocalFS(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	// Assert MFT is available.
	if _, err := fsys.mftScanPaths(cwdPath); err != nil {
		t.Fatalf("mftScanPaths() = %v; expected MFT to be available with admin", err)
	}

	got, err := fsys.ListFilenames(cwdPath)
	if err != nil {
		t.Fatalf("ListFilenames() = %v", err)
	}

	want := []string{"a.txt", "b.txt", "sub/c.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ListFilenames() mismatch (-want +got):\n%s", diff)
	}
}
