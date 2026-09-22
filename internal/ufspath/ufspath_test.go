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

package ufspath

import (
	"io/fs"
	"testing"
)

func TestError(t *testing.T) {
	t.Parallel()
	inner := fs.ErrInvalid
	err := Error("open", "file.txt", inner)
	if err == nil {
		t.Fatal("Error() = nil, want non-nil")
	}
	perr, ok := err.(*fs.PathError)
	if !ok {
		t.Fatalf("Error() = %T, want *fs.PathError", err)
	}
	if perr.Op != "open" {
		t.Errorf("Op = %q, want %q", perr.Op, "open")
	}
	if perr.Path != "file.txt" {
		t.Errorf("Path = %q, want %q", perr.Path, "file.txt")
	}
	if perr.Err != inner {
		t.Errorf("Err = %v, want %v", perr.Err, inner)
	}
}

func TestCoerceUnix(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input string
		want  string
	}{
		{input: "", want: ""},
		{input: "abc", want: "abc"},
		{input: ".", want: "."},
		{input: "/abc/d", want: "/abc/d"},
		{input: "abc/d/", want: "abc/d/"},
		{input: "abc\\d", want: "abc/d"},
		{input: "\\abc\\", want: "/abc/"},
		{input: "C:\\data\\file.txt", want: "C:/data/file.txt"},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := CoerceUnix(tc.input)
			if got != tc.want {
				t.Errorf("CoerceUnix(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

// TestNormalizeFileURI tests the non-Windows case: strip "file://" or "file:"
// prefix and return the path as-is.
func TestNormalizeFileURI(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input string
		want  string
	}{
		{input: "file:///tmp/foo", want: "/tmp/foo"},
		{input: "file:/tmp/foo", want: "/tmp/foo"},
		{input: "/tmp/foo", want: "/tmp/foo"},
		{input: "file:", want: ""},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := NormalizeFileURI(tc.input)
			if got != tc.want {
				t.Errorf("NormalizeFileURI(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}
