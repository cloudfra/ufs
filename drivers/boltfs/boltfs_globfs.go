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

// Glob and the bucket walk it drives are the [fs.GlobFS] half, which [types.FS] does not ask for.

import (
	"bytes"
	"path"
	"slices"

	"github.com/cloudfra/ufs/internal/pathutil"
	bolt "go.etcd.io/bbolt"
)

func (fsys *boltFS) Glob(pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	var matches []string
	err := fsys.view(func(tx *bolt.Tx) error {
		bkt, err := rootBucket(tx)
		if err != nil {
			return err
		}
		return boltGlobWalk(bkt, pathutil.CwdPath, pattern, &matches)
	})
	if err != nil {
		return nil, err
	}
	slices.Sort(matches)
	return matches, nil
}

// boltGlobWalk recursively matches pattern against every path under bkt
// (whose own path is prefix), appending matches to *matches.
func boltGlobWalk(bkt *bolt.Bucket, prefix, pattern string, matches *[]string) error {
	return bkt.ForEach(func(k, v []byte) error {
		if bytes.Equal(k, selfKey) {
			return nil
		}
		full := joinPath(prefix, string(k))
		matched, err := path.Match(pattern, full)
		if err != nil {
			return err
		}
		if matched {
			*matches = append(*matches, full)
		}
		if v == nil {
			return boltGlobWalk(bkt.Bucket(k), full, pattern, matches)
		}
		return nil
	})
}
