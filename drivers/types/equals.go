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
	"io/fs"
	"reflect"
)

// Equals reports whether two file systems are the same. A file system whose
// dynamic type cannot be compared, such as a wrapper holding a func, map or
// slice field, is reported as different rather than panicking.
func Equals(a, b fs.FS) bool {
	aType, bType := reflect.TypeOf(a), reflect.TypeOf(b)
	if aType == nil || bType == nil {
		return a == b
	}
	if aType != bType || !aType.Comparable() {
		return false
	}
	if aURIGet, aOk := a.(URIGet); aOk {
		if bURIGet, bOk := b.(URIGet); bOk {
			aURI, aErr := aURIGet.URI()
			bURI, bErr := bURIGet.URI()
			// A nil URL with no error is not an answer [URIGet] accepts, but an
			// older or third-party implementation can still give one. Two file
			// systems that both answer nil are not thereby the same file system,
			// so they are not compared by URI. Comparing them by identity instead
			// can only report the same value, never two unrelated file systems as
			// equal.
			if aErr == nil && bErr == nil && aURI != nil && bURI != nil {
				return aURI.String() == bURI.String()
			}
		}
	}
	return a == b
}
