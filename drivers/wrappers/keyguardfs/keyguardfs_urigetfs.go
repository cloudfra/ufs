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

// The [types.URIGetFS] half of a guard.

import (
	"net/url"

	"github.com/cloudfra/ufs/drivers/uri"
)

// URI returns the guarded file system's URI with ufs.ro=true added, because a
// guard never writes, see [types.URIGet].
func (fsys *keyGuardFS) URI() (*url.URL, error) {
	u, err := fsys.inner.URI()
	if err != nil {
		return nil, err
	}

	// Added with uri.AddParameter rather than the standard library's helper: the
	// pair walk leaves the guarded file system's own parameters in the order and
	// escaping they were written in, and returns a copy, so the guarded file
	// system keeps the URL it handed over. A nil URI stays nil rather than
	// becoming a URL invented out of nothing.
	return uri.AddParameter(u, "ufs.ro", "true")
}
