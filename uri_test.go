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

package ufs

import (
	"net/url"
	"testing"

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
			got := uriOrDefault(ug, tc.value)
			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", got, tc.want, diff)
			}
		})
	}
}
