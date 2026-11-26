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

package embedfs

// URI is the [types.URIGet] half of [types.ReadFS]: an embed:// address
// naming the embedded content, marked read-only because [embed.FS] cannot
// be written.

import (
	"net/url"
)

func (fsys *embedFS) URI() (*url.URL, error) {
	return &url.URL{Scheme: "embed", Path: "/" + fsys.name, RawQuery: "ufs.ro=true"}, nil
}
