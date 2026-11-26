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

package gcsfs

// URI is the [types.URIGet] half of [types.ReadFS]: it writes the bucket and prefix back into a gs:// URI, keeping the subscription parameter Watch reads.

import (
	"net/url"
)

func (fsys *gcsFS) URI() (*url.URL, error) {
	vals := url.Values{}
	if fsys.subscription != "" {
		vals.Set(subscriptionParam, fsys.subscription)
	}
	return &url.URL{
		Scheme:   "gs",
		Host:     fsys.bucket,
		Path:     "/" + fsys.baseDir,
		RawQuery: vals.Encode(),
	}, nil
}
