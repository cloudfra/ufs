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

package ufs

import (
	"errors"
	"io/fs"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

func TestNewFileInfo(t *testing.T) {
	t.Parallel()
	tNow := ufsTesting.MustTime(t, "2026-01-01T00:00:00Z")
	tests := []struct {
		name    string
		mode    fs.FileMode
		wantDir bool
	}{
		{"file.txt", 0o644, false},
		{"subdir", fs.ModeDir | 0o755, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			info := NewFileInfo(tc.name, 42, tc.mode, tNow)
			if info.Name() != tc.name {
				t.Errorf("Name() = %q, want %q", info.Name(), tc.name)
			}
			if info.Size() != 42 {
				t.Errorf("Size() = %d, want 42", info.Size())
			}
			if info.Mode() != tc.mode {
				t.Errorf("Mode() = %v, want %v", info.Mode(), tc.mode)
			}
			if !info.ModTime().Equal(tNow) {
				t.Errorf("ModTime() = %v, want %v", info.ModTime(), tNow)
			}
			if info.IsDir() != tc.wantDir {
				t.Errorf("IsDir() = %v, want %v", info.IsDir(), tc.wantDir)
			}
			if info.Sys() != nil {
				t.Errorf("Sys() = %v, want nil", info.Sys())
			}
		})
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
	fsys := mustBaseFS(t, "memory:///")
	ufsTesting.Must(t, fsys.MkdirAll("a/b/c", fs.ModePerm))
	ufsTesting.Must(t, fsys.MkdirAll("d/e/f", fs.ModePerm))
	ufsTesting.Must(t, fsys.MkdirAll("g/h/i", fs.ModePerm))

	dirFile := makeReadDirFile(fsys, pathutil.CwdPath)

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
	gotNames := ufsTesting.DirEntryListToNames(entries)
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("got %s, want %s diff(-want,+got):\n %v", gotNames, wantNames, diff)
	}

	stat, err := dirFile.Stat()
	if err != nil {
		t.Errorf("Stat() returned error, %s", err)
	}

	if stat.Name() != pathutil.CwdPath {
		t.Errorf("Stat().Name() got %s, want '.'", stat.Name())
	}

	if stat.Mode()|fs.ModeDir == 0 {
		t.Errorf("Stat().Mode() is not a directory, got: %s", stat.Mode())
	}

	if err := dirFile.Close(); err != nil {
		t.Errorf("Close() returned error, %s", err)
	}
}
