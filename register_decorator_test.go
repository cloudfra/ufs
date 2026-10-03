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
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// tagFS records which decorator produced it and what it wraps, so tests can
// read back the order in which decorators were applied.
type tagFS struct {
	WriteFS
	tag string
}

// tagOptions is the options type of the test decorators.
type tagOptions struct {
	Label   string        `yaml:"label"`
	Timeout time.Duration `yaml:"timeout"`
	Fail    bool          `yaml:"fail"`
}

var errTagDecorator = errors.New("tag decorator failed")

func tagDecorator(name string) Decorator {
	return NewDecorator(name, func(_ context.Context, inner WriteFS, opts tagOptions) (WriteFS, error) {
		if opts.Fail {
			return nil, errTagDecorator
		}
		return &tagFS{WriteFS: inner, tag: name + "=" + opts.Label}, nil
	})
}

// tags returns the decorator tags from the outermost wrapper inwards.
func tags(fsys WriteFS) []string {
	var got []string
	for {
		tagged, ok := fsys.(*tagFS)
		if !ok {
			return got
		}
		got = append(got, tagged.tag)
		fsys = tagged.WriteFS
	}
}

func nopDecorate(_ context.Context, inner WriteFS, _ any) (WriteFS, error) {
	return inner, nil
}

func TestRegistrarRegisterDecorator(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		decorator Decorator
		wantError string
	}{
		{
			name:      "empty name",
			decorator: Decorator{CreateFunc: nopDecorate},
			wantError: "empty name",
		},
		{
			name:      "nil CreateFunc",
			decorator: Decorator{Name: "testDecorator"},
			wantError: "empty CreateFunc",
		},
		{
			name:      "upper camel case name",
			decorator: Decorator{Name: "TestDecorator", CreateFunc: nopDecorate},
			wantError: "must be lower camelCase",
		},
		{
			name:      "kebab case name",
			decorator: Decorator{Name: "test-decorator", CreateFunc: nopDecorate},
			wantError: "must be lower camelCase",
		},
		{
			name:      "snake case name",
			decorator: Decorator{Name: "test_decorator", CreateFunc: nopDecorate},
			wantError: "must be lower camelCase",
		},
		{
			name:      "name starts with a digit",
			decorator: Decorator{Name: "2fast", CreateFunc: nopDecorate},
			wantError: "must be lower camelCase",
		},
		{
			name:      "valid",
			decorator: Decorator{Name: "testDecorator2", CreateFunc: nopDecorate},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			r := newRegistrar()
			err := r.registerDecorator(tc.decorator)
			if tc.wantError == "" {
				if err != nil {
					t.Errorf("registerDecorator() = %v, want nil", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("registerDecorator() = %v, want substring %q", err, tc.wantError)
			}
		})
	}
}

func TestRegistrarRegisterDecoratorDuplicate(t *testing.T) {
	t.Parallel()
	r := newRegistrar()
	if err := r.registerDecorator(Decorator{Name: "dupDecorator", CreateFunc: nopDecorate}); err != nil {
		t.Fatalf("first registerDecorator() = %v, want nil", err)
	}
	err := r.registerDecorator(Decorator{Name: "dupDecorator", CreateFunc: nopDecorate})
	if err == nil || !strings.Contains(err.Error(), "already registered") {
		t.Errorf("second registerDecorator() = %v, want substring %q", err, "already registered")
	}
	// Names are case sensitive, so a different casing is a different decorator.
	if err := r.registerDecorator(Decorator{Name: "dupdecorator", CreateFunc: nopDecorate}); err != nil {
		t.Errorf("registerDecorator(dupdecorator) = %v, want nil", err)
	}
}

