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

// Package uri adds and removes query parameters on a URI while keeping every
// parameter that is not addressed byte for byte: the ones that stay hold their
// position and their escaping exactly as they were written, so a URI signed over
// its exact query string is still valid after a layer has taken the parameter
// that belongs to it and put one of its own back. The standard library
// url.Values cannot do this, because Values.Encode sorts the parameters and
// re-encodes each of them, which breaks such a signature.
//
// It holds the decorator option list of a file system too, because a decorator
// records itself in the URI it reports and must not import the base package to
// do it. The package still has no dependency on the base ufs package — its only
// ufs import is drivers/types, which does not import the base package either —
// so there is no import cycle in either direction and both the base package and
// the drivers can use it.
package uri

import (
	"fmt"
	"net/url"
	"slices"
	"strings"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/httputil"
	"gopkg.in/yaml.v3"
)

// Params holds URI query parameters that were taken out of a URI, keyed by the
// decoded parameter name rather than by the escaping it happened to be written
// in. The value is the text that was written, decoded once from its
// percent-escaping; a pair written without a value carries the string "true".
// See [RemoveParameter].
type Params map[string]string

// RemoveParameter removes from name the query pairs whose parameter is one of
// params, and returns the reduced name and the removed values keyed by the
// decoded name. A pair is claimed when its name matches one of params after
// its percent-escaping is decoded, so claiming "ro" removes "?r%6F=true" but
// not "?ufs.ro=true": a caller that wants a parameter addressed to ufs puts the
// "ufs." prefix in front of its own claim. A pair written without a value
// ("?ro") yields the string "true", because a parameter written without one
// reads as switched on. A repeated parameter is taken once, by its first value,
// the way ufs reads a repeated parameter.
//
// Every pair that is not claimed keeps its position and its escaping byte for
// byte. That is the point of this function rather than url.Values: a caller
// strips the parameters that belong to it from a URI that may be signed over its
// exact query string, and Values.Encode sorts and re-encodes the rest.
//
// name is read as a URI string: the query runs from the first "?" to the first
// "#" that follows it, and a "?" written after that "#" stays part of the
// fragment, the way [url.Parse] reads a name. A parameter holding another URI
// keeps the nested "?" inside its own value.
func RemoveParameter(name string, params []string) (reduced string, values Params, err error) {
	head, query, fragment, marker := splitQueryPart(name)
	if marker == "" || len(params) == 0 {
		return name, nil, nil
	}
	pairs := httputil.SplitQueryParts(query)
	kept := make([]httputil.QueryPair, 0, len(pairs))
	for _, pair := range pairs {
		key, err := pair.Key()
		if err != nil || !slices.Contains(params, key) {
			// A pair whose name cannot be decoded cannot be claimed, so it stays
			// where it is for the writer that produced it to explain.
			kept = append(kept, pair)
			continue
		}
		value, err := pair.Value()
		if err != nil {
			return name, nil, fmt.Errorf("invalid value for query parameter %q, %w", key, err)
		}
		if !pair.HasValue {
			value = "true"
		}
		if values == nil {
			values = Params{}
		}
		if _, seen := values[key]; !seen {
			values[key] = value
		}
	}
	if len(kept) == len(pairs) {
		// Nothing was claimed, so the name goes back exactly as it came, even a
		// query that this function would otherwise have re-formatted.
		return name, nil, nil
	}
	return joinQueryPart(head, fragment, marker, kept), values, nil
}

