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

package ufs

// URI is the [types.URIGet] half of [types.ReadFS]: it renders the wrapped
// file system's URI with the mounts written into it.

import (
	"fmt"
	"io/fs"
	"net/url"
	"strings"
)

func (fsys *nestFS) URI() (*url.URL, error) {
	// types.Equals asks for the URI of any file system it is given, including a
	// closed one, and treats only an error as "no URI" because a nil URL would be
	// dereferenced.
	if fsys.fsys == nil {
		return nil, fmt.Errorf("cannot get the URI of a closed file system, %w", fs.ErrClosed)
	}
	base, err := fsys.fsys.URI()
	if base == nil || err != nil {
		return nil, nil
	}
	u := *base
	vals := u.Query()
	fsys.mounts.mu.RLock()
	defer fsys.mounts.mu.RUnlock()
	for p, mfs := range fsys.mounts.m {
		if strings.HasSuffix(p, archiveDirExt) {
			continue
		}
		if mu, err := mfs.URI(); mu != nil && err == nil {
			vals.Set(p, mu.String())
		}
	}
	u.RawQuery = vals.Encode()
	return &u, nil
}
