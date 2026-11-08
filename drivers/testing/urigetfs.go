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

// Package testing provides shared conformance tests that drivers run against their
// types.ReadFS, types.WriteFS, and ufs.FS implementations.
package testing

import (
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func uriGetFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("URIGetFS", func(t *testing.T) {
		requireType[types.URIGetFS](t, args)
		t.Parallel()
		createFunc := mustFSTypeFunc[types.URIGetFS](t, createFSFunc)
		uriGetReadOnly(t, args, createFunc)
	})
}

func uriGetReadOnly(t *testing.T, args Args, createFSFunc func(t *testing.T) types.URIGetFS) {
	t.Run("URI", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		defer ufsTesting.ValidateClose(t, fsys)()

		u, err := fsys.URI()
		if err != nil {
			t.Fatalf("URI() returned error: %v", err)
		}
		if u == nil {
			t.Fatal("URI() = nil, want a URL")
		}
		if args.ReadOnly {
			if got := u.Query().Get("ro"); got != "true" {
				t.Errorf("%q.Query().Get(\"ro\") = %q, want %q", u, got, "true")
			}
		} else {
			if got := u.Query().Get("ro"); got != "false" && got != "" {
				t.Errorf("%q.Query().Get(\"ro\") = %q, want %q", u, got, "false or empty")
			}
		}
	})
}
