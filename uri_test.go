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

package ufs

import (
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
	"gopkg.in/yaml.v3"
)

type uriGetter struct {
	name string
}

func (u *uriGetter) URI() (*url.URL, error) {
	return url.Parse(u.name)
}

func TestUriOrDefault(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		input string
		value string
		want  string
	}{
		{
			input: "",
			value: "",
			want:  "",
		},
		{
			input: "",
			value: "memory://default",
			want:  "",
		},
		{
			input: "file:///tmp/default",
			value: "",
			want:  "file:///tmp/default",
		},
		{
			input: "://example.com",
			value: "memory://fallback",
			want:  "memory://fallback",
		},
		{
			input: "https://example.com",
			value: "memory://fallback",
			want:  "https://example.com",
		},
		{
			input: "https://example.com/path?q=1",
			value: "memory://fallback",
			want:  "https://example.com/path?q=1",
		},
		{
			input: "file:///tmp/example.txt",
			value: "memory://fallback",
			want:  "file:///tmp/example.txt",
		},
		{
			input: "\\:broken:\\",
			value: "memory://fallback",
			want:  "memory://fallback",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			ug := &uriGetter{
				name: tc.input,
			}
			got := URIOrDefault(ug, tc.value)
			if diff := cmp.Diff(got, tc.want); diff != "" {
				t.Errorf("got: %q, want: %q, diff: %q", got, tc.want, diff)
			}
		})
	}
}

func mustParseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("url.Parse(%q) = %v, want nil", raw, err)
	}
	return u
}

func TestAppendURIOption(t *testing.T) {
	t.Parallel()

	t.Run("nil URI", func(t *testing.T) {
		t.Parallel()
		got, err := AppendURIOption(nil, "readOnly", true)
		if got != nil || err != nil {
			t.Errorf("AppendURIOption(nil) = %v, %v, want nil, nil", got, err)
		}
	})

	t.Run("keeps the order of the layers", func(t *testing.T) {
		t.Parallel()
		base := mustParseURL(t, "memory://test?cache=null%3A&ro=true")
		first, err := AppendURIOption(base, "readOnly", true)
		if err != nil {
			t.Fatalf("AppendURIOption(readOnly) = %v, want nil", err)
		}
		second, err := AppendURIOption(first, "fault", tagOptions{Label: "x", Timeout: 1500 * time.Millisecond})
		if err != nil {
			t.Fatalf("AppendURIOption(fault) = %v, want nil", err)
		}
		if got, want := base.String(), "memory://test?cache=null%3A&ro=true"; got != want {
			t.Errorf("AppendURIOption() changed its input to %q, want %q", got, want)
		}
		if got, want := first.Query().Get(optionsQueryParam), "[{readOnly: true}]"; got != want {
			t.Errorf("options after one layer = %q, want %q", got, want)
		}
		query := second.Query()
		if got, want := query.Get(optionsQueryParam), "[{readOnly: true}, {fault: {label: x, timeout: 1.5s, fail: false}}]"; got != want {
			t.Errorf("options after two layers = %q, want %q", got, want)
		}
		if got := query.Get("cache"); got != "null:" {
			t.Errorf("cache query parameter = %q, want it preserved", got)
		}
		if got := query.Get(roQueryParam); got != "true" {
			t.Errorf("ro query parameter = %q, want it preserved", got)
		}

		name, opts, err := splitURIOptions(second.String())
		if err != nil {
			t.Fatalf("splitURIOptions() = %v, want nil", err)
		}
		if want := "memory://test?cache=null%3A&ro=true"; name != want {
			t.Errorf("splitURIOptions() name = %q, want %q", name, want)
		}
		wantOpts := []MountOption{
			{Name: "readOnly", Config: true},
			{Name: "fault", Config: map[string]any{"label": "x", "timeout": "1.5s", "fail": false}},
		}
		if diff := cmp.Diff(wantOpts, opts); diff != "" {
			t.Errorf("splitURIOptions() options mismatch (-want +got):\n%s", diff)
		}
	})

	t.Run("long options stay on one line", func(t *testing.T) {
		t.Parallel()
		u, err := AppendURIOption(mustParseURL(t, "memory://test"), "fault", tagOptions{Label: strings.Repeat("long label ", 30)})
		if err != nil {
			t.Fatalf("AppendURIOption() = %v, want nil", err)
		}
		if got := u.Query().Get(optionsQueryParam); strings.Contains(got, "\n") {
			t.Errorf("options = %q, want a single line", got)
		}
		_, opts, err := splitURIOptions(u.String())
		if err != nil || len(opts) != 1 {
			t.Fatalf("splitURIOptions() = %v, %v, want one option", opts, err)
		}
		got, err := DecodeOptions[tagOptions](opts[0].Config)
		if err != nil || got.Label != strings.Repeat("long label ", 30) {
			t.Errorf("round-tripped label = %q, %v, want it unchanged", got.Label, err)
		}
	})

	t.Run("existing options are invalid", func(t *testing.T) {
		t.Parallel()
		_, err := AppendURIOption(mustParseURL(t, "memory://test?options=%5Bnot"), "readOnly", true)
		if err == nil || !strings.Contains(err.Error(), "invalid options query parameter") {
			t.Errorf("AppendURIOption() = %v, want an invalid options error", err)
		}
	})

	t.Run("options cannot be encoded", func(t *testing.T) {
		t.Parallel()
		for _, config := range []any{make(chan int), failingMarshaler{}} {
			_, err := AppendURIOption(mustParseURL(t, "memory://test"), "fault", config)
			if err == nil || !strings.Contains(err.Error(), `cannot encode options of file system decorator "fault"`) {
				t.Errorf("AppendURIOption(%T) = %v, want an encode error", config, err)
			}
		}
	})
}

