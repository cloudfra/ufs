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

package testing

import (
	"fmt"
	"io/fs"
	"reflect"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// gateProbeFS implements [types.SimpleFS] and no optional capability, so every
// capability gate must refuse its suite.
type gateProbeFS struct{}

func (gateProbeFS) Open(name string) (fs.File, error) {
	return nil, fmt.Errorf("%q: %w", name, fs.ErrNotExist)
}

func (gateProbeFS) Close() error {
	return nil
}

// TestCapabilityGateSkipsOnlyItsOwnSuite checks that a capability gate refuses
// its own suite without skipping the driver test that calls every suite.
func TestCapabilityGateSkipsOnlyItsOwnSuite(t *testing.T) {
	t.Parallel()

	args := Args{fsType: reflect.TypeFor[gateProbeFS]()}

	for _, tc := range []struct {
		name  string
		suite func(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS)
	}{
		{name: "copyFileFS", suite: copyFileFS},
		{name: "uriGetFS", suite: uriGetFS},
		{name: "removeFS", suite: removeFS},
		{name: "removeAllFS", suite: removeAllFS},
		{name: "releaseFS", suite: releaseFS},
	} {
		t.Run(tc.name, func(t *testing.T) {
			returned := false

			t.Run("caller", func(t *testing.T) {
				tc.suite(t, args, func(_ *testing.T) types.SimpleFS {
					return gateProbeFS{}
				})
				returned = true
			})

			if !returned {
				t.Errorf("%s skipped its caller instead of only its own suite, which skips every suite a driver test would run after it", tc.name)
			}
		})
	}
}
