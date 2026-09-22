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

package ufsurl

import (
	"net"
	"net/url"
	"testing"
)

type uriGetter struct {
	name string
}

func (u *uriGetter) URI() (*url.URL, error) {
	return url.Parse(u.name)
}

func TestURIOrDefault(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input string
		value string
		want  string
	}{
		{input: "", value: "", want: ""},
		{input: "", value: "memory://default", want: ""},
		{input: "file:///tmp/default", value: "", want: "file:///tmp/default"},
		{input: "://example.com", value: "memory://fallback", want: "memory://fallback"},
		{input: "https://example.com", value: "memory://fallback", want: "https://example.com"},
		{input: "https://example.com/path?q=1", value: "memory://fallback", want: "https://example.com/path?q=1"},
		{input: "file:///tmp/example.txt", value: "memory://fallback", want: "file:///tmp/example.txt"},
		{input: `\:broken:\`, value: "memory://fallback", want: "memory://fallback"},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			ug := &uriGetter{name: tc.input}
			got := URIOrDefault(ug, tc.value)
			if got != tc.want {
				t.Errorf("URIOrDefault(%q, %q) = %q, want %q", tc.input, tc.value, got, tc.want)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{name: "simple", path: "/archive/file.zip", want: "file.zip", wantErr: false},
		{name: "nested", path: "/a/b/c/data.tar.gz", want: "data.tar.gz", wantErr: false},
		{name: "single component", path: "/file.zip", want: "file.zip", wantErr: false},
		{name: "empty last component", path: "/path/to/", want: "", wantErr: true},
		{name: "dot", path: "/path/.", want: "", wantErr: true},
		{name: "dotdot", path: "/path/..", want: "", wantErr: true},
		{name: "root only", path: "/", want: "", wantErr: true},
		{name: "empty path", path: "", want: "", wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := &url.URL{Scheme: "https", Host: "example.com", Path: tc.path}
			got, err := SanitizeFilename(u)
			if (err != nil) != tc.wantErr {
				t.Errorf("SanitizeFilename(%q) error = %v, wantErr = %v", tc.path, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("SanitizeFilename(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsBlockedIP(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.1.100", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"fd00::1", true},

		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		{"192.169.0.1", false},
		{"11.0.0.1", false},
		{"2001:db8::1", false},
	}
	for _, tc := range testCases {
		t.Run(tc.ip, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) = nil", tc.ip)
			}
			got := IsBlockedIP(ip)
			if got != tc.want {
				t.Errorf("IsBlockedIP(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}
