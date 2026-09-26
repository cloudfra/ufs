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

// Package testing provides conformance tests that any file system driver can
// run against itself. The tests take an [fs.FS] and use the optional
// io/fs interfaces ([fs.StatFS], [fs.ReadFileFS], [fs.ReadDirFS],
// [fs.ReadLinkFS]) when the file system implements them, falling back to the
// io/fs helper functions otherwise. Write, remove and close behavior is
// exercised through the small capability interfaces below; a test whose
// capability the file system lacks is skipped.
//
// Write tests are generic over the file type returned by Create, so a driver
// whose Create returns ufs.File runs them as, for example:
//
//	driverTesting.Run[ufs.File](t, func(tb testing.TB) fs.FS { return newFS(tb) })
package testing

import (
	"io"
	"io/fs"
	"testing"
)

// Factory returns a new, empty file system for a test. It should register any
// cleanup (for example closing the file system) with tb.Cleanup; closing an
// already-closed file system must be safe.
type Factory func(tb testing.TB) fs.FS

// WritableFile is a file returned by [CreateFS.Create].
type WritableFile interface {
	fs.File
	io.Writer
}

// CreateFS is a file system that can create files. F is the concrete file
// type its Create method returns, such as ufs.File.
type CreateFS[F WritableFile] interface {
	fs.FS
	// Create opens a new writable file at name, replacing any existing file.
	Create(name string) (F, error)
}

// MkdirAllFS is a file system that can create directories.
type MkdirAllFS interface {
	fs.FS
	// MkdirAll creates the directory at name and any missing parents.
	MkdirAll(name string, perm fs.FileMode) error
}

// RemoveFS is a file system that can delete files and directories.
type RemoveFS interface {
	fs.FS
	// Remove deletes the file or empty directory at name.
	Remove(name string) error
	// RemoveAll removes name and everything beneath it.
	RemoveAll(name string) error
}

// Run runs every conformance test in this package against file systems
// produced by newFS.
func Run[F WritableFile](t *testing.T, newFS Factory) {
	t.Helper()
	t.Run("RoundTrip", func(t *testing.T) { RoundTrip[F](t, newFS) })
	t.Run("InvalidPaths", func(t *testing.T) { InvalidPaths[F](t, newFS) })
	t.Run("Close", func(t *testing.T) { Close[F](t, newFS) })
	t.Run("MkdirAll", func(t *testing.T) { MkdirAll(t, newFS) })
	t.Run("ReadDir", func(t *testing.T) { ReadDir(t, newFS) })
	t.Run("CreateAndRead", func(t *testing.T) { CreateAndRead[F](t, newFS) })
	t.Run("ReadFile", func(t *testing.T) { ReadFile[F](t, newFS) })
	t.Run("DirFileConflicts", func(t *testing.T) { DirFileConflicts[F](t, newFS) })
}

// requireMkdirAll returns fsys as a MkdirAllFS, skipping the test if it is
// not one.
func requireMkdirAll(tb testing.TB, fsys fs.FS) MkdirAllFS {
	tb.Helper()
	mfs, ok := fsys.(MkdirAllFS)
	if !ok {
		tb.Skipf("%T does not implement MkdirAll", fsys)
	}
	return mfs
}

// writeFile creates name in fsys containing data.
func writeFile[F WritableFile](tb testing.TB, fsys CreateFS[F], name string, data string) {
	tb.Helper()
	f, err := fsys.Create(name)
	if err != nil {
		tb.Fatalf("Create(%q) = %v", name, err)
	}
	if n, err := io.WriteString(f, data); err != nil || n != len(data) {
		tb.Fatalf("WriteString(%q) = (%d, %v), want (%d, nil)", name, n, err, len(data))
	}
	if err := f.Close(); err != nil {
		tb.Fatalf("Close(%q) = %v", name, err)
	}
}

// readFile reads name through fsys's ReadFile method when it implements
// fs.ReadFileFS, checking that it agrees with reading through Open.
func readFile(tb testing.TB, fsys fs.FS, name string) ([]byte, error) {
	tb.Helper()
	opened, openErr := readViaOpen(fsys, name)
	rfs, ok := fsys.(fs.ReadFileFS)
	if !ok {
		return opened, openErr
	}
	data, err := rfs.ReadFile(name)
	if (err == nil) != (openErr == nil) || string(data) != string(opened) {
		tb.Errorf("ReadFile(%q) = (%q, %v) disagrees with Open+ReadAll = (%q, %v)", name, data, err, opened, openErr)
	}
	return data, err
}

func readViaOpen(fsys fs.FS, name string) ([]byte, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	data, err := io.ReadAll(f)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return data, err
}
