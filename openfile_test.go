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

package ufs_test

import (
	"errors"
	"io/fs"
	"os"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// plainFS hides every optional interface of the file system it embeds, as a
// driver outside this module that predates OpenFileFS does.
type plainFS struct {
	ufs.WriteFS
}

// openFileFS records the OpenFile calls it receives.
type openFileFS struct {
	ufs.WriteFS
	flags []int
}

func (fsys *openFileFS) OpenFile(name string, flag int, _ fs.FileMode) (fs.File, error) {
	fsys.flags = append(fsys.flags, flag)
	return fsys.Open(name)
}

// TestOpenFileWithoutOpenFileFS runs the OpenFile conformance tests against a
// file system that only has Open and Create.
func TestOpenFileWithoutOpenFileFS(t *testing.T) {
	createFSFunc := func(_ *testing.T) ufs.WriteFS {
		return plainFS{core.MakeMemFS("memory://test")}
	}
	if _, ok := createFSFunc(t).(ufs.OpenFileFS); ok {
		t.Fatal("plainFS implements ufs.OpenFileFS, want it hidden")
	}
	ufsdriversTesting.OpenFile(t, createFSFunc)
	ufsdriversTesting.OpenFile(t, func(t *testing.T) ufs.WriteFS {
		return ufs.WrapNestFS(t.Context(), createFSFunc(t))
	})
}

func TestOpenFileUsesOpenFileFS(t *testing.T) {
	t.Parallel()
	fsys := &openFileFS{WriteFS: core.MakeMemFS("memory://test")}
	defer ufsTesting.ValidateClose(t, fsys)()

	// A flag the fallback rejects reaches a file system that has its own
	// OpenFile.
	f, err := ufs.OpenFile(fsys, ".", os.O_RDWR|os.O_APPEND, fs.ModePerm)
	if err != nil {
		t.Fatalf("OpenFile() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, f)()
	if len(fsys.flags) != 1 || fsys.flags[0] != os.O_RDWR|os.O_APPEND {
		t.Errorf("OpenFile received flags %v, want [%#x]", fsys.flags, os.O_RDWR|os.O_APPEND)
	}
}

// TestOpenFileFallbackReadOnlyFS verifies that a file system that is neither
// an OpenFileFS nor a WriteFS can be read and not written.
func TestOpenFileFallbackReadOnlyFS(t *testing.T) {
	t.Parallel()
	inner := core.MakeMemFS("memory://test")
	ufsdriversTesting.WriteFile(t, inner, "a.txt", "content")
	fsys := struct{ ufs.ReadFS }{inner}
	defer ufsTesting.ValidateClose(t, fsys)()

	f, err := ufs.OpenFile(fsys, "a.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(O_RDONLY) = %v, want nil", err)
	}
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if _, err := ufs.OpenFile(fsys, "a.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fs.ModePerm); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("OpenFile(O_WRONLY|O_CREATE|O_TRUNC) = %v, want %v", err, fs.ErrPermission)
	}
}