// AddParameter returns a copy of u with one query pair added, param=value. It
// replaces the hand-rolled "u.Query(); v.Set(...); u.RawQuery = v.Encode()"
// pattern that several file systems use for their own parameter, because that
// pattern sorts and re-encodes every other parameter in the query, which breaks
// a URI signed over its exact query string.
//
// A nil u yields nil and no error, so a caller that has no URI to address a
// parameter to gets nil back rather than a URL invented out of nothing.
//
// param is the name in its decoded form: it is escaped before it goes into the
// query, so a caller that wants a parameter addressed to ufs rather than to the
// file system spells it with the "ufs." prefix that ufs strips from a URI
// before handing it to a driver, and a name that needs no escaping comes back
// byte for byte. value is query-escaped here and must not have been escaped
// already, which writes a space as "+".
//
// The result carries exactly one pair for param: every pair whose decoded name
// is param goes, and the new pair takes the place of the first of them, so the
// pairs around it are not moved. A pair whose name cannot be percent-decoded
// matches nothing and stays where it is for its writer to explain. Only
// RawQuery of the copy differs from u.
func AddParameter(u *url.URL, param, value string) (*url.URL, error) {
	if u == nil {
		return nil, nil
	}
	// A name holding a separator would be escaped into a single pair whose name
	// is not the one its caller asked for, so it is refused rather than encoded.
	if param == "" || strings.ContainsAny(param, "=&") {
		return nil, fmt.Errorf("cannot add URI query parameter %q to %q, a parameter name has no separator and is not empty", param, u)
	}
	escapedKey := url.QueryEscape(param)
	escapedValue := url.QueryEscape(value)
	replacement := httputil.QueryPair{
		Raw:      escapedKey + "=" + escapedValue,
		RawKey:   escapedKey,
		RawValue: escapedValue,
		HasValue: true,
	}
	pairs := httputil.SplitQueryParts(u.RawQuery)
	kept := make([]httputil.QueryPair, 0, len(pairs)+1)
	written := false
	for _, pair := range pairs {
		key, err := pair.Key()
		if err == nil && key == param {
			if !written {
				kept = append(kept, replacement)
				written = true
			}
			continue
		}
		kept = append(kept, pair)
	}
	if !written {
		kept = append(kept, replacement)
	}
	out := *u
	out.RawQuery = httputil.JoinQuery(kept)
	return &out, nil
}

// OptionsParam is the URI query parameter that holds the decorators of a file
// system, in the order they are applied. Its value is the decorator list in
// YAML flow form, e.g. ufs.options=[{readOnly: true}], see [Option]. A query
// parameter written without the "ufs." prefix names a mount point inside the
// file system rather than a setting of it, so the parameter carries that
// prefix, and an un-prefixed "options" pair is left exactly where it is.
const OptionsParam = "ufs.options"

// Option configures one decorator of a file system. In YAML it is a mapping
// with a single key, the decorator's name, whose value is the decorator's
// configuration:
//
//	options:
//	  - readOnly: true
//	  - fault:
//	      errorRate: 0.25
//
// The base package lists these as the options of a mount spec under the name
// ufs.MountOption, which is this type.
type Option struct {
	// Name is the name of a registered file system decorator.
	Name string

	// Config is the decorator's raw configuration, in whatever form that
	// decorator's own options type reads; the base package hands it to
	// ufs.DecodeOptions when it applies the decorator from a mount spec.
	Config any
}

// UnmarshalYAML reads the single-key mapping form of an [Option].
func (o *Option) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode || len(node.Content) != 2 {
		return fmt.Errorf("line %d: a mount option must be a mapping with a single decorator name", node.Line)
	}
	if err := node.Content[0].Decode(&o.Name); err != nil {
		return err
	}
	return node.Content[1].Decode(&o.Config)
}

// MarshalYAML writes the single-key mapping form of an [Option].
func (o Option) MarshalYAML() (any, error) {
	return map[string]any{o.Name: o.Config}, nil
}

// OrDefault returns fsys's canonical URI, or value if fsys reports none. A file
// system that owns its data reports a URI even when no URI can name it, as
// [types.URIGet] requires, so value is used when fsys fails or is a wrapper of
// a file system that reported none.
func OrDefault(fsys types.URIGet, value string) string {
	u, err := fsys.URI()
	if err != nil || u == nil {
		return value
	}
	return u.String()
}

// AppendOption returns a copy of u that also records a decorator named name with
// the given config, so that the base package applies the decorator again when
// it is given the result. A decorator calls it from its URI method with the URI
// of the file system it wraps; calling it once per layer keeps the decorators in
// the order they were applied. A nil u yields nil, which is the case of a
// decorator wrapping a file system that reported no URI of its own.
//
// The parameters u already carries keep their order and escaping: [AddParameter]
// writes the options pair into the raw query rather than rebuilding it through
// [url.Values], because a URI can be signed over its exact query string. The
// decorators already recorded are read from the first options pair and written
// back in its place, and any further options pair goes with it, so the result
// carries one options pair listing the decorators in order.
func AppendOption(u *url.URL, name string, config any) (*url.URL, error) {
	if u == nil {
		return nil, nil
	}
	var written string
	if pair := optionQueryPair(u.RawQuery); pair != nil {
		value, err := pair.Value()
		if err != nil {
			return nil, fmt.Errorf("cannot read the options of a file system decorator, %w", err)
		}
		written = value
	}
	opts, err := decodeOptions(written)
	if err != nil {
		return nil, err
	}
	encoded, err := encodeOptions(append(opts, Option{Name: name, Config: config}))
	if err != nil {
		return nil, fmt.Errorf("cannot encode options of file system decorator %q, %w", name, err)
	}
	return AddParameter(u, OptionsParam, encoded)
}

