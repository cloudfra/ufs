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
	"io"
	"io/fs"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestNestFSOpenHandleCannotWrite verifies that a handle from Open cannot change
// the file, for a backend whose handles can write.
func TestNestFSOpenHandleCannotWrite(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	const want = "original"
	f, err := fsys.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(want); err != nil {
		t.Fatal(err)
	}
	ufsTesting.Must(t, f.Close())

	opened, err := fsys.Open("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, opened)()
	w, ok := opened.(io.Writer)
	if !ok {
		t.Fatalf("Open() = %T, want a handle with a Write method that fails", opened)
	}
	if _, err := w.Write([]byte("changed")); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Write() on a handle from Open = %v, want fs.ErrInvalid", err)
	}

	got, err := fsys.ReadFile("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != want {
		t.Errorf("ReadFile() = %q, want %q", got, want)
	}
}

// TestNestFSCreateHandleCanWrite verifies that keeping a handle from Open
// read-only did not reach Create, whose caller asked to write.
func TestNestFSCreateHandleCanWrite(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	f, err := fsys.Create("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString("written"); err != nil {
		t.Errorf("WriteString() on a handle from Create = %v, want nil", err)
	}
	ufsTesting.Must(t, f.Close())

	got, err := fsys.ReadFile("file.txt")
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "written" {
		t.Errorf("ReadFile() = %q, want %q", got, "written")
	}
}
