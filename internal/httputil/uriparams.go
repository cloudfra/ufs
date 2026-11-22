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
	"fmt"
	"net/url"
	"strings"
)

// URIParamPrefix starts the name of a query parameter addressed to ufs itself
// rather than to the server or file system holding the data. A driver that
// claims one of these parameters takes it out of the URI it is given, so a
// caller can pass one URI through several layers without each layer seeing the
// others' settings.
const URIParamPrefix = "ufs."

// QueryPair is one pair of a URI query, held in the exact spelling it was
// written in. A pair is kept as text because a URI can be signed over its exact
// query string: url.Values cannot round trip one, since Values.Encode sorts the
// parameters and re-encodes each of them, and url.Query drops a malformed pair
// instead of reporting it. Both would break a signature over a query ufs only
// read part of.
type QueryPair struct {
	// Raw is the pair as written, escaping included.
	Raw string

	// RawKey and RawValue are the two sides of Raw split at its first "=".
	RawKey   string
	RawValue string

	// HasValue is false for a pair written without "=", which carries no value
	// of its own. Readers decide what presence alone means; the walk does not
	// invent a value for it.
	HasValue bool
}

// SplitQueryParts splits a raw query string into its pairs, keeping the order
// and the escaping of each one. An empty query has no pairs.
func SplitQueryParts(rawQuery string) []QueryPair {
	if rawQuery == "" {
		return nil
	}
	pairs := make([]QueryPair, 0, strings.Count(rawQuery, "&")+1)
	for pair := range strings.SplitSeq(rawQuery, "&") {
		rawKey, rawValue, hasValue := strings.Cut(pair, "=")
		pairs = append(pairs, QueryPair{
			Raw:      pair,
			RawKey:   rawKey,
			RawValue: rawValue,
			HasValue: hasValue,
		})
	}
	return pairs
}

// JoinQuery writes pairs back as a query string, byte for byte.
func JoinQuery(pairs []QueryPair) string {
	if len(pairs) == 0 {
		return ""
	}
	var b strings.Builder
	for i, pair := range pairs {
		if i > 0 {
			b.WriteByte('&')
		}
		b.WriteString(pair.Raw)
	}
	return b.String()
}

// Key returns the decoded parameter name of the pair. An escaped sequence that
// is not valid percent-encoding is an error, so a malformed name is reported
// rather than skipped.
func (p QueryPair) Key() (string, error) {
	key, err := url.QueryUnescape(p.RawKey)
	if err != nil {
		return "", fmt.Errorf("invalid query parameter name %q: %w", p.RawKey, err)
	}
	return key, nil
}

// Value returns the decoded value of the pair. A pair written without "=" has
// an empty value; a reader that treats a bare parameter as switched on has to
// say so itself.
func (p QueryPair) Value() (string, error) {
	value, err := url.QueryUnescape(p.RawValue)
	if err != nil {
		return "", fmt.Errorf("invalid query parameter value %q: %w", p.RawValue, err)
	}
	return value, nil
}

// IndexOfQueryKey returns the index of the first pair in pairs whose decoded
// name is name, or -1 if there is none. A pair whose name cannot be decoded
// matches nothing, which leaves a malformed pair in place for its writer to
// explain.
func IndexOfQueryKey(pairs []QueryPair, name string) int {
	for i, pair := range pairs {
		key, err := pair.Key()
		if err != nil {
			continue
		}
		if key == name {
			return i
		}
	}
	return -1
}
