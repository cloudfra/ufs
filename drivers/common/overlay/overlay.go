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

// Package overlay provides [FS], a union of N [ufs.FS] layers. Reads are
// served by the highest layer that has a path, writes go to the top layer,
// and removals are recorded as in-memory tombstones that hide the removed
// path in every lower layer until the owner applies the removal there.
package overlay

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"slices"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ ufs.FS         = (*FS)(nil)
	_ fs.ReadDirFile = (*dirFile)(nil)
)

// FS is a union of layers, top first. Layer 0 receives every write and is
// always visible. A path in a lower layer is visible unless a higher layer
// has it or a tombstone hides it.
//
// FS keeps the merged view consistent for writes made through it: Create and
// MkdirAll reject a path that the merged view already has as the other kind
// (a file where a directory is wanted or the reverse). Lower layers must only
// be modified by the owner, and only in ways that keep them consistent with
// the view, such as applying a removal and then clearing its tombstone.
type FS struct {
	layers []ufs.FS
	closed atomic.Bool

	mu         sync.RWMutex
	tombstones map[string]uint64
	seq        uint64
}

// New returns an overlay of layers, top first. layers[0] receives all writes.
// The overlay owns the layers: Close closes them.
func New(layers ...ufs.FS) (*FS, error) {
	if len(layers) == 0 {
		return nil, errors.New("overlay: at least one layer is required")
	}
	return &FS{
		layers:     slices.Clone(layers),
		tombstones: map[string]uint64{},
	}, nil
}

// Layer returns layer i, where 0 is the top.
func (o *FS) Layer(i int) ufs.FS {
	return o.layers[i]
}

// Tombstones returns a snapshot of the tombstones and their sequence numbers.
func (o *FS) Tombstones() map[string]uint64 {
	o.mu.RLock()
	defer o.mu.RUnlock()
	if len(o.tombstones) == 0 {
		return nil
	}
	snapshot := make(map[string]uint64, len(o.tombstones))
	for name, seq := range o.tombstones {
		snapshot[name] = seq
	}
	return snapshot
}

// ClearTombstones removes each tombstone whose sequence number still matches
// snapshot. A tombstone recorded again after the snapshot is kept.
func (o *FS) ClearTombstones(snapshot map[string]uint64) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for name, seq := range snapshot {
		if o.tombstones[name] == seq {
			delete(o.tombstones, name)
		}
	}
}

// Hidden reports whether name, or an ancestor of it, has a tombstone.
func (o *FS) Hidden(name string) bool {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return o.hiddenLocked(name)
}

func (o *FS) hiddenLocked(name string) bool {
	if len(o.tombstones) == 0 {
		return false
	}
	for p := name; ; p = path.Dir(p) {
		if _, ok := o.tombstones[p]; ok {
			return true
		}
		if p == pathutil.CwdPath {
			return false
		}
	}
}

// addTombstone records a tombstone on name and returns its sequence number.
func (o *FS) addTombstone(name string) uint64 {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.seq++
	o.tombstones[name] = o.seq
	return o.seq
}

// dropTombstone removes the tombstone on name if it still has sequence seq.
func (o *FS) dropTombstone(name string, seq uint64) {
	o.ClearTombstones(map[string]uint64{name: seq})
}

// visibleLayers returns how many layers, from the top, may show name: all of
// them, or only layer 0 when name is hidden.
func (o *FS) visibleLayers(name string) int {
	if len(o.layers) > 1 && o.Hidden(name) {
		return 1
	}
	return len(o.layers)
}

