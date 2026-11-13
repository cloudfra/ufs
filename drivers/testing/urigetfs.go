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
	"net/url"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func uriGetFS(t *testing.T, args Args, createFSFunc func(t *testing.T) types.SimpleFS) {
	t.Run("uriGetFS", func(t *testing.T) {
		requireType[types.URIGetFS](t, args)
		t.Parallel()
		createFS := requireFSTypeFunc[types.URIGetFS](t, createFSFunc)
		uriGetReadOnly(t, args, createFS)
		uriGetStable(t, createFS)
	})
}

// uriGetStable asserts the identifier is a stable, absolute URL that survives
// being rendered and re-parsed, and that the file system keeps its own copy.
func uriGetStable(t *testing.T, createFSFunc func(t *testing.T) types.URIGetFS) {
	t.Run("uriGetStable", func(t *testing.T) {
		t.Parallel()
		fsys := createFSFunc(t)
		t.Cleanup(ufsTesting.ValidateClose(t, fsys))

		first, err := fsys.URI()
		if err != nil {
			t.Fatalf("URI() returned error: %v", err)
		}
		if first == nil {
			t.Fatal("URI() = nil, want a URL")
		}
		wanted := first.String()
		second, err := fsys.URI()
		if err != nil {
			t.Fatalf("URI() returned error: %v", err)
		}
		if second == nil {
			t.Fatal("URI() = nil on the second call, want the URL reported by the first call")
		}
		if got := second.String(); got != wanted {
			t.Errorf("URI() = %q the second time and %q the first, want the same URL on every call", got, wanted)
		}
		if !first.IsAbs() {
			t.Errorf("URI() = %q has no scheme, so ufs.New cannot reopen the file system from it", wanted)
		}
		reparsed, err := url.Parse(wanted)
		if err != nil {
			t.Fatalf("url.Parse(%q) = %v, want the reported URL to parse", wanted, err)
		}
		if got := reparsed.String(); got != wanted {
			t.Errorf("url.Parse(%q).String() = %q, want the reported URL to survive being written and read back", wanted, got)
		}

		// A caller may keep the URL and edit it as configuration, so the file
		// system must not report the only copy it holds.
		first.Scheme = "tampered"
		first.Path = "/tampered"
		first.RawQuery = "tampered=true"
		third, err := fsys.URI()
		if err != nil {
			t.Fatalf("URI() returned error: %v", err)
		}
		if third == nil {
			t.Fatal("URI() = nil after an earlier result was edited, want the URL reported before the edit")
		}
		if got := third.String(); got != wanted {
			t.Errorf("URI() = %q after editing a URL returned by URI(), want %q: the file system must hand out a copy", got, wanted)
		}
	})
}

func uriGetReadOnly(t *testing.T, args Args, createFSFunc func(t *testing.T) types.URIGetFS) {
	t.Run("uriGetReadOnly", func(t *testing.T) {
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
