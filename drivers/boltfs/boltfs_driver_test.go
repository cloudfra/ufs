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

//go:build !wasm

package boltfs

import (
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
)

func TestBoltFSDriver(t *testing.T) {
	ufsdriversTesting.WriteFS(t, func(t *testing.T) ufs.WriteFS {
		name := testBoltFSURI(t)
		fsys, err := makeBoltFS(name)
		if err != nil {
			t.Fatalf("cannot create boltFS %q, %s", name, err)
		}
		return fsys
	})
}
