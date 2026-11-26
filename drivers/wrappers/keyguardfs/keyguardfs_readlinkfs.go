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

package keyguardfs

// The [fs.ReadLinkFS] half of a guard.

import (
	"github.com/cloudfra/ufs/internal/pathutil"
)

func (fsys *keyGuardFS) ReadLink(name string) (string, error) {
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	if fsys.protected(name) {
		return "", fsys.denied("readlink", name)
	}

	return fsys.inner.ReadLink(name)
}
