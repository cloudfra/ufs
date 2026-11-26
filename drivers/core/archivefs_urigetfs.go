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

// URI is the [types.URIGet] half of [types.ReadFS], reported read-only because a mounted archive cannot be written.

import (
	"net/url"
)

func (fsys *archiveFS) URI() (*url.URL, error) {
	p := fsys.name
	if len(p) > 0 && p[0] != '/' {
		p = "/" + p
	}
	return &url.URL{Scheme: "archive", Path: p, RawQuery: "ufs.ro=true"}, nil
}