func TestSplitURIOptions(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		input    string
		wantName string
		wantOpts []MountOption
		wantErr  string
	}{
		{
			name:     "no options",
			input:    "memory://test?cache=null%3A",
			wantName: "memory://test?cache=null%3A",
		},
		{
			name:     "options",
			input:    "memory://test?options=%5B%7BreadOnly%3A+true%7D%5D",
			wantName: "memory://test",
			wantOpts: []MountOption{{Name: "readOnly", Config: true}},
		},
		{
			name:     "empty options",
			input:    "memory://test?options=",
			wantName: "memory://test",
		},
		{
			name:     "options= inside another parameter",
			input:    "memory://test?cache=options=1",
			wantName: "memory://test?cache=options=1",
		},
		{
			name:     "not a URL",
			input:    "%zz?options=1",
			wantName: "%zz?options=1",
		},
		{
			name:     "invalid options",
			input:    "memory://test?options=%5Bnot",
			wantName: "memory://test?options=%5Bnot",
			wantErr:  "invalid options query parameter",
		},
		{
			name:     "option with two names",
			input:    "memory://test?options=" + url.QueryEscape("[{readOnly: true, fault: {}}]"),
			wantName: "memory://test?options=" + url.QueryEscape("[{readOnly: true, fault: {}}]"),
			wantErr:  "a mount option must be a mapping with a single decorator name",
		},
		{
			name:     "option that is not a mapping",
			input:    "memory://test?options=" + url.QueryEscape("[readOnly]"),
			wantName: "memory://test?options=" + url.QueryEscape("[readOnly]"),
			wantErr:  "a mount option must be a mapping with a single decorator name",
		},
		{
			name:     "option name is not a string",
			input:    "memory://test?options=" + url.QueryEscape("[{[a]: true}]"),
			wantName: "memory://test?options=" + url.QueryEscape("[{[a]: true}]"),
			wantErr:  "invalid options query parameter",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			name, opts, err := splitURIOptions(tc.input)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("splitURIOptions(%q) = %v, want nil", tc.input, err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("splitURIOptions(%q) = %v, want substring %q", tc.input, err, tc.wantErr)
			}
			if name != tc.wantName {
				t.Errorf("splitURIOptions(%q) name = %q, want %q", tc.input, name, tc.wantName)
			}
			if diff := cmp.Diff(tc.wantOpts, opts); diff != "" {
				t.Errorf("splitURIOptions(%q) options mismatch (-want +got):\n%s", tc.input, diff)
			}
		})
	}
}

func TestMountOptionYAML(t *testing.T) {
	t.Parallel()
	want := []MountOption{
		{Name: "readOnly", Config: true},
		{Name: "fault", Config: map[string]any{"errorRate": 0.25}},
		{Name: "empty"},
	}
	data, err := yaml.Marshal(want)
	if err != nil {
		t.Fatalf("yaml.Marshal() = %v, want nil", err)
	}
	var got []MountOption
	if err := yaml.Unmarshal(data, &got); err != nil {
		t.Fatalf("yaml.Unmarshal(%q) = %v, want nil", data, err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("round trip mismatch (-want +got):\n%s", diff)
	}
}

func TestNewAppliesURIOptions(t *testing.T) {
	name := fmt.Sprintf("uriTestDecorator%d", registerTestCounter.Add(1))
	RegisterDecorator(tagDecorator(name))
	options := url.QueryEscape("[{" + name + ": {label: fromURI}}]")

	t.Run("root", func(t *testing.T) {
		fsys, err := New(t.Context(), "memory://test?options="+options)
		if err != nil {
			t.Fatalf("New() = %v, want nil", err)
		}
		defer ufsTesting.ValidateClose(t, fsys)()
		if got, want := tags(fsys.(*nestFS).fsys), []string{name + "=fromURI"}; !cmp.Equal(got, want) {
			t.Errorf("New() applied %v to the root, want %v", got, want)
		}
	})

	t.Run("mount", func(t *testing.T) {
		fsys, err := New(t.Context(), "memory://test?data="+url.QueryEscape("memory://data?options="+options))
		if err != nil {
			t.Fatalf("New() = %v, want nil", err)
		}
		defer ufsTesting.ValidateClose(t, fsys)()
		nFS := fsys.(*nestFS)
		if got := tags(nFS.fsys); len(got) != 0 {
			t.Errorf("New() applied %v to the root, want none", got)
		}
		mount, ok := nFS.mounts.m["data"]
		if !ok {
			t.Fatalf("New() mounts = %v, want a data mount", nFS.mounts.m)
		}
		if got, want := tags(mount.fsys), []string{name + "=fromURI"}; !cmp.Equal(got, want) {
			t.Errorf("New() applied %v to the mount, want %v", got, want)
		}
	})

	t.Run("mount spec source", func(t *testing.T) {
		fsys, err := New(t.Context(), "- source: \"memory://test?options="+options+"\"")
		if err != nil {
			t.Fatalf("New() = %v, want nil", err)
		}
		defer ufsTesting.ValidateClose(t, fsys)()
		if got, want := tags(fsys.(*nestFS).fsys), []string{name + "=fromURI"}; !cmp.Equal(got, want) {
			t.Errorf("New() applied %v to the root, want %v", got, want)
		}
	})

	t.Run("invalid options", func(t *testing.T) {
		_, err := New(t.Context(), "memory://test?options=%5Bnot")
		if err == nil || !strings.Contains(err.Error(), "invalid options query parameter") {
			t.Errorf("New() = %v, want an invalid options error", err)
		}
	})
}
