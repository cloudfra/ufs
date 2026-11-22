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
// Every file system reports a URI: URI returns a non-nil URL unless it returns
// an error. A file system that holds its own data but has no locatable source —
// a synthetic adapter, or embed.FS — reports a URL whose scheme names its type
// and nothing else, which is the "type://" form written as [*url.URL] fields:
// the empty authority renders as nothing, so a scheme of "embed" prints as
// "embed:". That URI names the value without promising that ufs.New can reopen
// it, which is what a caller storing a durable configuration value needs to
// know. A wrapper has no type of its own to name, so it reports the layers it
// adds on top of the wrapped URI and returns an error when the wrapped file
// system has no URI to build on.
//
// A nil URL is not an accepted answer. A wrapper builds its own URI out of the
// URI of the file system it wraps, so a nil result would force every wrapper to
// invent one. [github.com/cloudfra/ufs/drivers/polyfills.URI] is the shared
// answer to that question: it reports the URI a file system reports, or one
// derived from a standard library file system whose source is known, or an error
// naming the type. The drivers/testing URI suite reports a nil URL as a failure
// of the driver under test.
//
// A non-nil error means the file system could not construct a URI at this
// time. It is a failure to answer, not an answer that there is no locatable
// source.
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
	// The URL is non-nil. A backend with no locatable source reports a URL with
	// only its type as the scheme rather than nil, so a wrapper of it can still
	// report the layers it adds.
	//
	// A non-nil error means the backend could not construct a URI at this time.
	// It is a failure to answer, not an answer that there is no locatable source.
	//
	// Wrappers such as nestFS include mount information in the query string so the
	// full virtual composition is preserved. Read-only backends commonly include a
	// ro=true query parameter to indicate that writes are not supported.
	URI() (*url.URL, error)
}
