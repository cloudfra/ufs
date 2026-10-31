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

	"github.com/cloudfra/ufs/internal/osutil"
)

func TestDirEntry(t *testing.T) {
	t.Parallel()
	entry := NewDirEntry("mydir")
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
	if entry.Size() != osutil.EmptyDirSize {
		t.Errorf("Size() got: %d, want: %d", entry.Size(), osutil.EmptyDirSize)
	}
	if entry.ModTime() != (time.Time{}) {
		t.Errorf("ModTime() got: %v, want: %v", entry.ModTime(), time.Time{})
	}
	if entry.Sys() != nil {
		t.Errorf("Sys() got: %v, want: nil", entry.Sys())
	}
}