// check returns the error for op on name when the overlay is closed or name
// is invalid.
func (o *FS) check(op, name string) error {
	if o.closed.Load() {
		return ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	return pathutil.Validate(op, name)
}

// find calls fn on each visible layer, top first, until fn returns an error
// other than fs.ErrNotExist, and returns that result. If every layer returns
// fs.ErrNotExist, find returns the top layer's error.
func find[T any](o *FS, name string, fn func(l ufs.FS) (T, error)) (T, error) {
	var (
		first    T
		firstErr error
	)
	for i := range o.visibleLayers(name) {
		v, err := fn(o.layers[i])
		if !errors.Is(err, fs.ErrNotExist) {
			return v, err
		}
		if i == 0 {
			first, firstErr = v, err
		}
	}
	return first, firstErr
}

// stat returns the merged view's Stat of name.
func (o *FS) stat(name string) (fs.FileInfo, error) {
	return find(o, name, func(l ufs.FS) (fs.FileInfo, error) { return l.Stat(name) })
}

// Open opens name from the highest layer that has it. A directory is
// returned as a snapshot of its merged listing.
func (o *FS) Open(name string) (fs.File, error) {
	if err := o.check("open", name); err != nil {
		return nil, err
	}
	f, err := find(o, name, func(l ufs.FS) (fs.File, error) { return l.Open(name) })
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, ufserrors.Join(err, f.Close())
	}
	if !info.IsDir() || len(o.layers) == 1 {
		return f, nil
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	entries, err := o.readDir("open", name)
	if err != nil {
		return nil, err
	}
	return &dirFile{name: name, info: info, entries: entries}, nil
}

// ReadFile reads name from the highest layer that has it.
func (o *FS) ReadFile(name string) ([]byte, error) {
	if err := o.check("readfile", name); err != nil {
		return nil, err
	}
	return find(o, name, func(l ufs.FS) ([]byte, error) { return l.ReadFile(name) })
}

// ReadLink reads the link name from the highest layer that has it.
func (o *FS) ReadLink(name string) (string, error) {
	if err := o.check("readlink", name); err != nil {
		return "", err
	}
	return find(o, name, func(l ufs.FS) (string, error) { return l.ReadLink(name) })
}

// Stat returns name's FileInfo from the highest layer that has it.
func (o *FS) Stat(name string) (fs.FileInfo, error) {
	if err := o.check("stat", name); err != nil {
		return nil, err
	}
	return o.stat(name)
}

// Lstat returns name's FileInfo, without following a final link, from the
// highest layer that has it.
func (o *FS) Lstat(name string) (fs.FileInfo, error) {
	if err := o.check("lstat", name); err != nil {
		return nil, err
	}
	return find(o, name, func(l ufs.FS) (fs.FileInfo, error) { return l.Lstat(name) })
}

// ReadDir returns the merged listing of the directory name, sorted by name.
func (o *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := o.check("readdir", name); err != nil {
		return nil, err
	}
	return o.readDir("readdir", name)
}

// readDir returns the merged, sorted listing of the directory name. The
// highest layer that has name decides whether it is a directory; it and every
// visible layer below it where name is also a directory contribute entries.
// Entries hidden by a tombstone are skipped in the lower layers.
func (o *FS) readDir(op, name string) ([]fs.DirEntry, error) {
	visible := o.visibleLayers(name)
	top := -1
	for i := range visible {
		info, err := o.layers[i].Stat(name)
		if errors.Is(err, fs.ErrNotExist) {
			continue
		}
		if err != nil {
			return nil, ufserrors.NewPathError(op, name, err)
		}
		if !info.IsDir() {
			return nil, ufserrors.NewPathError(op, name, fmt.Errorf("not a directory: %w", fs.ErrInvalid))
		}
		top = i
		break
	}
	if top < 0 {
		return nil, ufserrors.NewPathError(op, name, fs.ErrNotExist)
	}

	merged := map[string]fs.DirEntry{}
	var entries []fs.DirEntry
	for i := top; i < visible; i++ {
		layerEntries, err := o.layers[i].ReadDir(name)
		if i > top && errNotDir(err) {
			continue
		}
		if err != nil {
			return nil, ufserrors.NewPathError(op, name, err)
		}
		for _, e := range layerEntries {
			if _, ok := merged[e.Name()]; ok {
				continue
			}
			if i > 0 && o.Hidden(childPath(name, e.Name())) {
				continue
			}
			merged[e.Name()] = e
			entries = append(entries, e)
		}
	}
	slices.SortFunc(entries, func(a, b fs.DirEntry) int { return strings.Compare(a.Name(), b.Name()) })
	return entries, nil
}

// errNotDir reports whether a lower layer's ReadDir error means the path is
// missing or not a directory there, so the layer contributes nothing.
func errNotDir(err error) bool {
	return errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrInvalid)
}

// childPath returns the path of the entry child in directory dir.
func childPath(dir, child string) string {
	if dir == pathutil.CwdPath {
		return child
	}
	return dir + pathutil.UnixSeparator + child
}

// checkAncestors returns an error wrapping fs.ErrExist if any ancestor of
// name (excluding the root) is a file in the merged view.
func (o *FS) checkAncestors(name string) error {
	for dir := path.Dir(name); dir != pathutil.CwdPath; dir = path.Dir(dir) {
		info, err := o.stat(dir)
		if err == nil && !info.IsDir() {
			return fmt.Errorf("%q is not a directory: %w", dir, fs.ErrExist)
		}
	}
	return nil
}

