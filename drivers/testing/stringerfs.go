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
	"encoding/json"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// stringerFS runs the conformance suite for [types.StringerFS], whose description
// is a JSON object naming the file system and the storage it rests on.
//
// It does not branch on [Args.ReadOnly] or [Args.Bucket]: what a file system says
// it is does not change because it cannot be written to.
func stringerFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("StringerFS", func(t *testing.T) {
		requireType[types.StringerFS](t, args)
		t.Parallel()
		createStringerFSFunc := requireFSTypeFunc[types.StringerFS](t, createFSFunc)
		stringerDescription(t, createStringerFSFunc)
		stringerStable(t, createStringerFSFunc)
	})
}

// stringerDescription requires the description to be the JSON object
// [types.StringerFS] describes: one key naming the file system, whose value is the
// storage it rests on or, for a file system built from others, the parts it holds
// keyed by where each part is found.
func stringerDescription(t *testing.T, createFSFunc func(t *testing.T) types.StringerFS) {
	t.Run("StringerDescription", func(t *testing.T) {
		t.Parallel()
		fsys := newStringerFS(t, createFSFunc)

		got := fsys.String()
		if got == "" {
			t.Fatal("String() returned the empty string: [types.StringerFS] says the description is never empty")
		}
		if trimmed := strings.TrimSpace(got); got != trimmed {
			t.Errorf("String() = %q, want no space around the description: [types.StringerFS] says the description carries no space around it, and padding reads as part of the name", got)
		}

		var root map[string]any
		if err := json.Unmarshal([]byte(got), &root); err != nil {
			t.Fatalf("String() = %q, want a JSON object: [types.StringerFS] says the description is an object whose one key names the file system: %v", got, err)
		}

		if len(root) != 1 {
			t.Fatalf("String() = %q has %d keys, want one: [types.StringerFS] says the description is an object whose one key names the file system", got, len(root))
		}

		var name string
		var storage any
		for key, value := range root {
			name, storage = key, value
		}

		if name == "" {
			t.Errorf("String() = %q has an empty key, want the name of the file system: [types.StringerFS] says the one key names the file system", got)
		}

		switch storage := storage.(type) {
		case string:
			if storage == "" {
				t.Errorf("String() = %q names no storage: [types.StringerFS] says the value is the storage the file system rests on, which names something", got)
			}
			stringerStorageURI(t, fsys, got, storage)
		case map[string]any:
			if len(storage) == 0 {
				t.Errorf("String() = %q holds no parts: [types.StringerFS] says a file system built from others reports the parts it holds, and an empty object holds none", got)
			}
			for where, part := range storage {
				if where == "" {
					t.Errorf("String() = %q has an empty part key: [types.StringerFS] says the parts are keyed by where each part is found", got)
				}
				switch part := part.(type) {
				case string:
					if part == "" {
						t.Errorf("String() = %q names no storage for part %q: [types.StringerFS] says each part is the storage that part rests on", got, where)
					}
				case map[string]any:
					if len(part) == 0 {
						t.Errorf("String() = %q describes part %q as an empty object: [types.StringerFS] says the description is never empty, and an empty part names nothing", got, where)
					}
				default:
					t.Errorf("String() = %q describes part %q as %v, want a storage or a description of another file system: [types.StringerFS] says the value is the storage the file system rests on or the parts it holds", got, where, part)
				}
			}
		default:
			t.Errorf("String() = %q describes %v, want a storage string or an object of held parts: [types.StringerFS] says the value is the storage the file system rests on or an object of the parts it holds", got, storage)
		}
	})
}

// stringerStable requires the description to be a property of the file system
// rather than of the moment it was asked.
func stringerStable(t *testing.T, createFSFunc func(t *testing.T) types.StringerFS) {
	t.Run("StringerStable", func(t *testing.T) {
		t.Parallel()
		fsys := newStringerFS(t, createFSFunc)

		first := fsys.String()
		second := fsys.String()
		if first != second {
			t.Errorf("String() a second time = %q, want %q: a log line that changes each time it is written describes something other than the file system", second, first)
		}
	})
}

// stringerStorageURI requires a leaf description to name the same storage
// [types.URIGetFS] identifies, so a log line and a handle cannot drift apart.
func stringerStorageURI(t *testing.T, fsys types.StringerFS, got, storage string) {
	t.Helper()

	uriFS, ok := fsys.(types.URIGetFS)
	if !ok {
		return
	}

	// A URI that cannot be read is reported by the uriGetFS suite.
	uri, err := uriFS.URI()
	if err != nil || uri == nil {
		return
	}

	if want := uri.String(); storage != want {
		t.Errorf("String() = %q names storage %q, want %q: [types.StringerFS] says the value is the storage the file system rests on, which is the storage [types.URIGetFS] identifies", got, storage, want)
	}
}

// newStringerFS builds a file system that describes itself, for one sub-suite.
func newStringerFS(t *testing.T, createFS func(*testing.T) types.StringerFS) types.StringerFS {
	t.Helper()
	fsys := createFS(t)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	return fsys
}
