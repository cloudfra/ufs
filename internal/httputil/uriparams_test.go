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

package httputil

import (
	"strings"
	"testing"
)

func TestSplitJoinQueryParts(t *testing.T) {
	t.Parallel()
	// A query is split and joined byte for byte, so anything that survives the
	// walk reaches the next reader unchanged. url.Query would have dropped the
	// malformed pair instead.
	queries := []string{
		"",
		"a=1",
		"a=1&b=2&a=3",
		"b=two%20one&a=1&sig=a%26b%3Dc+d",
		"ro",
		"ro&x",
		"data=null%3A%2F%2Fh%3Fa%3D1",
		"%zz=1&ro=true",
		"a=1&&b=2",
		"a=1&",
		"a=1=&b",
		"options=%5B%7BreadOnly%3A+true%7D%5D",
	}
	for _, query := range queries {
		t.Run(query, func(t *testing.T) {
			t.Parallel()
			pairs := SplitQueryParts(query)
			if got := JoinQuery(pairs); got != query {
				t.Errorf("JoinQuery(SplitQueryParts(%q)) = %q, want %q", query, got, query)
			}
			if query == "" {
				if len(pairs) != 0 {
					t.Errorf("SplitQueryParts(%q) = %d pairs, want none", query, len(pairs))
				}
				return
			}
			if want := strings.Count(query, "&") + 1; len(pairs) != want {
				t.Fatalf("SplitQueryParts(%q) = %d pairs, want %d", query, len(pairs), want)
			}
			for i, pair := range pairs {
				if pair.Raw != query && !strings.Contains(query, pair.Raw) {
					t.Errorf("pair %d = %q, want a piece of %q", i, pair.Raw, query)
				}
				rejoined := pair.RawKey
				if pair.HasValue {
					rejoined += "=" + pair.RawValue
				}
				if rejoined != pair.Raw {
					t.Errorf("pair %d = %q + %q, want the pair %q rejoinable", i, pair.RawKey, pair.RawValue, pair.Raw)
				}
			}
		})
	}
}

func TestQueryPairKeyValue(t *testing.T) {
	t.Parallel()
	tests := []struct {
		raw          string
		wantKey      string
		wantKeyErr   bool
		wantValue    string
		wantValueErr bool
		wantHasValue bool
	}{
		{raw: "ro=true", wantKey: "ro", wantValue: "true", wantHasValue: true},
		{raw: "ro", wantKey: "ro", wantValue: "", wantHasValue: false},
		{raw: "ro=", wantKey: "ro", wantValue: "", wantHasValue: true},
		{raw: "a=b=c", wantKey: "a", wantValue: "b=c", wantHasValue: true},
		{raw: "a=b%26c", wantKey: "a", wantValue: "b&c", wantHasValue: true},
		{raw: "a+b=1", wantKey: "a b", wantValue: "1", wantHasValue: true},
		{raw: "%zz=1", wantKeyErr: true, wantValue: "1", wantHasValue: true},
		{raw: "a=%zz", wantKey: "a", wantValueErr: true, wantHasValue: true},
	}
	for _, tc := range tests {
		t.Run(tc.raw, func(t *testing.T) {
			t.Parallel()
			pair := SplitQueryParts(tc.raw)[0]
			key, err := pair.Key()
			switch {
			case tc.wantKeyErr:
				if err == nil {
					t.Errorf("Key(%q) = %q, want an error", tc.raw, key)
				}
			case err != nil:
				t.Errorf("Key(%q) = %v, want nil", tc.raw, err)
			case key != tc.wantKey:
				t.Errorf("Key(%q) = %q, want %q", tc.raw, key, tc.wantKey)
			}
			value, err := pair.Value()
			switch {
			case tc.wantValueErr:
				if err == nil {
					t.Errorf("Value(%q) = %q, want an error", tc.raw, value)
				}
			case err != nil:
				t.Errorf("Value(%q) = %v, want nil", tc.raw, err)
			case value != tc.wantValue:
				t.Errorf("Value(%q) = %q, want %q", tc.raw, value, tc.wantValue)
			}
			if pair.HasValue != tc.wantHasValue {
				t.Errorf("HasValue(%q) = %v, want %v", tc.raw, pair.HasValue, tc.wantHasValue)
			}
		})
	}
}

func TestIndexOfQueryKey(t *testing.T) {
	t.Parallel()
	pairs := SplitQueryParts("%zz=1&b=2&ro=true&ro=false")
	tests := []struct {
		key  string
		want int
	}{
		{key: "%zz", want: -1},
		{key: "b", want: 1},
		{key: "ro", want: 2},
		{key: "missing", want: -1},
		// The name is matched exactly as written, so a parameter spelled with a
		// different case stays with whoever wrote it.
		{key: "RO", want: -1},
	}
	for _, tc := range tests {
		t.Run(tc.key, func(t *testing.T) {
			t.Parallel()
			if got := IndexOfQueryKey(pairs, tc.key); got != tc.want {
				t.Errorf("IndexOfQueryKey(%q) = %d, want %d", tc.key, got, tc.want)
			}
		})
	}
}