// Create creates or truncates name in layer 0, creating missing parent
// directories there. It fails if the merged view has name as a directory or
// an ancestor of name as a file.
func (o *FS) Create(name string) (ufs.File, error) {
	if err := o.check("create", name); err != nil {
		return nil, err
	}
	if len(o.layers) == 1 {
		return o.layers[0].Create(name)
	}
	if info, err := o.stat(name); err == nil && info.IsDir() {
		return nil, ufserrors.NewPathError("create", name, fmt.Errorf("is a directory: %w", fs.ErrInvalid))
	}
	if err := o.checkAncestors(name); err != nil {
		return nil, ufserrors.NewPathError("create", name, err)
	}
	if dir := path.Dir(name); dir != pathutil.CwdPath {
		if err := o.layers[0].MkdirAll(dir, fs.ModePerm); err != nil {
			return ufserrors.ChangePathErrorOp[ufs.File](nil, err, "create")
		}
	}
	return o.layers[0].Create(name)
}

// MkdirAll creates the directory name in layer 0. It fails if the merged view
// has name or an ancestor of it as a file.
func (o *FS) MkdirAll(name string, perm fs.FileMode) error {
	if err := o.check("mkdir", name); err != nil {
		return err
	}
	if len(o.layers) > 1 && name != pathutil.CwdPath {
		if info, err := o.stat(name); err == nil && !info.IsDir() {
			return ufserrors.NewPathError("mkdir", name, fmt.Errorf("%q is not a directory: %w", name, fs.ErrExist))
		}
		if err := o.checkAncestors(name); err != nil {
			return ufserrors.NewPathError("mkdir", name, err)
		}
	}
	return o.layers[0].MkdirAll(name, perm)
}

// Remove removes the file or empty directory name from layer 0 and records a
// tombstone that hides it in the lower layers. A directory is empty when its
// merged listing is.
func (o *FS) Remove(name string) error {
	if err := o.check("remove", name); err != nil {
		return err
	}
	if len(o.layers) == 1 {
		return o.layers[0].Remove(name)
	}
	if name == pathutil.CwdPath {
		return ufserrors.NewPathError("remove", name, fs.ErrPermission)
	}
	info, err := o.stat(name)
	if err != nil {
		_, err = ufserrors.ChangePathErrorOp[any](nil, err, "remove")
		return err
	}
	if info.IsDir() {
		entries, err := o.readDir("remove", name)
		if err != nil {
			return err
		}
		if len(entries) > 0 {
			return ufserrors.NewPathError("remove", name, ufserrors.ErrDirNotEmpty)
		}
	}
	// The tombstone goes in first so the lower layers' copy never shows
	// through once layer 0's copy is gone.
	seq := o.addTombstone(name)
	if err := o.layers[0].Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		o.dropTombstone(name, seq)
		return err
	}
	return nil
}

// RemoveAll removes name and everything below it from layer 0 and records a
// tombstone that hides them in the lower layers.
func (o *FS) RemoveAll(name string) error {
	if err := o.check("removeall", name); err != nil {
		return err
	}
	if len(o.layers) == 1 {
		return o.layers[0].RemoveAll(name)
	}
	seq := o.addTombstone(name)
	if err := o.layers[0].RemoveAll(name); err != nil {
		o.dropTombstone(name, seq)
		return err
	}
	return nil
}

// Close closes the layers, top first, and joins their errors. Later calls
// return nil.
func (o *FS) Close() error {
	if o.closed.Swap(true) {
		return nil
	}
	errs := make([]error, 0, len(o.layers))
	for _, l := range o.layers {
		errs = append(errs, l.Close())
	}
	return ufserrors.Join(errs...)
}

// GetDeviceInfo returns layer 0's devices.
func (o *FS) GetDeviceInfo() ufs.DeviceMap {
	return o.layers[0].GetDeviceInfo()
}

// URI returns layer 0's URI.
func (o *FS) URI() (*url.URL, error) {
	return o.layers[0].URI()
}

// String describes the overlay and its layers, top first.
func (o *FS) String() string {
	names := make([]string, len(o.layers))
	for i, l := range o.layers {
		names[i] = l.String()
	}
	return "overlay(" + strings.Join(names, ", ") + ")"
}

// dirFile is an open directory whose entries are the merged listing,
// snapshotted when it was opened.
type dirFile struct {
	name    string
	info    fs.FileInfo
	entries []fs.DirEntry
	offset  int
}

func (d *dirFile) Stat() (fs.FileInfo, error) {
	return d.info, nil
}

func (d *dirFile) Read([]byte) (int, error) {
	return 0, ufserrors.NewPathError("read", d.name, fmt.Errorf("is a directory: %w", fs.ErrInvalid))
}

func (d *dirFile) Close() error {
	return nil
}

func (d *dirFile) ReadDir(n int) ([]fs.DirEntry, error) {
	rest := d.entries[d.offset:]
	if n <= 0 {
		d.offset = len(d.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	n = min(n, len(rest))
	d.offset += n
	return rest[:n], nil
}