// RemoveOptions removes the options query parameter from name and returns the
// reduced name and the decorators it lists. A name without the parameter is
// returned unchanged with no decorators. Every pair of the parameter goes, the
// way [RemoveParameter] removes a repeated parameter, and the decorators come
// from the first value written. The other parameters keep their order and
// escaping, which is why the name is reduced pair by pair instead of through
// url.Values.
//
// A pair written without a value ("?ufs.options") is read as the string "true"
// by the rule [RemoveParameter] states, which is not a decorator list, so this
// function reports it as an invalid value rather than reading it as a request
// for no decorators. Whether that report reaches the caller of the base
// package's New depends on what New does when its first attempt to open a name
// fails, which is not this function's choice to make.
func RemoveOptions(name string) (reduced string, opts []Option, err error) {
	reduced, values, err := RemoveParameter(name, []string{OptionsParam})
	if err != nil {
		return name, nil, err
	}
	written, ok := values[OptionsParam]
	if !ok {
		return name, nil, nil
	}
	opts, err = decodeOptions(written)
	if err != nil {
		return name, nil, err
	}
	return reduced, opts, nil
}

// optionQueryPair returns the first pair of rawQuery whose parameter is the
// options parameter, or nil when the query has none.
func optionQueryPair(rawQuery string) *httputil.QueryPair {
	pairs := httputil.SplitQueryParts(rawQuery)
	if at := httputil.IndexOfQueryKey(pairs, OptionsParam); at >= 0 {
		return &pairs[at]
	}
	return nil
}

// encodeOptions renders opts as a single-line YAML flow sequence, e.g.
// [{readOnly: true}, {fault: {errorRate: 0.25}}].
func encodeOptions(opts []Option) (encoded string, err error) {
	// yaml panics on values it cannot represent (e.g. channels and funcs),
	// which is reported as an error instead.
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	var node yaml.Node
	if err := node.Encode(opts); err != nil {
		return "", err
	}
	setFlowStyle(&node)
	data, err := yaml.Marshal(&node)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(data)), nil
}

func setFlowStyle(node *yaml.Node) {
	node.Style |= yaml.FlowStyle
	for _, child := range node.Content {
		setFlowStyle(child)
	}
}

// decodeOptions parses the value written by encodeOptions.
func decodeOptions(raw string) ([]Option, error) {
	var opts []Option
	if err := yaml.Unmarshal([]byte(raw), &opts); err != nil {
		return nil, fmt.Errorf("invalid %s query parameter %q, %w", OptionsParam, raw, err)
	}
	return opts, nil
}

// splitQueryPart splits a URI string into the text before its query, the query,
// the fragment with its leading "#", and the query marker. The marker is "?"
// when the name carries a query and empty when it does not. The fragment runs
// from the first "#" to the end of the name, so only a "?" written before it
// marks a query: a parameter whose value is another URI keeps that URI's "?"
// inside the value, and a "?" written inside the fragment stays there.
func splitQueryPart(name string) (head, query, fragment, marker string) {
	body, fragment := name, ""
	if hash := strings.IndexByte(name, '#'); hash >= 0 {
		body, fragment = name[:hash], name[hash:]
	}
	start := strings.IndexByte(body, '?')
	if start < 0 {
		return name, "", "", ""
	}
	return name[:start], body[start+1:], fragment, "?"
}

// joinQueryPart writes a reduced query back between the marker it came with and
// the fragment. A query with nothing left to write loses its marker, the way
// [url.URL.String] prints an empty query: pairs kept as written can still join
// to nothing ("?ro=true&" reduced to one empty pair), and a removal must not
// leave a bare "?" behind.
func joinQueryPart(head, fragment, marker string, pairs []httputil.QueryPair) string {
	query := httputil.JoinQuery(pairs)
	if query == "" {
		marker = ""
	}
	return head + marker + query + fragment
}
