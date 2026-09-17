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

// Package osutil centralizes ufs's filesystem access in one place. It offers
// thin wrappers around the standard library os package so that the rest of ufs
// does not call os directly. Every path is cleaned before use, and the
// directory and file creation helpers apply restrictive, secure default
// permissions (0o750 for directories, 0o600 for files). Callers should prefer
// these wrappers over calling the os package directly.
package osutil

import (
	"fmt"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
)

const (
	// FilePermissions is the mode applied when writing files (owner read/write
	// only, no access for group or others).
	FilePermissions = 0o600

	// DirectoryPermissions is the mode applied when creating directories (owner
	// read/write, group read/execute, no permissions for others).
	DirectoryPermissions = 0o750
)

// NewTempDirectory creates a new temporary directory under os.TempDir() with a
// "goapp" prefix and returns its path together with a cleanup function that
// deletes the directory and everything inside it. If creation fails, an error
// is returned and the cleanup function is a no-op. The returned cleanup reports
// any error it hits while deleting, so callers can surface teardown failures.
func NewTempDirectory() (string, func() error, error) {
	tmpDir, err := os.MkdirTemp(os.TempDir(), "goapp")
	if err != nil {
		return "", func() error { return nil }, fmt.Errorf("cannot create temp directory, %w", err)
	}
	return tmpDir, func() error {
		return DeleteDirectory(tmpDir)
	}, nil
}

// Exists reports whether a path resolves to an existing file or directory.
func Exists(path string) bool {
	_, err := Stat(path)
	return err == nil
}

// DeleteDirectory removes a directory and all of its contents. Deleting a path
// that does not exist is not an error.
func DeleteDirectory(path string) error {
	if err := RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot delete directory %q, %w", path, err)
	}
	return nil
}

// TryDeleteDirectory behaves like DeleteDirectory except that failures are
// only logged (at warn level) rather than returned, for cleanup paths where the
// caller does not want deletion errors to propagate.
func TryDeleteDirectory(path string) {
	if err := DeleteDirectory(path); err != nil {
		slog.Warn("failed to delete directory", "path", path, "error", err)
	}
}

// DeleteFile removes a single file. Removing a path that does not exist is not
// an error.
func DeleteFile(path string) error {
	if err := Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot delete file %q, %w", path, err)
	}
	return nil
}

// TryDeleteFile behaves like DeleteFile except that failures are only logged
// (at warn level) rather than returned, for cleanup paths where the caller does
// not want deletion errors to propagate.
func TryDeleteFile(path string) {
	if err := DeleteFile(path); err != nil {
		slog.Warn("failed to delete file", "path", path, "error", err)
	}
}

// Mkdir creates a new directory at name using the default directory
// permissions (0o750). It does not create missing parent directories.
func Mkdir(name string) error {
	return os.Mkdir(filepath.Clean(name), DirectoryPermissions)
}

// MkdirAll creates a directory at name and any missing parents, all with the
// default directory permissions (0o750).
func MkdirAll(name string) error {
	return os.MkdirAll(filepath.Clean(name), DirectoryPermissions)
}

// Remove removes a single existing file or empty directory, mirroring
// os.Remove.
func Remove(name string) error {
	return os.Remove(filepath.Clean(name))
}

// RemoveAll removes name and, recursively, any directory contents beneath it,
// mirroring os.RemoveAll. Removing a path that does not exist is not an error.
func RemoveAll(name string) error {
	return os.RemoveAll(filepath.Clean(name))
}

// Create opens name for reading and writing, creating a new file if it does not
// already exist and truncating it if it does, mirroring os.Create.
func Create(name string) (*os.File, error) {
	return os.Create(filepath.Clean(name))
}

// ReadFile reads the entire file at name and returns its contents.
func ReadFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Clean(name))
}

// Stat returns a FileInfo describing the file or directory at name.
func Stat(name string) (os.FileInfo, error) {
	return os.Stat(filepath.Clean(name))
}

// WriteFile writes data to the file at name, creating it if it does not exist
// and truncating it if it does, using the default file permissions (0o600).
func WriteFile(name string, data []byte) error {
	return os.WriteFile(filepath.Clean(name), data, FilePermissions)
}

// ReadDir reads the named directory and returns a slice of its entries,
// sorted by filename.
func ReadDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(filepath.Clean(name))
}

// DirFS returns a read-only filesystem rooted at the directory name, mirroring
// os.DirFS.
func DirFS(name string) fs.FS {
	return os.DirFS(filepath.Clean(name))
}

// OpenRoot opens a reference that can be used to safely open paths that are
// relative to the root directory name, preventing path traversal out of that
// root, mirroring os.OpenRoot.
func OpenRoot(name string) (*os.Root, error) {
	return os.OpenRoot(filepath.Clean(name))
}

// Symlink creates newname as a symbolic link pointing to oldname.
func Symlink(oldname string, newname string) error {
	return os.Symlink(filepath.Clean(oldname), filepath.Clean(newname))
}

// Open opens name for reading-only access, mirroring os.Open.
func Open(name string) (*os.File, error) {
	return os.Open(filepath.Clean(name))
}

// CreateTemp creates a new temporary file in the specified directory dir (or
// os.TempDir() when dir is empty), with a name of the form pattern-*, for an
// exclusive read/write handle, mirroring os.CreateTemp.
func CreateTemp(dir string, pattern string) (*os.File, error) {
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	return os.CreateTemp(dir, pattern)
}

// MkdirTemp creates a new temporary directory in the specified directory dir
// (or os.TempDir() when dir is empty) and returns the resulting path, with a
// name of the form pattern-*, mirroring os.MkdirTemp. The directory is created
// with restrictive permissions.
func MkdirTemp(dir string, pattern string) (string, error) {
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	return os.MkdirTemp(dir, pattern)
}
