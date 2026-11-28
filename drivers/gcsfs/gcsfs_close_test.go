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

package gcsfs

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
)

// TestGCSOperationsRefusedAfterClose checks the decision every entry point makes
// once the client is gone. Each of these would put something on the wire, and
// each of them refuses with an error matching [fs.ErrClosed] rather than
// answering from a client that has been given up — including the names, like the
// root, that a method might have answered without a request.
func TestGCSOperationsRefusedAfterClose(t *testing.T) {
	t.Parallel()

	client := CreateFakeGcsBackend(t)
	fsys, err := makeGCSFSWithClient(t.Context(), client, "gs://first")
	if err != nil {
		t.Fatalf("makeGCSFSWithClient(ctx, client, %q) = %v, want nil", "gs://first", err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() the second time = %v, want nil: closing twice is not an error", err)
	}

	operations := []struct {
		name string
		call func() error
	}{
		{"Open", func() error { _, err := fsys.Open("a"); return err }},
		{"Open root", func() error { _, err := fsys.Open(pathutil.CwdPath); return err }},
		{"Stat", func() error { _, err := fsys.Stat("a"); return err }},
		{"Stat root", func() error { _, err := fsys.Stat(pathutil.CwdPath); return err }},
		{"Lstat", func() error { _, err := fsys.Lstat("a"); return err }},
		{"Create", func() error { _, err := fsys.Create("a"); return err }},
		{"ReadFile", func() error { _, err := fsys.ReadFile("a"); return err }},
		{"ReadDir", func() error { _, err := fsys.ReadDir("dir"); return err }},
		{"ReadDir root", func() error { _, err := fsys.ReadDir(pathutil.CwdPath); return err }},
		{"Remove", func() error { return fsys.Remove("a") }},
		{"RemoveAll", func() error { return fsys.RemoveAll("dir") }},
		{"RemoveAll root", func() error { return fsys.RemoveAll(pathutil.CwdPath) }},
	}
	for _, operation := range operations {
		t.Run(operation.name, func(t *testing.T) {
			if err := operation.call(); !errors.Is(err, fs.ErrClosed) {
				t.Errorf("%s on a closed file system = %v, want an error matching fs.ErrClosed", operation.name, err)
			}
		})
	}
}
