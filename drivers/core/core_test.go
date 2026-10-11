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

package core_test

import (
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// checkWriteRead verifies that a file written to fsys reads back.
func checkWriteRead(t *testing.T, fsys ufs.WriteFS) {
	t.Helper()
	const name, want = "hello.txt", "hello"
	f, err := fsys.Create(name)
	if err != nil {
		t.Fatalf("Create(%q) = %v, want nil", name, err)
	}
	if _, err := f.WriteString(want); err != nil {
		t.Errorf("WriteString(%q) = %v, want nil", want, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	got, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v, want nil", name, err)
	}
	if string(got) != want {
		t.Errorf("ReadFile(%q) = %q, want %q", name, got, want)
	}
}

func TestMakeMemFS(t *testing.T) {
	t.Parallel()
	fsys := core.MakeMemFS("memory://test")
	defer ufsTesting.ValidateClose(t, fsys)()
	checkWriteRead(t, fsys)
}

func TestNewTempMountFS(t *testing.T) {
	t.Parallel()
	prepared := ""
	fsys, err := core.NewTempMountFS(t.Context(), "test://", func(tempDir string) error {
		prepared = tempDir
		return nil
	})
	if err != nil {
		t.Fatalf("NewTempMountFS() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
	if prepared == "" {
		t.Error("NewTempMountFS() did not call prepare with the temporary directory")
	}
	checkWriteRead(t, fsys)
}
