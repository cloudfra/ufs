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

package polyfills

import (
	"io/fs"

	"github.com/cloudfra/ufs/drivers/types"
)

// ExternalPath returns the canonical external path of the file at path, keeping
// the contract of [types.ExternalPathGetFS].
//
// fsys's own [types.ExternalPathGetFS] implementation is used when it has one.
// The fallback is not implemented yet and returns the empty string, which is
// what [types.ExternalPathGetFS] asks of an implementation that cannot answer,
// so no name is validated and nothing is reported when the answer is unknown.
func ExternalPath(fsys fs.FS, path string) string {
	if cFS, ok := fsys.(types.ExternalPathGetFS); ok {
		return cFS.ExternalPath(path)
	}
	// TODO: Populate this with the best effort implementation of this method based on any combinations of methods that are available to reconstruct this method. Also include tests.
	// An interesting thing is to get the data from os.Root.
	return ""
}
