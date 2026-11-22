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

package uri

import (
	"errors"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) = %v, want nil", raw, err)
	}
	return u
}

func TestRemoveParameter(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		input      string
		params     []string
		wantName   string
		wantValues Params
		wantErr    string
	}{
		{
			name:     "no query",
			input:    "memory://test",
			params:   []string{"ro"},
			wantName: "memory://test",
		},
		{
			name:     "no claim list",
			input:    "memory://test?ro=true",
			wantName: "memory://test?ro=true",
		},
		{
			name:     "nothing claimed is byte for byte unchanged",
			input:    "memory://test?b=two%20one&a=1",
			params:   []string{"ro"},
			wantName: "memory://test?b=two%20one&a=1",
		},
		{
			// The query marker of a name that carries no pair is left alone too,
			// because a name nothing was taken from is returned as it came.
			name:     "nothing claimed from an empty query",
			input:    "memory://test?",
			params:   []string{"ro"},
			wantName: "memory://test?",
		},
		{
			name:       "claimed pair is removed where it stands",
			input:      "memory://test?b=two%20one&ro=true&a=1",
			params:     []string{"ro"},
			wantName:   "memory://test?b=two%20one&a=1",
			wantValues: Params{"ro": "true"},
		},
		{
			name:       "bare parameter reads as true",
			input:      "memory://test?ro&b=2",
			params:     []string{"ro"},
			wantName:   "memory://test?b=2",
			wantValues: Params{"ro": "true"},
		},
		{
			// A pair written with "=" but no text has the empty value, which is not
			// the same answer as a pair written without "=" at all.
			name:       "empty value stays empty",
			input:      "memory://test?ro=&b=2",
			params:     []string{"ro"},
			wantName:   "memory://test?b=2",
			wantValues: Params{"ro": ""},
		},
		{
			name:       "repeated parameter is taken by its first value",
			input:      "memory://test?ro=false&a=1&ro=true",
			params:     []string{"ro"},
			wantName:   "memory://test?a=1",
			wantValues: Params{"ro": "false"},
		},
		{
			name:       "a different name is not claimed",
			input:      "memory://test?ro=true&ufs.ro=false",
			params:     []string{"ro"},
			wantName:   "memory://test?ufs.ro=false",
			wantValues: Params{"ro": "true"},
		},
		{
			// The name is compared after its percent-escaping is decoded, which is
			// how every reader of the query sees it.
			name:       "escaped name is claimed after decoding",
			input:      "memory://test?r%6F=true&a=1",
			params:     []string{"ro"},
			wantName:   "memory://test?a=1",
			wantValues: Params{"ro": "true"},
		},
		{
			// Matching is on the decoded name and still case sensitive: a parameter
			// that differs only by case belongs to whoever wrote it, so it stays
			// where it stands.
			name:       "case differs and is not claimed",
			input:      "memory://test?RO=true&ro=false",
			params:     []string{"ro"},
			wantName:   "memory://test?RO=true",
			wantValues: Params{"ro": "false"},
		},
		{
			name:       "prefixed parameter is claimed when asked for",
			input:      "memory://test?ro=true&ufs.ro=false",
			params:     []string{"ufs.ro"},
			wantName:   "memory://test?ro=true",
			wantValues: Params{"ufs.ro": "false"},
		},
		{
			// A caller may hand over several of its own parameters at once. Each of
			// them goes wherever it stands and the pairs in between stay put, so a
			// layer with more than one parameter does not have to call this per
			// parameter and rebuild the query each time.
			name:       "several names claimed in one pass",
			input:      "memory://test?b=two%20one&ro=true&a=1&options=%5B%7BreadOnly%3A+true%7D%5D",
			params:     []string{"options", "ro"},
			wantName:   "memory://test?b=two%20one&a=1",
			wantValues: Params{"ro": "true", "options": "[{readOnly: true}]"},
		},
		{
			// A name in the claim list that the query does not carry claims
			// nothing; the names the query does carry are still taken.
			name:       "claim list holding a name the query does not carry",
			input:      "memory://test?ro=true&a=1",
			params:     []string{"ro", "ufs.ro"},
			wantName:   "memory://test?a=1",
			wantValues: Params{"ro": "true"},
		},
		{
			name:       "value holding separators stays in one pair",
			input:      "memory://test?sig=a%26b%3Dc&ro=true",
			params:     []string{"ro"},
			wantName:   "memory://test?sig=a%26b%3Dc",
			wantValues: Params{"ro": "true"},
		},
		{
			name:       "parameter holding a nested URI keeps its query",
			input:      "memory://test?data=null%3A%2F%2Fh%3Fa%3D1&ro=true",
			params:     []string{"ro"},
			wantName:   "memory://test?data=null%3A%2F%2Fh%3Fa%3D1",
			wantValues: Params{"ro": "true"},
		},
		{
			name:       "fragment stays after the query",
			input:      "memory://test?ro=true#a=b",
			params:     []string{"ro"},
			wantName:   "memory://test#a=b",
			wantValues: Params{"ro": "true"},
		},
		{
			// A "?" behind a "#" sits inside the fragment, which is how [url.Parse]
			// and [httputil.ParseURI] read a name, so there is no query here to take
			// from and the name goes back as it came.
			name:     "query marker inside a fragment",
			input:    "mem://x#frag?ro=true",
			params:   []string{"ro"},
			wantName: "mem://x#frag?ro=true",
		},
		{
			name:       "last pair also loses the query marker",
			input:      "memory://test?ro=true",
			params:     []string{"ro"},
			wantName:   "memory://test",
			wantValues: Params{"ro": "true"},
		},
		{
			// The kept pairs are not what decides the marker, the text they join to
			// is: a leftover empty pair writes nothing, so a bare "?" would be an
			// invention rather than a byte the caller wrote.
			name:       "kept pairs joining to nothing lose the marker",
			input:      "mem://x?ro=true&",
			params:     []string{"ro"},
			wantName:   "mem://x",
			wantValues: Params{"ro": "true"},
		},
		{
			name:       "a pair with an undecodable name stays",
			input:      "memory://test?%zz=1&ro=true",
			params:     []string{"ro"},
			wantName:   "memory://test?%zz=1",
			wantValues: Params{"ro": "true"},
		},
		{
			name:    "claimed pair with an undecodable value is an error",
			input:   "memory://test?ro=%zz",
			params:  []string{"ro"},
			wantErr: "invalid value for query parameter \"ro\"",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, values, err := RemoveParameter(tc.input, tc.params)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("RemoveParameter(%q, %v) = %v, want nil", tc.input, tc.params, err)
			}
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("RemoveParameter(%q, %v) = %v, want substring %q", tc.input, tc.params, err, tc.wantErr)
				}
				if name != tc.input {
					t.Errorf("RemoveParameter(%q, %v) name = %q, want the name unchanged", tc.input, tc.params, name)
				}
				return
			}
			if name != tc.wantName {
				t.Errorf("RemoveParameter(%q, %v) name = %q, want %q", tc.input, tc.params, name, tc.wantName)
			}
			if diff := cmp.Diff(tc.wantValues, values); diff != "" {
				t.Errorf("RemoveParameter(%q, %v) values mismatch (-want +got):\n%s", tc.input, tc.params, diff)
			}
		})
	}
}

