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

// Package readonlyfs provides the "readOnly" file system decorator, which
// rejects every write to the file system it wraps. Importing the package
// registers the decorator.
package readonlyfs

import (
	"context"
	"io/fs"
	"net/url"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/fsutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"gopkg.in/yaml.v3"
)

var _ ufs.WriteFS = (*readOnlyFS)(nil)

const (
	optionName = "readOnly"
)

// Options configures the readOnly decorator. In a mount spec it is written
// either as a bare bool or as a mapping:
//
//	options:
//	  - readOnly: true
//
//	options:
//	  - readOnly:
//	      enabled: true
type Options struct {
	// Enabled makes the file system read-only.
	Enabled bool `yaml:"enabled"`
}

// UnmarshalYAML accepts both the bare bool and the mapping form of [Options].
func (o *Options) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind == yaml.ScalarNode {
		return node.Decode(&o.Enabled)
	}
	type plain Options
	return node.Decode((*plain)(o))
}

// MarshalYAML writes [Options] in its bare bool form.
func (o Options) MarshalYAML() (any, error) {
	return o.Enabled, nil
}

func init() {
	ufs.RegisterDecorator(ufs.NewDecorator(optionName, wrap))
}

func wrap(_ context.Context, inner ufs.WriteFS, opts Options) (ufs.WriteFS, error) {
	if !opts.Enabled {
		return inner, nil
	}
	return New(inner), nil
}

// readOnlyFS wraps a [ReadFS] and satisfies [FS] by returning
// [fs.ErrPermission] for all write operations.
type readOnlyFS struct {
	ufs.ReadFS
}

// IsMountedArchiveDir forwards to the wrapped file system, see
// [ufs.MountedArchiveDirFS].
func (fsys *readOnlyFS) IsMountedArchiveDir(name string) bool {
	m, ok := fsys.ReadFS.(ufs.MountedArchiveDirFS)
	return ok && m.IsMountedArchiveDir(name)
}

// New wraps inner as an [WriteFS] whose write operations (Create, MkdirAll,
// Remove, RemoveAll) always return [fs.ErrPermission]. All read operations
// delegate to inner unchanged.
func New(inner ufs.ReadFS) ufs.WriteFS {
	return &readOnlyFS{
		ReadFS: inner,
	}
}

func (fsys *readOnlyFS) URI() (*url.URL, error) {
	u, err := fsys.ReadFS.URI()
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, nil
	}
	v := u.Query()
	v.Set("ro", "true")
	u.RawQuery = v.Encode()
	return ufs.AppendURIOption(u, optionName, Options{Enabled: true})
}

func (fsys *readOnlyFS) String() string {
	return fsutil.String(fsys.ReadFS)
}

func (fsys *readOnlyFS) Create(name string) (ufs.File, error) {
	if err := pathutil.Validate("create", name); err != nil {
		return nil, err
	}
	return nil, ufserrors.NewPathError("create", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := pathutil.Validate("mkdir", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("mkdir", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) Remove(name string) error {
	if err := pathutil.Validate("remove", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("remove", name, fs.ErrPermission)
}

func (fsys *readOnlyFS) RemoveAll(name string) error {
	if err := pathutil.Validate("removeall", name); err != nil {
		return err
	}
	return ufserrors.NewPathError("removeall", name, fs.ErrPermission)
}
