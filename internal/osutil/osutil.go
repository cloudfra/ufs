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

// Package osutil provides thin wrappers around package os that clean the
// path argument before use. It is shared by ufs and its subpackages.
package osutil

import (
	"io/fs"
	"os"
	"path/filepath"
)

const (
	// DefaultFilePermissions is the permission used by [WriteFile].
	DefaultFilePermissions = 0o600
	// DefaultDirectoryPermissions is the permission used by [Mkdir] and [MkdirAll].
	DefaultDirectoryPermissions = 0o750
)

// Mkdir creates the directory name with [DefaultDirectoryPermissions].
func Mkdir(name string) error {
	return os.Mkdir(filepath.Clean(name), DefaultDirectoryPermissions)
}

// MkdirAll creates the directory name and any parents with [DefaultDirectoryPermissions].
func MkdirAll(name string) error {
	return os.MkdirAll(filepath.Clean(name), DefaultDirectoryPermissions)
}

// Remove removes the file or empty directory name.
func Remove(name string) error {
	return os.Remove(filepath.Clean(name))
}

// RemoveAll removes name and any children it contains.
func RemoveAll(name string) error {
	return os.RemoveAll(filepath.Clean(name))
}

// Create creates or truncates the file name.
func Create(name string) (*os.File, error) {
	return os.Create(filepath.Clean(name))
}

// ReadFile reads the file name.
func ReadFile(name string) ([]byte, error) {
	return os.ReadFile(filepath.Clean(name))
}

// ReadDir reads the directory name.
func ReadDir(name string) ([]os.DirEntry, error) {
	return os.ReadDir(filepath.Clean(name))
}

// Stat returns the file info for name.
func Stat(name string) (os.FileInfo, error) {
	return os.Stat(filepath.Clean(name))
}

// WriteFile writes data to name with [DefaultFilePermissions].
func WriteFile(name string, data []byte) error {
	return os.WriteFile(filepath.Clean(name), data, DefaultFilePermissions)
}

// DirFS returns a file system rooted at the directory name.
func DirFS(name string) fs.FS {
	return os.DirFS(filepath.Clean(name))
}

// OpenRoot opens the directory name as an [os.Root].
func OpenRoot(name string) (*os.Root, error) {
	return os.OpenRoot(filepath.Clean(name))
}

// Symlink creates newname as a symbolic link to oldname.
func Symlink(oldname string, newname string) error {
	return os.Symlink(filepath.Clean(oldname), filepath.Clean(newname))
}

// Open opens the file name for reading.
func Open(name string) (*os.File, error) {
	return os.Open(filepath.Clean(name))
}

// CreateTemp creates a temporary file in dir (or the default temp directory if
// dir is empty).
func CreateTemp(dir string, pattern string) (*os.File, error) {
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	return os.CreateTemp(dir, pattern)
}

// MkdirTemp creates a temporary directory in dir (or the default temp
// directory if dir is empty).
func MkdirTemp(dir string, pattern string) (string, error) {
	if dir != "" {
		dir = filepath.Clean(dir)
	}
	return os.MkdirTemp(dir, pattern)
}
