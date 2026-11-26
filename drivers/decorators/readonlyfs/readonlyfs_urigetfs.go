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

package readonlyfs

// URI is the [types.URIGet] half of [types.ReadFS]: the wrapped file
// system's address with ufs.ro=true and the readOnly option added.

import (
	"net/url"

	"github.com/cloudfra/ufs/drivers/uri"
)

func (fsys *readOnlyFS) URI() (*url.URL, error) {
	u, err := fsys.ReadFS.URI()
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	// Tacked rather than set through url.Values, so the parameters the wrapped
	// file system reported keep the order and escaping they were written with.
	tacked, err := uri.AddParameter(u, "ufs.ro", "true")
	if err != nil {
		return nil, err
	}
	return uri.AppendOption(tacked, optionName, Options{Enabled: true})
}
