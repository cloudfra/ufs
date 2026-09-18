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
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestInfoDir(t *testing.T) {
	tNow := mustTime("2026-01-01T00:00:00Z")
	sysVal := struct{ x int }{x: 42}
	info := &fsInfo{
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

func TestInfo(t *testing.T) {
	tNow := time.Now()
	info := &fsInfo{
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

func TestVirtualDirEntry(t *testing.T) {
	entry := makeVirtualDirEntry("mydir")
	if entry.Name() != "mydir" {
		t.Errorf("Name() = %q, want: %q", entry.Name(), "mydir")
	}
	if !entry.IsDir() {
		t.Error("IsDir() got: false, want: true")
	}
	if entry.Type() != fs.ModeDir {
		t.Errorf("Type() got: %v, want: %v", entry.Type(), fs.ModeDir)
	}
	if entry.Mode() != fs.ModeDir {
		t.Errorf("Mode() got: %v, want: %v", entry.Mode(), fs.ModeDir)
	}
	if got, err := entry.Info(); err != nil {
		t.Errorf("Info() returned error: %v", err)
	} else if got != entry {
		t.Errorf("Info() got: %v, want: %v", got, entry)
	}
	if entry.Size() != 0 {
		t.Errorf("Size() got: %d, want: 0", entry.Size())
	}
	if entry.ModTime() != unixEpochTime {
		t.Errorf("ModTime() got: %v, want: %v", entry.ModTime(), unixEpochTime)
	}
	if entry.Sys() != nil {
		t.Errorf("Sys() got: %v, want: nil", entry.Sys())
	}
}

func TestReadDirFile(t *testing.T) {
	fsys := makeMemFS("memory:///")
	must(t, fsys.MkdirAll("a/b/c", fs.ModePerm))
	must(t, fsys.MkdirAll("d/e/f", fs.ModePerm))
	must(t, fsys.MkdirAll("g/h/i", fs.ModePerm))

	dirFile := makeReadDirFile(fsys, cwdPath)

	if bytesRead, err := dirFile.Read(nil); bytesRead != 0 || err == nil || !strings.Contains(err.Error(), "is a directory") {
		t.Errorf("Read() should return (0, 'is a directory') got (%d, %s)", bytesRead, err)
	} else {
		var pe *fs.PathError
		if !errors.As(err, &pe) {
			t.Errorf("Read() error type = %T, want *fs.PathError; err = %v", err, err)
		}
	}

	entries, err := dirFile.ReadDir(-1)
	if err != nil {
		t.Errorf("ReadDir(-1) returned error, %s", err)
	}
	wantNames := []string{"a", "d", "g"}
	gotNames := dirEntryListToNames(entries)
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("got %s, want %s diff(-want,+got):\n %v", gotNames, wantNames, diff)
	}

	stat, err := dirFile.Stat()
	if err != nil {
		t.Errorf("Stat() returned error, %s", err)
	}

	if stat.Name() != cwdPath {
		t.Errorf("Stat().Name() got %s, want '.'", stat.Name())
	}

	if stat.Mode()|fs.ModeDir == 0 {
		t.Errorf("Stat().Mode() is not a directory, got: %s", stat.Mode())
	}

	if err := dirFile.Close(); err != nil {
		t.Errorf("Close() returned error, %s", err)
	}
}