func TestAddParameter(t *testing.T) {
	t.Parallel()
	if got, err := AddParameter(nil, "ro", "true"); got != nil || err != nil {
		t.Errorf("AddParameter(nil, ro, true) = %v, %v, want nil, nil", got, err)
	}
	tests := []struct {
		name    string
		uri     string
		param   string
		value   string
		wantURI string
		wantErr bool
	}{
		{
			name:    "pair is added to an empty query",
			uri:     "memory://test",
			param:   "ro",
			value:   "true",
			wantURI: "memory://test?ro=true",
		},
		{
			name:    "pair is appended after the others",
			uri:     "memory://test?b=two%20one&a=1",
			param:   "ro",
			value:   "true",
			wantURI: "memory://test?b=two%20one&a=1&ro=true",
		},
		{
			name:    "existing pair is replaced where it stands",
			uri:     "memory://test?ro=false&a=1",
			param:   "ro",
			value:   "true",
			wantURI: "memory://test?ro=true&a=1",
		},
		{
			// One pair per parameter: the written pair takes the place of the first
			// pair that was there and the further ones go, so a reader that takes
			// the first value and a reader that takes them all read what was just
			// written.
			name:    "repeated pairs collapse into one",
			uri:     "memory://test?ro=false&a=1&ro=true",
			param:   "ro",
			value:   "yes",
			wantURI: "memory://test?ro=yes&a=1",
		},
		{
			name:    "value needing escaping is escaped once",
			uri:     "memory://test",
			param:   "x",
			value:   "%zz",
			wantURI: "memory://test?x=%25zz",
		},
		{
			name:    "empty value writes an empty pair",
			uri:     "memory://test?a=1",
			param:   "x",
			value:   "",
			wantURI: "memory://test?a=1&x=",
		},
		{
			name:    "value is escaped once",
			uri:     "memory://test",
			param:   "sig",
			value:   "a&b=c d",
			wantURI: "memory://test?sig=a%26b%3Dc+d",
		},
		{
			name:    "escaped parameter name is kept escaped",
			uri:     "memory://test",
			param:   "a b",
			value:   "1",
			wantURI: "memory://test?a+b=1",
		},
		{
			name:    "name holding a separator is refused",
			uri:     "memory://test",
			param:   "a=b",
			value:   "1",
			wantErr: true,
		},
		{
			name:    "empty name is refused",
			uri:     "memory://test",
			param:   "",
			value:   "1",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := mustParseURL(t, tc.uri)
			got, err := AddParameter(u, tc.param, tc.value)
			if tc.wantErr {
				if err == nil {
					t.Errorf("AddParameter(%q, %q, %q) = %q, want an error", tc.uri, tc.param, tc.value, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("AddParameter(%q, %q, %q) = %v, want nil", tc.uri, tc.param, tc.value, err)
			}
			if got == nil {
				t.Fatalf("AddParameter(%q, %q, %q) = nil, want a URL", tc.uri, tc.param, tc.value)
			}
			if got.String() != tc.wantURI {
				t.Errorf("AddParameter(%q, %q, %q) = %q, want %q", tc.uri, tc.param, tc.value, got.String(), tc.wantURI)
			}
			if u.String() != tc.uri {
				t.Errorf("AddParameter() changed its input to %q, want %q", u.String(), tc.uri)
			}
		})
	}
}

// TestAddRemoveParameterRoundTrip is the property the pair-preserving walk exists
// for: a parameter ufs adds and takes back leaves the URI byte for byte as it
// was, so a signature over the query of a URI ufs only read part of still holds.
// A base that already carries the parameter comes back without it, because
// [AddParameter] replaces the pair where it stands instead of appending a second.
// Each reduced form is written out here rather than computed with
// RemoveParameter, which is the function under test: for a base that already
// carries "ro", the round trip cannot tell a removal of the added pair from a
// removal of the base's own.
func TestAddRemoveParameterRoundTrip(t *testing.T) {
	t.Parallel()
	tests := []struct {
		base string
		// What RemoveParameter must return once the added "ro" is taken out.
		wantReduced string
	}{
		{"memory://test", "memory://test"},
		{"memory://test?ro=true", "memory://test"},
		{"memory://test?b=two%20one&a=1", "memory://test?b=two%20one&a=1"},
		{"memory://test?sig=a%26b%3Dc+d", "memory://test?sig=a%26b%3Dc+d"},
		{"memory://test?data=null%3A%2F%2Fh%3Fa%3D1", "memory://test?data=null%3A%2F%2Fh%3Fa%3D1"},
		{"memory://test?a=1#frag", "memory://test?a=1#frag"},
		{"mem:", "mem:"},
		{"mem:?ro=false", "mem:"},
		{"mem://x?ro=false&a=1&ro=true", "mem://x?a=1"},
	}
	for _, tc := range tests {
		t.Run(tc.base, func(t *testing.T) {
			t.Parallel()
			u := mustParseURL(t, tc.base)
			tacked, err := AddParameter(u, "ro", "true")
			if err != nil {
				t.Fatalf("AddParameter(%q, ro, true) = %v, want nil", tc.base, err)
			}
			reduced, values, err := RemoveParameter(tacked.String(), []string{"ro"})
			if err != nil {
				t.Fatalf("RemoveParameter(%q, [ro]) = %v, want nil", tacked, err)
			}
			if reduced != tc.wantReduced {
				t.Errorf("RemoveParameter(AddParameter(%q, ro, true)) = %q, want %q", tc.base, reduced, tc.wantReduced)
			}
			if got, want := values["ro"], "true"; got != want {
				t.Errorf("taken ro = %q, want %q", got, want)
			}
		})
	}
}

// TestAddParameterKeepsSignedQuery pins the reason the walk exists: a query a
// server signed over its exact bytes must arrive at the driver unchanged, even
// though ufs added a parameter of its own.
func TestAddParameterKeepsSignedQuery(t *testing.T) {
	t.Parallel()
	// Written by a server, deliberately unsorted and already percent-escaped.
	signed := "sig=R%2Bc3x%3D&Expires=1893456000&a=%2Fb%2Fc"
	u := mustParseURL(t, "https://example.com/f")
	u.RawQuery = signed
	tacked, err := AddParameter(u, "ufs.checksum", "sha256:abc")
	if err != nil {
		t.Fatalf("AddParameter() = %v, want nil", err)
	}
	got := tacked.RawQuery
	if !strings.HasPrefix(got, signed+"&") {
		t.Errorf("AddParameter() query = %q, want the signed prefix %q unchanged", got, signed)
	}
	if want := signed + "&ufs.checksum=sha256%3Aabc"; got != want {
		t.Errorf("AddParameter() query = %q, want %q", got, want)
	}
	// url.Values, the pattern this replaces, sorts the pairs by name, so the
	// signature no longer sits where the server wrote it.
	sorted := u.Query().Encode()
	if sorted == got {
		t.Errorf("url.Values.Encode() = %q, want it to differ from a pair-preserving add", sorted)
	}
	if !strings.HasPrefix(sorted, "Expires=") {
		t.Errorf("url.Values.Encode() = %q, want the pairs sorted by name", sorted)
	}
}

// decoratorOptions is a decorator's configuration type, using the YAML field
// names the "fault" decorator takes. yaml.v3 writes a time.Duration as its
// String() form, which is why a 1500ms timeout reads as "1.5s" in the flow form.
type decoratorOptions struct {
	Label   string        `yaml:"label"`
	Timeout time.Duration `yaml:"timeout"`
	Fail    bool          `yaml:"fail"`
}

type failingMarshaler struct{}

var errMarshal = errors.New("marshal failed")

// MarshalYAML always fails, so an option holding this value cannot be written.
func (failingMarshaler) MarshalYAML() (any, error) {
	return nil, errMarshal
}

func TestAppendOption(t *testing.T) {
	t.Parallel()

	t.Run("nil URI", func(t *testing.T) {
		t.Parallel()
		got, err := AppendOption(nil, "readOnly", true)
		if got != nil || err != nil {
			t.Errorf("AppendOption(nil) = %v, %v, want nil, nil", got, err)
		}
	})

	t.Run("keeps the order of the layers", func(t *testing.T) {
		t.Parallel()
		base := mustParseURL(t, "memory://test?cache=null%3A&ro=true")
		first, err := AppendOption(base, "readOnly", true)
		if err != nil {
			t.Fatalf("AppendOption(readOnly) = %v, want nil", err)
		}
		second, err := AppendOption(first, "fault", decoratorOptions{Label: "x", Timeout: 1500 * time.Millisecond})
		if err != nil {
			t.Fatalf("AppendOption(fault) = %v, want nil", err)
		}
		if got, want := base.String(), "memory://test?cache=null%3A&ro=true"; got != want {
			t.Errorf("AppendOption() changed its input to %q, want %q", got, want)
		}
		if got, want := first.Query().Get(OptionsParam), "[{readOnly: true}]"; got != want {
			t.Errorf("options after one layer = %q, want %q", got, want)
		}
		query := second.Query()
		if got, want := query.Get(OptionsParam), "[{readOnly: true}, {fault: {label: x, timeout: 1.5s, fail: false}}]"; got != want {
			t.Errorf("options after two layers = %q, want %q", got, want)
		}
		if got := query.Get("cache"); got != "null:" {
			t.Errorf("cache query parameter = %q, want it preserved", got)
		}
		if got := query.Get("ro"); got != "true" {
			t.Errorf("ro query parameter = %q, want it preserved", got)
		}

		name, opts, err := RemoveOptions(second.String())
		if err != nil {
			t.Fatalf("RemoveOptions() = %v, want nil", err)
		}
		if want := "memory://test?cache=null%3A&ro=true"; name != want {
			t.Errorf("RemoveOptions() name = %q, want %q", name, want)
		}
		wantOpts := []Option{
			{Name: "readOnly", Config: true},
			{Name: "fault", Config: map[string]any{"label": "x", "timeout": "1.5s", "fail": false}},
		}
		if diff := cmp.Diff(wantOpts, opts); diff != "" {
			t.Errorf("RemoveOptions() options mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("an existing query is not re-encoded", func(t *testing.T) {
		t.Parallel()
		base := mustParseURL(t, "memory://test?sig=a%26b&b=2")
		first, err := AppendOption(base, "readOnly", true)
		if err != nil {
			t.Fatalf("AppendOption(readOnly) = %v, want nil", err)
		}
		// The escaped pair stays escaped and in place; url.Values.Encode would have
		// sorted the pairs and re-escaped the signature.
		if got, want := first.String(), "memory://test?sig=a%26b&b=2&ufs.options=%5B%7BreadOnly%3A+true%7D%5D"; got != want {
			t.Errorf("AppendOption() = %q, want %q", got, want)
		}
		second, err := AppendOption(first, "fault", true)
		if err != nil {
			t.Fatalf("AppendOption(fault) = %v, want nil", err)
		}
		if got, want := second.RawQuery, "sig=a%26b&b=2&ufs.options=%5B%7BreadOnly%3A+true%7D%2C+%7Bfault%3A+true%7D%5D"; got != want {
			t.Errorf("AppendOption() raw query = %q, want %q", got, want)
		}
	})

	t.Run("long options stay on one line", func(t *testing.T) {
		t.Parallel()
		label := strings.Repeat("long label ", 30)
		u, err := AppendOption(mustParseURL(t, "memory://test"), "fault", decoratorOptions{Label: label})
		if err != nil {
			t.Fatalf("AppendOption() = %v, want nil", err)
		}
		if got := u.Query().Get(OptionsParam); strings.Contains(got, "\n") {
			t.Errorf("options = %q, want a single line", got)
		}
		_, opts, err := RemoveOptions(u.String())
		if err != nil || len(opts) != 1 {
			t.Fatalf("RemoveOptions() = %v, %v, want one option", opts, err)
		}
		// What the decorator is handed to decode: the value decoded once, still in
		// the mapping form the YAML writes.
		cfg, ok := opts[0].Config.(map[string]any)
		if !ok || cfg["label"] != label {
			t.Errorf("round-tripped config = %#v, want the label unchanged", opts[0].Config)
		}
	})

	t.Run("existing options are invalid", func(t *testing.T) {
		t.Parallel()
		_, err := AppendOption(mustParseURL(t, "memory://test?ufs.options=%5Bnot"), "readOnly", true)
		if err == nil || !strings.Contains(err.Error(), "invalid ufs.options query parameter") {
			t.Errorf("AppendOption() = %v, want an invalid options error", err)
		}
	})

	t.Run("existing options cannot be decoded", func(t *testing.T) {
		t.Parallel()
		_, err := AppendOption(mustParseURL(t, "memory://test?ufs.options=%zz"), "readOnly", true)
		if err == nil || !strings.Contains(err.Error(), "cannot read the options of a file system decorator") {
			t.Errorf("AppendOption() = %v, want an unreadable options error", err)
		}
	})

	t.Run("repeated options pairs collapse into one", func(t *testing.T) {
		t.Parallel()
		// The decorators are read from the first pair, so a URI that spells the
		// parameter twice would advertise a list no reader takes.
		base := mustParseURL(t, "memory://test?ufs.options=%5B%7BreadOnly%3A+true%7D%5D&ufs.options=%5B%7Bfault%3A+true%7D%5D&a=1")
		first, err := AppendOption(base, "tag", true)
		if err != nil {
			t.Fatalf("AppendOption() = %v, want nil", err)
		}
		if got, want := len(first.Query()[OptionsParam]), 1; got != want {
			t.Errorf("AppendOption() wrote %d options pairs in %q, want %d", got, first.RawQuery, want)
		}
		_, opts, err := RemoveOptions(first.String())
		if err != nil {
			t.Fatalf("RemoveOptions() = %v, want nil", err)
		}
		wantOpts := []Option{{Name: "readOnly", Config: true}, {Name: "tag", Config: true}}
		if diff := cmp.Diff(wantOpts, opts); diff != "" {
			t.Errorf("RemoveOptions() options mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("options cannot be encoded", func(t *testing.T) {
		t.Parallel()
		for _, config := range []any{make(chan int), failingMarshaler{}} {
			_, err := AppendOption(mustParseURL(t, "memory://test"), "fault", config)
			if err == nil || !strings.Contains(err.Error(), `cannot encode options of file system decorator "fault"`) {
				t.Errorf("AppendOption(%T) = %v, want an encode error", config, err)
			}
		}
	})
}

func TestRemoveOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		wantName string
		wantOpts []Option
		wantErr  string
	}{
		{
			name:     "no options",
			input:    "memory://test?cache=null%3A",
			wantName: "memory://test?cache=null%3A",
		},
		{
			name:     "options",
			input:    "memory://test?ufs.options=%5B%7BreadOnly%3A+true%7D%5D",
			wantName: "memory://test",
			wantOpts: []Option{{Name: "readOnly", Config: true}},
		},
		{
			name:     "empty options",
			input:    "memory://test?ufs.options=",
			wantName: "memory://test",
		},
		{
			name:     "options= inside another parameter",
			input:    "memory://test?cache=options=1",
			wantName: "memory://test?cache=options=1",
		},
		{
			// Only the prefixed parameter is the decorator list. Written without
			// the "ufs." prefix, options names a mount point inside the file
			// system, so it stays where it is.
			name:     "options without the prefix is a mount point",
			input:    "memory://test?options=%5B%7BreadOnly%3A+true%7D%5D",
			wantName: "memory://test?options=%5B%7BreadOnly%3A+true%7D%5D",
		},
		{
			// The pairs that stay keep their order and their escaping byte for byte.
			name:     "other parameters keep their order",
			input:    "memory://test?b=two%20one&a=1&ufs.options=%5B%7BreadOnly%3A+true%7D%5D",
			wantName: "memory://test?b=two%20one&a=1",
			wantOpts: []Option{{Name: "readOnly", Config: true}},
		},
		{
			// A parameter whose value is another URI keeps that URI's own "?", and
			// the "?" of the name it sits in.
			name:     "parameter holding a nested URI",
			input:    "memory://test?data=null%3A%2F%2Fhome%3Fa%3D1&ufs.options=%5B%7BreadOnly%3A+true%7D%5D",
			wantName: "memory://test?data=null%3A%2F%2Fhome%3Fa%3D1",
			wantOpts: []Option{{Name: "readOnly", Config: true}},
		},
		{
			name:     "fragment after the query",
			input:    "memory://test?ufs.options=%5B%7BreadOnly%3A+true%7D%5D#a=b",
			wantName: "memory://test#a=b",
			wantOpts: []Option{{Name: "readOnly", Config: true}},
		},
		{
			// A repeated parameter goes entirely, the way [RemoveParameter] takes it.
			// Leaving a second pair behind would hand it to the mount of the reduced
			// name, which applies the decorator a second time.
			name:     "repeated ufs.options pairs all go",
			input:    "memory://test?ufs.options=%5B%7BreadOnly%3A+true%7D%5D&ufs.options=%5B%7Bfault%3A+true%7D%5D&a=1",
			wantName: "memory://test?a=1",
			wantOpts: []Option{{Name: "readOnly", Config: true}},
		},
		{
			// A pair written without "=" carries no list to decode, so it is a bad
			// value rather than a request for no decorators.
			name:     "bare ufs.options parameter",
			input:    "memory://test?ufs.options&b=2",
			wantName: "memory://test?ufs.options&b=2",
			wantErr:  "invalid ufs.options query parameter",
		},
		{
			// The ufs.options parameter is read from the raw name rather than from a
			// parsed URL, so a name that is not a valid URI is still walked. The
			// malformed options value is reported instead of being swallowed.
			name:     "not a URL",
			input:    "%zz?ufs.options=1",
			wantName: "%zz?ufs.options=1",
			wantErr:  "invalid ufs.options query parameter",
		},
		{
			// No pair carries the "ufs." prefix, so there is no decorator list to
			// read and the name comes back as it came: an un-prefixed "ro" is left
			// for the reader that opens a query parameter as a mount point.
			name:     "no ufs.options to read",
			input:    "%zz?ro=true",
			wantName: "%zz?ro=true",
		},
		{
			name:     "invalid options",
			input:    "memory://test?ufs.options=%5Bnot",
			wantName: "memory://test?ufs.options=%5Bnot",
			wantErr:  "invalid ufs.options query parameter",
		},
		{
			name:     "option with two names",
			input:    "memory://test?ufs.options=" + url.QueryEscape("[{readOnly: true, fault: {}}]"),
			wantName: "memory://test?ufs.options=" + url.QueryEscape("[{readOnly: true, fault: {}}]"),
			wantErr:  "a mount option must be a mapping with a single decorator name",
		},
		{
			name:     "option that is not a mapping",
			input:    "memory://test?ufs.options=" + url.QueryEscape("[readOnly]"),
			wantName: "memory://test?ufs.options=" + url.QueryEscape("[readOnly]"),
			wantErr:  "a mount option must be a mapping with a single decorator name",
		},
		{
			name:     "option name is not a string",
			input:    "memory://test?ufs.options=" + url.QueryEscape("[{[a]: true}]"),
			wantName: "memory://test?ufs.options=" + url.QueryEscape("[{[a]: true}]"),
			wantErr:  "invalid ufs.options query parameter",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, opts, err := RemoveOptions(tc.input)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("RemoveOptions(%q) = %v, want nil", tc.input, err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("RemoveOptions(%q) = %v, want substring %q", tc.input, err, tc.wantErr)
			}
			if name != tc.wantName {
				t.Errorf("RemoveOptions(%q) name = %q, want %q", tc.input, name, tc.wantName)
			}
			if diff := cmp.Diff(tc.wantOpts, opts); diff != "" {
				t.Errorf("RemoveOptions(%q) options mismatch (-want +got):\n%s", tc.input, diff)
			}
		})
	}
}

// TestOptionYAML is the single-key mapping form of an [Option] on its own, which
// is what the flow form of the options parameter is built out of.
func TestOptionYAML(t *testing.T) {
	t.Parallel()
	want := []Option{
		{Name: "readOnly", Config: true},
		{Name: "fault", Config: map[string]any{"errorRate": 0.25}},
		{Name: "empty"},
	}
	data, err := yaml.Marshal(want)
	if err != nil {
		t.Fatalf("yaml.Marshal() = %v, want nil", err)
	}
	if got, wantText := string(data), "- readOnly: true\n- fault:\n    errorRate: 0.25\n- empty: null\n"; got != wantText {
		t.Errorf("yaml.Marshal() = %q, want %q", got, wantText)
	}
	var got []Option
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal(%q) = %v, want nil", data, err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}
