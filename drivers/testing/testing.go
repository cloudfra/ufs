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

package testing

import (
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
)

var invalidPaths = []string{
	"/absolute/path",
	"../relative/path",
	"invalid/../path",
}

// FS runs the full read-write conformance suite against file systems
// returned by createFSFunc.
func FS(t *testing.T, createFSFunc func(t *testing.T) ufs.FS) {
	createWriteFSFunc := func(t *testing.T) types.WriteFS {
		return createFSFunc(t)
	}
	MkdirAll(t, createWriteFSFunc)
	ReadFile(t, createWriteFSFunc)
	DirFileConflicts(t, createWriteFSFunc)
	Rename(t, createWriteFSFunc)
	BatchCreate(t, createWriteFSFunc)
	ReadFS(t, func(t *testing.T) types.ReadFS {
		return createFSFunc(t)
	})
}

// WriteFile creates name in fsys with content, stopping the test if Create,
// the write, or Close fails.
func WriteFile(tb testing.TB, fsys types.WriteFS, name, content string) {
	tb.Helper()
	f, err := fsys.Create(name)
	if err != nil {
		tb.Fatalf("Create(%q) = %v", name, err)
	}
	if _, err := f.WriteString(content); err != nil {
		tb.Fatalf("WriteString(%q) = %v", name, err)
	}
	if err := f.Close(); err != nil {
		tb.Fatalf("Close(%q) = %v", name, err)
	}
}
