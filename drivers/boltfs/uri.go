// Copyright 2026 Jeremy Edwards
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

// Package boltfs registers the "bolt:" ufs driver, a read-write file system
// stored in a single BoltDB (go.etcd.io/bbolt) file. Import it for its side
// effect to make bolt: URIs available to [ufs.New]:
//
//	import _ "github.com/cloudfra/ufs/drivers/boltfs"
//
//	fsys, err := ufs.New(ctx, "bolt:/path/to/data.db")
//
// Directories are nested buckets and files are key/value entries in their
// parent directory's bucket. Writes to an open file are buffered in memory
// and committed to the database, in a single transaction, when the file is
// closed. The driver is unavailable on GOARCH=wasm, where bbolt does not
// build; there, bolt: URIs return an error.
package boltfs

import "strings"

const (
	boltFSPrefix = "bolt:"
)

func isBoltFSUri(name string) bool {
	return strings.HasPrefix(name, boltFSPrefix)
}
