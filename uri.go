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
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"

	"github.com/cloudfra/ufs/internal/pathutil"
	"gopkg.in/yaml.v3"
)

var osRootType = reflect.TypeFor[*os.Root]()

// URIOrDefault returns fsys's canonical URI, or value if fsys reports no URI.
func URIOrDefault(fsys URIGet, value string) string {
	u, err := fsys.URI()
	if err != nil || u == nil {
		return value
	}
	return u.String()
}

// URIOf returns the URI of fsys on a best-effort basis. A file system that
// implements [URIGet] reports its own URI, including a nil URL when it has
// none. Otherwise fsys is checked against well-known standard library types:
// the results of [os.DirFS] and [os.Root.FS] yield a file URI of their
// directory. Any other file system has no URI and yields nil.
func URIOf(fsys fs.FS) (*url.URL, error) {
	if ug, ok := fsys.(URIGet); ok {
		return ug.URI()
	}
	if dir, ok := osFSDir(fsys); ok {
		abs, err := filepath.Abs(dir)
		if err != nil {
			return nil, fmt.Errorf("cannot resolve directory %q of %T, %w", dir, fsys, err)
		}
		return &url.URL{Scheme: "file", Path: pathutil.CoerceUnix(abs)}, nil
	}
	return nil, nil
}

// osFSDir returns the directory behind a file system created by [os.DirFS] or
// [os.Root.FS]. Package os does not export either type, so they are recognized
// by name: os.dirFS is a string holding the directory, and *os.rootFS converts
// to the [*os.Root] it was made from. If package os changes these types, fsys
// is simply not recognized.
func osFSDir(fsys fs.FS) (string, bool) {
	v := reflect.ValueOf(fsys)
	if !v.IsValid() {
		return "", false
	}
	t := v.Type()
	switch {
	case t.Kind() == reflect.String && t.PkgPath() == "os" && t.Name() == "dirFS":
		dir := v.String()
		return dir, dir != ""
	case t.Kind() == reflect.Pointer && t.Elem().PkgPath() == "os" && t.Elem().Name() == "rootFS" && t.ConvertibleTo(osRootType):
		root, ok := v.Convert(osRootType).Interface().(*os.Root)
		if !ok || root == nil {
			return "", false
		}
		return root.Name(), true
	}
	return "", false
}

// AppendURIOption returns a copy of u that also records a decorator named name
// with the given options, so that [New] applies the decorator again when it is
// given the result. Decorators call it from their URI method with the URI of
// the file system they wrap; calling it once per layer keeps the decorators in
// the order they were applied. A nil u, meaning the wrapped file system has no
// URI, yields nil.
func AppendURIOption(u *url.URL, name string, options any) (*url.URL, error) {
	if u == nil {
		return nil, nil
	}
	query := u.Query()
	opts, err := decodeURIOptions(query.Get(optionsQueryParam))
	if err != nil {
		return nil, err
	}
	encoded, err := encodeURIOptions(append(opts, MountOption{Name: name, Config: options}))
	if err != nil {
		return nil, fmt.Errorf("cannot encode options of file system decorator %q, %w", name, err)
	}
	query.Set(optionsQueryParam, encoded)
	out := *u
	out.RawQuery = query.Encode()
	return &out, nil
}

// splitURIOptions removes the options query parameter from name and returns
// the decorators it lists. A name without the parameter is returned unchanged.
func splitURIOptions(name string) (string, []MountOption, error) {
	if !strings.Contains(name, optionsQueryParam+"=") {
		return name, nil, nil
	}
	u, err := url.Parse(name)
	if err != nil {
		return name, nil, nil
	}
	query := u.Query()
	if !query.Has(optionsQueryParam) {
		return name, nil, nil
	}
	opts, err := decodeURIOptions(query.Get(optionsQueryParam))
	if err != nil {
		return name, nil, err
	}
	query.Del(optionsQueryParam)
	u.RawQuery = query.Encode()
	return u.String(), opts, nil
}

// encodeURIOptions renders opts as a single-line YAML flow sequence, e.g.
// [{readOnly: true}, {fault: {errorRate: 0.25}}].
func encodeURIOptions(opts []MountOption) (encoded string, err error) {
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

// decodeURIOptions parses the value written by encodeURIOptions.
func decodeURIOptions(raw string) ([]MountOption, error) {
	var opts []MountOption
	if err := yaml.Unmarshal([]byte(raw), &opts); err != nil {
		return nil, fmt.Errorf("invalid %s query parameter %q, %w", optionsQueryParam, raw, err)
	}
	return opts, nil
}
