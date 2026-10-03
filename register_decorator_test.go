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
	"reflect"
	"strings"
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

func tagDecorator(name string, priority int) Decorator {
	return NewDecorator(name, priority, func(_ context.Context, inner WriteFS, opts tagOptions) (WriteFS, error) {
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
			decorator: Decorator{Name: "test-decorator"},
			wantError: "empty CreateFunc",
		},
		{
			name:      "valid",
			decorator: Decorator{Name: "test-decorator", CreateFunc: nopDecorate},
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
	// Names are matched case-insensitively, so they must also collide that way.
	for _, name := range []string{"dupDecorator", "DUPDECORATOR"} {
		err := r.registerDecorator(Decorator{Name: name, CreateFunc: nopDecorate})
		if err == nil || !strings.Contains(err.Error(), "already registered") {
			t.Errorf("registerDecorator(%q) = %v, want substring %q", name, err, "already registered")
		}
	}
}

func TestRegistrarMatchDecorator(t *testing.T) {
	t.Parallel()
	r := newRegistrar()
	if err := r.registerDecorator(tagDecorator("camelCase", 0)); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"camelCase", "camelcase", "CAMELCASE"} {
		got, err := r.matchDecorator(name)
		if err != nil {
			t.Fatalf("matchDecorator(%q) = %v, want nil", name, err)
		}
		if got.Name != "camelCase" {
			t.Errorf("matchDecorator(%q) = %q, want %q", name, got.Name, "camelCase")
		}
	}
	_, err := r.matchDecorator("missing")
	if err == nil {
		t.Fatal("matchDecorator(missing) = nil error, want error")
	}
	for _, want := range []string{"cannot find a ufs file system decorator", `"missing"`, "is imported"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("matchDecorator(missing) = %q, want substring %q", err, want)
		}
	}
}

func TestRegistrarDecorate(t *testing.T) {
	t.Parallel()
	r := newRegistrar()
	for _, d := range []Decorator{
		tagDecorator("inner", 0),
		tagDecorator("outer", 10),
		tagDecorator("tieB", 5),
		tagDecorator("tieA", 5),
	} {
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
			opts: map[string]any{"inner": map[string]any{"label": "a"}},
			want: []string{"inner=a"},
		},
		{
			name: "nil section uses zero options",
			opts: map[string]any{"inner": nil},
			want: []string{"inner="},
		},
		{
			name: "applied in priority order, ties by name",
			opts: map[string]any{
				"outer": map[string]any{"label": "o"},
				"tieB":  map[string]any{"label": "b"},
				"inner": map[string]any{"label": "i"},
				"tieA":  map[string]any{"label": "a"},
			},
			want: []string{"outer=o", "tieB=b", "tieA=a", "inner=i"},
		},
		{
			name: "typed options",
			opts: map[string]any{
				"inner": tagOptions{Label: "value"},
				"outer": &tagOptions{Label: "pointer"},
			},
			want: []string{"outer=pointer", "inner=value"},
		},
		{
			name:      "unknown section",
			opts:      map[string]any{"inner": nil, "missing": true},
			wantError: "cannot find a ufs file system decorator",
		},
		{
			name:      "section configured twice",
			opts:      map[string]any{"inner": nil, "INNER": nil},
			wantError: "configured more than once",
		},
		{
			name:      "options of the wrong shape",
			opts:      map[string]any{"inner": "not a mapping"},
			wantError: `invalid options for file system decorator "inner"`,
		},
		{
			name:      "decorator fails",
			opts:      map[string]any{"inner": map[string]any{"fail": true}},
			wantError: errTagDecorator.Error(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
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
	name := uniqueDriverName("register-test-decorator")
	decorator := tagDecorator(name, 0)
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
