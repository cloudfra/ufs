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
	"fmt"
	"io/fs"
)

func String(fsys fs.FS) string {
	if cFS, ok := fsys.(fmt.Stringer); ok {
		return cFS.String()
	}
	// TODO: Populate this with the best effort implementation of this method based on any combinations of methods that are available to reconstruct this method. Also include tests.
	return fmt.Sprintf("%+v", fsys)
}
