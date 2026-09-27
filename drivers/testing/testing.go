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

package testing

import (
	"testing"

	"github.com/cloudfra/ufs"
)

var invalidPaths = []string{
	"/absolute/path",
	"../relative/path",
	"invalid/../path",
}

// FS runs the full read-write conformance suite against file systems
// returned by createFSFunc.
func FS(t *testing.T, createFSFunc func(t *testing.T) ufs.FS) {
	createWriteFSFunc := func(t *testing.T) ufs.WriteFS {
		return createFSFunc(t)
	}
	MkdirAll(t, createWriteFSFunc)
	ReadFile(t, createWriteFSFunc)
	DirFileConflicts(t, createWriteFSFunc)
	ReadFS(t, func(t *testing.T) ufs.ReadFS {
		return createFSFunc(t)
	})
}
