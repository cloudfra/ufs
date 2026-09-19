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

package file

import (
	"io/fs"
	"testing"
	"time"
)

func mustTime(tb testing.TB, s string) time.Time {
	tb.Helper()
	val, err := time.Parse(time.RFC3339, s)
	if err != nil {
		tb.Fatal(err)
	}
	return val
}

func TestInfoDir(t *testing.T) {
	tNow := mustTime(t, "2026-01-01T00:00:00Z")
	sysVal := struct{ x int }{x: 42}
	info := New(Params{
		Name:    "mydir",
		Size:    5000,
		Mode:    fs.ModeDir | fs.ModePerm,
		ModTime: tNow,
		IsDir:   true,
		Sys:     sysVal,
	})
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
	info := New(Params{
		Name:    "test",
		ModTime: tNow,
	})

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
	entry := NewVirtualDirEntry("mydir")
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
