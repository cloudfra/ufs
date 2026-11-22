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

import (
	"fmt"
	"net/url"
	"strings"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

type uriGetter struct {
	name string
}

func (u *uriGetter) URI() (*url.URL, error) {
	return url.Parse(u.name)
}

func TestUriOrDefault(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input string
		value string
		want  string
	}{
		{
			input: "",
			value: "",
			want:  "",
		},
		{
			input: "",
			value: "memory://default",
			want:  "",
		},
		{
			input: "file:///tmp/default",
			value: "",
			want:  "file:///tmp/default",
		},
		{
			input: "://example.com",
			value: "memory://fallback",
			want:  "memory://fallback",
		},
		{
			input: "https://example.com",
			value: "memory://fallback",
			want:  "https://example.com",
		},
		{
			input: "https://example.com/path?q=1",
			value: "memory://fallback",
			want:  "https://example.com/path?q=1",
		},
		{
			input: "file:///tmp/example.txt",
			value: "memory://fallback",
			want:  "file:///tmp/example.txt",
		},
		{
			input: "\\:broken:\\",
			value: "memory://fallback",
			want:  "memory://fallback",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			ug := &uriGetter{
				name: tc.input,
			}
			got := URIOrDefault(ug, tc.value)
			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", got, tc.want, diff)
			}
		})
	}
}

// TestSplitURIOptions is the thin root wrapper over [uri.RemoveOptions], which
// holds the options parameter and walks the query pair by pair: the decorators
// come back as []MountOption and the name loses the parameter. The table of
// pair-walking cases lives with that function, in drivers/uri.
func TestSplitURIOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		wantName string
		wantOpts []MountOption
		wantErr  string
	}{
		{
			name:     "no options",
			input:    "memory://test?cache=null%3A",
			wantName: "memory://test?cache=null%3A",
		},
		{
			name:     "options",
			input:    "memory://test?ufs.options=%5B%7BreadOnly%3A+true%7D%5D",
			wantName: "memory://test",
			wantOpts: []MountOption{{Name: "readOnly", Config: true}},
		},
		{
			// The pairs that stay keep their order and their escaping byte for byte.
			name:     "other parameters keep their order",
			input:    "memory://test?b=two%20one&a=1&ufs.options=%5B%7BreadOnly%3A+true%7D%5D",
			wantName: "memory://test?b=two%20one&a=1",
			wantOpts: []MountOption{{Name: "readOnly", Config: true}},
		},
		{
			name:     "invalid options",
			input:    "memory://test?ufs.options=%5Bnot",
			wantName: "memory://test?ufs.options=%5Bnot",
			wantErr:  "invalid ufs.options query parameter",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, opts, err := splitURIOptions(tc.input)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("splitURIOptions(%q) = %v, want nil", tc.input, err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("splitURIOptions(%q) = %v, want substring %q", tc.input, err, tc.wantErr)
			}
			if name != tc.wantName {
				t.Errorf("splitURIOptions(%q) name = %q, want %q", tc.input, name, tc.wantName)
			}
			if diff := cmp.Diff(tc.wantOpts, opts); diff != "" {
				t.Errorf("splitURIOptions(%q) options mismatch (-want +got):\n%s", tc.input, diff)
			}
		})
	}
}

func TestNewAppliesURIOptions(t *testing.T) {
	name := fmt.Sprintf("uriTestDecorator%d", registerTestCounter.Add(1))
	RegisterDecorator(tagDecorator(name))
	options := url.QueryEscape("[{" + name + ": {label: fromURI}}]")

	t.Run("root", func(t *testing.T) {
		fsys, err := New(t.Context(), "memory://test?ufs.options="+options)
		if err != nil {
			t.Fatalf("New() = %v, want nil", err)
		}
		defer ufsTesting.ValidateClose(t, fsys)()
		if got, want := tags(fsys.(*nestFS).fsys), []string{name + "=fromURI"}; !cmp.Equal(got, want) {
			t.Errorf("New() applied %v to the root, want %v", got, want)
		}
	})

	t.Run("mount", func(t *testing.T) {
		fsys, err := New(t.Context(), "memory://test?data="+url.QueryEscape("memory://data?ufs.options="+options))
		if err != nil {
			t.Fatalf("New() = %v, want nil", err)
		}
		defer ufsTesting.ValidateClose(t, fsys)()
		nFS := fsys.(*nestFS)
		if got := tags(nFS.fsys); len(got) != 0 {
			t.Errorf("New() applied %v to the root, want none", got)
		}
		mount, ok := nFS.mounts.m["data"]
		if !ok {
			t.Fatalf("New() mounts = %v, want a data mount", nFS.mounts.m)
		}
		if got, want := tags(mount.fsys), []string{name + "=fromURI"}; !cmp.Equal(got, want) {
			t.Errorf("New() applied %v to the mount, want %v", got, want)
		}
	})

	t.Run("mount spec source", func(t *testing.T) {
		fsys, err := New(t.Context(), "- source: \"memory://test?ufs.options="+options+"\"")
		if err != nil {
			t.Fatalf("New() = %v, want nil", err)
		}
		defer ufsTesting.ValidateClose(t, fsys)()
		if got, want := tags(fsys.(*nestFS).fsys), []string{name + "=fromURI"}; !cmp.Equal(got, want) {
			t.Errorf("New() applied %v to the root, want %v", got, want)
		}
	})

	t.Run("invalid options", func(t *testing.T) {
		_, err := New(t.Context(), "memory://test?ufs.options=%5Bnot")
		if err == nil || !strings.Contains(err.Error(), "invalid ufs.options query parameter") {
			t.Errorf("New() = %v, want an invalid options error", err)
		}
	})
}
