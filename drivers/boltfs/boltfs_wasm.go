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

//go:build wasm

package boltfs

import (
	"context"
	"fmt"

	"github.com/cloudfra/ufs"
)

func init() {
	ufs.Register(ufs.Driver{
		Name:       "bolt",
		CreateFunc: newBoltFS,
		MatchFunc:  isBoltFSUri,
		Priority:   1,
		Standard:   true,
		ReadWrite:  true,
	})
}

// newBoltFS reports that boltFS is unavailable on GOARCH=wasm:
// go.etcd.io/bbolt has no MaxAllocSize constant for that architecture, and
// its mmap-based storage model has no wasm implementation regardless. The
// driver is still registered so bolt: URIs fail with this clear error
// instead of falling through to another driver.
func newBoltFS(_ context.Context, name string) (ufs.FS, error) {
	return nil, fmt.Errorf("boltFS (%q) is not supported on this platform: go.etcd.io/bbolt does not support GOARCH=wasm", name)
}
