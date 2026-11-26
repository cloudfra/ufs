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

//go:build !wasm

package boltfs

// URI is the [types.URIGet] half of [types.ReadFS]: it writes the open database path back into a bolt: URI, keeping the query that configures the database.

import (
	"net/url"

	"github.com/cloudfra/ufs/internal/pathutil"
)

// URI returns the bolt:///abs/path form of the database path, which
// makeBoltFS accepts, so the URI reopens the same database.
func (fsys *boltFS) URI() (*url.URL, error) {
	return &url.URL{Scheme: boltFSScheme, Path: pathutil.CoerceUnix(fsys.absPath), RawQuery: fsys.opts.query()}, nil
}
