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

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/google/go-cmp/cmp"
)

func TestReadDirFile(t *testing.T) {
	fsys := makeMemFS("memory:///")
	must(t, fsys.MkdirAll("a/b/c", fs.ModePerm))
	must(t, fsys.MkdirAll("d/e/f", fs.ModePerm))
	must(t, fsys.MkdirAll("g/h/i", fs.ModePerm))

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
	gotNames := dirEntryListToNames(entries)
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
