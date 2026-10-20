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
	"net/url"
)

// URIGet describes file systems that can report the canonical identifier used
// to reconstruct them. The returned [*url.URL] is typically passed to ufs.New
// or stored as a durable configuration value. It must be safe to round-trip:
// calling ufs.New with u.String() should recreate an equivalent file system
// when the backend supports a stable URI representation.
//
// Implementations may return nil if the file system is synthetic or has no
// meaningful external identity (for example, readwrapfs.FromFS). Callers
// should treat a nil URL or a non-nil error as "no URI" and fall back to a
// default value when needed. Wrappers such as nestFS embed mount information
// in the query string so the full composition is preserved.
//
// Read-only backends commonly include the query parameter ro=true to indicate
// the underlying scheme is read-only.
//
// Decorators record themselves, in the order they were applied, in the options
// query parameter via ufs.AppendURIOption so that ufs.New applies them again.
type URIGet interface {
	// URI returns the canonical identifier for this file system as a [*url.URL].
	//
	// The result is intended to be stable and round-trippable: if the backend has
	// a meaningful external identity, calling ufs.New with u.String() should
	// reconstruct an equivalent file system with the same semantics and mount
	// composition. This allows a file system to be serialized, logged, cached, or
	// re-opened without losing configuration details.
	//
	// A nil value indicates the backend has no meaningful URI. This is common for
	// synthetic adapters or wrappers that exist only in memory and cannot be
	// reconstructed from a single URL (for example, readwrapfs.FromFS). In that
	// case,
	// callers should treat the result as absent and fall back to a default value or
	// another source of configuration.
	//
	// A non-nil error means the backend could not construct a URI at this time.
	// Errors are distinct from a nil URL: a nil URL is an intentional "no
	// identifier" state, while an error indicates that the implementation failed to
	// determine or format the identifier.
	//
	// Wrappers such as nestFS include mount information in the query string so the
	// full virtual composition is preserved. Read-only backends commonly include a
	// ro=true query parameter to indicate that writes are not supported.
	URI() (*url.URL, error)
}
