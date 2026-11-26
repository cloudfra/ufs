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

package core

// GetAbsPath is [ufs.AbsPathGetter], which [localFSInterface] asks beside [fs.GlobFS].

import (
	"path/filepath"
)

func (fsys *localFS) GetAbsPath(name string) (string, error) {
	if err := validLocalPath("absPath", name); err != nil {
		return "", err
	}
	return filepath.Abs(filepath.Join(fsys.osFS.Name(), name))
}