func TestRegistrarMatchDecorator(t *testing.T) {
	t.Parallel()
	r := newRegistrar()
	if err := r.registerDecorator(tagDecorator("camelCase")); err != nil {
		t.Fatal(err)
	}
	got, err := r.matchDecorator("camelCase")
	if err != nil {
		t.Fatalf("matchDecorator(camelCase) = %v, want nil", err)
	}
	if got.Name != "camelCase" {
		t.Errorf("matchDecorator(camelCase) = %q, want %q", got.Name, "camelCase")
	}
	// The match is strict: a different casing is not the same option.
	for _, name := range []string{"camelcase", "CamelCase", "CAMELCASE", "missing", ""} {
		_, err := r.matchDecorator(name)
		if err == nil {
			t.Fatalf("matchDecorator(%q) = nil error, want error", name)
		}
		for _, want := range []string{"cannot find a ufs file system decorator", "case sensitive", "is imported"} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("matchDecorator(%q) = %q, want substring %q", name, err, want)
			}
		}
	}
}

func TestRegistrarDecorate(t *testing.T) {
	t.Parallel()
	r := newRegistrar()
	var created atomic.Int64
	counting := NewDecorator("counting", func(_ context.Context, inner WriteFS, _ tagOptions) (WriteFS, error) {
		created.Add(1)
		return inner, nil
	})
	for _, d := range []Decorator{tagDecorator("alpha"), tagDecorator("beta"), tagDecorator("gamma"), counting} {
		if err := r.registerDecorator(d); err != nil {
			t.Fatal(err)
		}
	}

	tests := []struct {
		name string
		opts map[string]any
		// want lists the applied decorators from the outermost inwards.
		want      []string
		wantError string
	}{
		{name: "nil options", opts: nil},
		{name: "empty options", opts: map[string]any{}},
		{
			name: "single section",
			opts: map[string]any{"alpha": map[string]any{"label": "a"}},
			want: []string{"alpha=a"},
		},
		{
			name: "nil section uses zero options",
			opts: map[string]any{"alpha": nil},
			want: []string{"alpha="},
		},
		{
			name: "applied in name order",
			opts: map[string]any{
				"gamma": map[string]any{"label": "g"},
				"alpha": map[string]any{"label": "a"},
				"beta":  map[string]any{"label": "b"},
			},
			want: []string{"gamma=g", "beta=b", "alpha=a"},
		},
		{
			name: "typed options",
			opts: map[string]any{
				"alpha": tagOptions{Label: "value"},
				"beta":  &tagOptions{Label: "pointer"},
			},
			want: []string{"beta=pointer", "alpha=value"},
		},
		{
			name:      "unknown section",
			opts:      map[string]any{"counting": nil, "missing": true},
			wantError: "cannot find a ufs file system decorator",
		},
		{
			name:      "section name with the wrong case",
			opts:      map[string]any{"counting": nil, "Alpha": nil},
			wantError: "cannot find a ufs file system decorator",
		},
		{
			name:      "options of the wrong shape",
			opts:      map[string]any{"alpha": "not a mapping"},
			wantError: `invalid options for file system decorator "alpha"`,
		},
		{
			name:      "decorator fails",
			opts:      map[string]any{"alpha": map[string]any{"fail": true}},
			wantError: errTagDecorator.Error(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			base := makeNullFS(nullFSPrefix)
			got, err := r.decorate(t.Context(), base, tc.opts)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("decorate() = %v, want substring %q", err, tc.wantError)
				}
				if got != nil {
					t.Errorf("decorate() = %v, want nil on error", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("decorate() = %v, want nil", err)
			}
			if gotTags := tags(got); !reflect.DeepEqual(gotTags, tc.want) {
				t.Errorf("decorate() applied %v, want %v", gotTags, tc.want)
			}
			if len(tc.want) == 0 && got != WriteFS(base) {
				t.Errorf("decorate() = %v, want the undecorated base", got)
			}
		})
	}
	// An unmatched section must fail before any decorator is created.
	if got := created.Load(); got != 0 {
		t.Errorf("decorators created alongside an unknown section = %d, want 0", got)
	}
}

type failingMarshaler struct{}

var errMarshal = errors.New("marshal failed")

func (failingMarshaler) MarshalYAML() (any, error) {
	return nil, errMarshal
}

