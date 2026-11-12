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

package types

import (
	"encoding/json"
	"fmt"
	"strings"
)

// StringerFS is a file system that can describe itself. It pairs [SimpleFS]
// with [fmt.Stringer] so that a wrapper can print the file system it holds
// instead of a bare Go value.
//
// String returns a JSON description of the file system: an object whose one key
// names the file system, and whose value is the storage it rests on, or for a
// file system built from others an object of the parts it holds keyed by where
// each part is found. The description is never empty, carries no space around
// it, and says the same thing each time it is asked. It is meant for debugging
// and logging only: it is not round-trippable, so use [URIGetFS] when a file
// system must be reconstructed.
type StringerFS interface {
	SimpleFS
	fmt.Stringer
}

// Description renders a file system description as compact JSON, given an
// object that names the file system and describes what it rests on or holds.
// Keys are sorted, so a composed file system describes itself the same way each
// time it is asked.
func Description(root map[string]any) string {
	var b strings.Builder
	enc := json.NewEncoder(&b)
	// A description carries URIs whose query strings read better with their
	// ampersands than with escapes in place of them.
	enc.SetEscapeHTML(false)
	// Every value is a string, a nested map, or a value already decoded by
	// [Nested], so encoding cannot fail.
	enc.Encode(root) //nolint:errcheck,gosec // no value here is one JSON cannot encode
	return strings.TrimSuffix(b.String(), "\n")
}

// Nested returns description for use as the value inside another description,
// so that a wrapper shows the file system it holds as an object rather than as
// a quoted string. A description that is not JSON is kept as text.
func Nested(description string) any {
	var value any
	if err := json.Unmarshal([]byte(description), &value); err != nil {
		return description
	}
	return value
}
