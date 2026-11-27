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
	"io/fs"
	"net/url"
	"strings"
)

func (fsys *nestFS) URI() (*url.URL, error) {
	// The base URI goes first, outside the mount section below: taking
	// fsys.mu inside that section would order the two locks the opposite way
	// from [nestFS.Close]. Asking the mounts takes this nest's mount lock and
	// then each mount's lifetime lock, which is the direction Close also
	// points.
	base, err := fsys.baseURI()
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

// baseURI renders the wrapped file system's URI under the lifetime read
// lock, the barrier that keeps Close from tearing the base down mid-call. A
// closed nest has no URI, reported the way a base that offers none is.
func (fsys *nestFS) baseURI() (*url.URL, error) {
	if fsys.closed.Load() {
		return nil, fs.ErrClosed
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return nil, fs.ErrClosed
	}
	uri, err := fsys.fsys.URI()
	fsys.mu.RUnlock()
	return uri, err
}
