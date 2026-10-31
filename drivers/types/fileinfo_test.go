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

package types

import (
	"io/fs"
	"testing"
	"time"
)

func TestFileInfoDir(t *testing.T) {
	t.Parallel()
	tNow := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	sysVal := struct{ x int }{x: 42}
	info := &fileInfo{
		name:    "mydir",
		size:    5000,
		mode:    fs.ModeDir | fs.ModePerm,
		modTime: tNow,
		isDir:   true,
		sys:     sysVal,
	}
	if info.Name() != "mydir" {
		t.Errorf("Name() = %q, want %q", info.Name(), "mydir")
	}
	if info.Size() != 5000 {
		t.Errorf("Size() = %d, want 5000", info.Size())
	}
	if info.Mode() != fs.ModeDir|fs.ModePerm {
		t.Errorf("Mode() = %v, want %v", info.Mode(), fs.ModeDir|fs.ModePerm)
	}
	if info.ModTime() != tNow {
		t.Errorf("ModTime() = %v, want %v", info.ModTime(), tNow)
	}
	if !info.IsDir() {
		t.Error("IsDir() = false, want true")
	}
	if info.Sys() != sysVal {
		t.Errorf("Sys() = %v, want %v", info.Sys(), sysVal)
	}
}

func TestFileInfo(t *testing.T) {
	t.Parallel()
	tNow := time.Now()
	info := &fileInfo{
		name:    "test",
		size:    0,
		mode:    0,
		modTime: tNow,
		isDir:   false,
		sys:     nil,
	}

	if info.Name() != "test" {
		t.Errorf("Name() = %q, want %q", info.Name(), "test")
	}
	if info.Size() != 0 {
		t.Errorf("Size() = %d, want %d", info.Size(), 0)
	}
	if info.Mode() != 0 {
		t.Errorf("Mode() = %d, want %d", info.Mode(), 0)
	}
	if info.ModTime() != tNow {
		t.Errorf("ModTime() = %v, want %v", info.ModTime(), tNow)
	}
	if info.IsDir() != false {
		t.Errorf("IsDir() = %v, want %v", info.IsDir(), false)
	}
	if info.Sys() != nil {
		t.Errorf("Sys() = %v, want %v", info.Sys(), nil)
	}
}

func TestNewFileInfo(t *testing.T) {
	t.Parallel()
	tNow := time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC)

	file := NewFileInfo("file.txt", 42, 0o644, tNow)
	if file.Name() != "file.txt" {
		t.Errorf("Name() = %q, want %q", file.Name(), "file.txt")
	}
	if file.Size() != 42 {
		t.Errorf("Size() = %d, want 42", file.Size())
	}
	if file.Mode() != 0o644 {
		t.Errorf("Mode() = %v, want %v", file.Mode(), fs.FileMode(0o644))
	}
	if !file.ModTime().Equal(tNow) {
		t.Errorf("ModTime() = %v, want %v", file.ModTime(), tNow)
	}
	if file.IsDir() {
		t.Error("IsDir() = true, want false")
	}
	if file.Sys() != nil {
		t.Errorf("Sys() = %v, want nil", file.Sys())
	}

	// IsDir is taken from the mode, so a caller cannot report a directory whose
	// mode says otherwise.
	dir := NewFileInfo("subdir", 0, fs.ModeDir|0o755, tNow)
	if !dir.IsDir() {
		t.Error("IsDir() = false, want true")
	}
	if !dir.Mode().IsDir() {
		t.Errorf("Mode() = %v, want a directory mode", dir.Mode())
	}
}
