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

// Package patternutil matches file system names against a list of glob
// patterns, so that a caller can declare which names it means as data.
package patternutil

import (
	"fmt"
	"path"
	"strings"
)

// Match reports whether name matches any of patterns, where a pattern matches
// the whole name or any single slash-delimited component of it, so ".ssh" covers
// every name below a .ssh directory and a malformed pattern matches nothing.
func Match(patterns []string, name string) bool {
	for _, pattern := range patterns {
		if matchOne(pattern, name) {
			return true
		}
	}
	return false
}

// Validate reports an error for the first pattern that [path.Match] rejects, so
// that a list holding a malformed pattern fails where it is built rather than
// silently matching nothing.
func Validate(patterns []string) error {
	for _, pattern := range patterns {
		if _, err := path.Match(pattern, ""); err != nil {
			return fmt.Errorf("%q is not a valid pattern, %w", pattern, err)
		}
	}
	return nil
}

// matchOne reports whether pattern matches all of name or any one of its
// slash-delimited components.
func matchOne(pattern, name string) bool {
	// path.Match matches no slash in a pattern holding no slash, so for such a
	// pattern the whole-path test can only succeed on a name that is one
	// component, which the loop below tests anyway.
	if strings.Contains(pattern, "/") && matched(pattern, name) {
		return true
	}
	for rest := name; ; {
		component, tail, found := strings.Cut(rest, "/")
		if matched(pattern, component) {
			return true
		}
		if !found {
			return false
		}
		rest = tail
	}
}

// matched applies one pattern to one name, comparing a pattern that holds no
// meta character as a plain name so that a policy of literal names costs a
// comparison rather than a parse.
func matched(pattern, name string) bool {
	if !hasMeta(pattern) {
		return pattern == name
	}
	ok, err := path.Match(pattern, name)

	return err == nil && ok
}

// hasMeta reports whether pattern holds a character that [path.Match] gives
// meaning to.
func hasMeta(pattern string) bool {
	return strings.ContainsAny(pattern, `*?[]\`)
}
