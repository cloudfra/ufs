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

// Package ufsurl provides URL and network helpers that are not tied to a
// specific file-system implementation.
package ufsurl

import (
	"fmt"
	"net"
	"net/url"
	"path/filepath"
	"strings"
)

// URIer is satisfied by types that return their URL representation.
type URIer interface {
	URI() (*url.URL, error)
}

// URIOrDefault calls ug.URI() and returns the URL's string form on success.
// If ug.URI() returns an error or a nil URL, value is returned instead.
func URIOrDefault(ug URIer, value string) string {
	u, err := ug.URI()
	if err != nil || u == nil {
		return value
	}
	return u.String()
}

// SanitizeFilename extracts and validates a filename from the last component
// of rawURL.Path. It returns an error if the resulting name is empty, ".",
// "..", or contains path separators.
func SanitizeFilename(rawURL *url.URL) (string, error) {
	p := rawURL.Path
	parts := strings.Split(p, "/")
	filename := parts[len(parts)-1]
	filename = strings.TrimSpace(filename)
	if filename == "" || filename == "." || filename == ".." {
		return "", fmt.Errorf("invalid filename %q derived from URL %q", filename, rawURL.Redacted())
	}
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) {
		return "", fmt.Errorf("invalid filename %q derived from URL %q", filename, rawURL.Redacted())
	}
	return filename, nil
}

// IsBlockedIP reports whether ip is a loopback, private, link-local,
// or unspecified address. Use as an SSRF guard when validating download
// destinations.
func IsBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}