func TestDecodeOptions(t *testing.T) {
	t.Parallel()
	want := tagOptions{Label: "x", Timeout: 1500 * time.Millisecond, Fail: true}
	tests := []struct {
		name string
		raw  any
		want tagOptions
	}{
		{"nil", nil, tagOptions{}},
		{"value", want, want},
		{"pointer", &want, want},
		{"nil pointer", (*tagOptions)(nil), tagOptions{}},
		{"yaml mapping", map[string]any{"label": "x", "timeout": "1.5s", "fail": true}, want},
		{"string mapping", map[string]string{"label": "x"}, tagOptions{Label: "x"}},
		{"unknown fields are ignored", map[string]any{"label": "x", "extra": 1}, tagOptions{Label: "x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := DecodeOptions[tagOptions](tc.raw)
			if err != nil {
				t.Fatalf("DecodeOptions(%#v) = %v, want nil", tc.raw, err)
			}
			if got != tc.want {
				t.Errorf("DecodeOptions(%#v) = %+v, want %+v", tc.raw, got, tc.want)
			}
		})
	}

	t.Run("scalar options", func(t *testing.T) {
		t.Parallel()
		got, err := DecodeOptions[bool](true)
		if err != nil || !got {
			t.Errorf("DecodeOptions[bool](true) = %v, %v, want true, nil", got, err)
		}
	})

	errTests := []struct {
		name      string
		raw       any
		wantError string
	}{
		{"wrong shape", true, "cannot decode"},
		{"wrong field type", map[string]any{"timeout": "soon"}, "cannot decode"},
		{"marshal error", failingMarshaler{}, errMarshal.Error()},
		{"unencodable value", make(chan int), "cannot encode"},
	}
	for _, tc := range errTests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := DecodeOptions[tagOptions](tc.raw)
			if err == nil || !strings.Contains(err.Error(), tc.wantError) {
				t.Errorf("DecodeOptions(%#v) = %v, want substring %q", tc.raw, err, tc.wantError)
			}
		})
	}
}

func TestRegisterDecorator(t *testing.T) {
	name := fmt.Sprintf("registerTestDecorator%d", registerTestCounter.Add(1))
	decorator := tagDecorator(name)
	RegisterDecorator(decorator)

	fsys, err := New(t.Context(), "- source: \"memory://\"\n  options:\n    "+name+":\n      label: global")
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	nFS, ok := fsys.(*nestFS)
	if !ok {
		t.Fatalf("New() = %T, want *nestFS", fsys)
	}
	if got, want := tags(nFS.fsys), []string{name + "=global"}; !reflect.DeepEqual(got, want) {
		t.Errorf("New() applied %v, want %v", got, want)
	}
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}

	defer func() {
		if recover() == nil {
			t.Error("RegisterDecorator() of a duplicate did not panic")
		}
	}()
	RegisterDecorator(decorator)
}

// closeTrackFS reports whether the file system created for a mount was closed.
type closeTrackFS struct {
	WriteFS
	closed *bool
}

func (fsys *closeTrackFS) Close() error {
	*fsys.closed = true
	return fsys.WriteFS.Close()
}

func TestNewFromMountSpecClosesOnDecoratorError(t *testing.T) {
	scheme := uniqueDriverName("closetrack")
	var created int
	var closed [2]bool
	Register(Driver{
		Name:      scheme,
		MatchFunc: func(name string) bool { return strings.HasPrefix(name, scheme+":") },
		CreateFunc: func(context.Context, string) (WriteFS, error) {
			fsys := &closeTrackFS{WriteFS: makeNullFS(nullFSPrefix), closed: &closed[created]}
			created++
			return fsys, nil
		},
	})

	_, err := New(t.Context(), "- source: \""+scheme+":root\"\n- source: \""+scheme+":mount\"\n  mountPoint: \"data\"\n  options:\n    noSuchDecorator: true")
	if err == nil || !strings.Contains(err.Error(), "cannot find a ufs file system decorator") {
		t.Fatalf("New() = %v, want an unknown decorator error", err)
	}
	if created != 2 {
		t.Fatalf("created %d file systems, want 2", created)
	}
	if !closed[0] {
		t.Error("root file system was not closed")
	}
	if !closed[1] {
		t.Error("mount file system was not closed")
	}
}
