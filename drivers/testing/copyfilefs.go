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

	"github.com/cloudfra/ufs/drivers/types"
)

// CopyFileFS verifies that fs.CopyFileFS copies files and directories as expected when the source
// and destination are valid, and that it fails with the documented errors when the source or
// destination are invalid. It also verifies that the source and destination are unchanged when
// CopyFileFS fails.
func CopyFileFS(t *testing.T, _ Args, createFSFunc func(t *testing.T) types.CopyFileFS) {
	if createFSFunc == nil {
		t.Fatal("createFSFunc is nil")
	}
	// TODO Handle the scenarios.
	// At a minimum:
	// * Copy a file to an existing directory.
	// * Copy a file to a directory that does not exist.
	// * Copy with "." as destination or source.
	// * Copy a file to overwrite an existing file.
	// * Copy a directory to an existing directory.
	// * Copy a directory to a directory that does not exist.
	// * Copy a directory to overwrite an existing file.
	// * Copy a directory to overwrite an existing directory.
}
