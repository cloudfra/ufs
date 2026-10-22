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

// Package overlay provides [FS], a union of N [types.WriteFS] layers. Reads are
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
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/ops"
)

var (
	_ ufs.FS         = (*FS)(nil)
	_ fs.ReadDirFile = (*dirFile)(nil)
)

// FS is a union of at least two layers, top first. Layer 0 receives every
// write and is always visible: a lookup in it never consults the tombstones.
// A path in a lower layer is visible unless a higher layer has it or a
// tombstone hides it.
//
// FS keeps the merged view consistent for writes made through it: Create and
// MkdirAll reject a path that the merged view already has as the other kind
// (a file where a directory is wanted or the reverse). Lower layers must only
// be modified by the owner, and only in ways that keep them consistent with
// the view, such as applying a removal and then clearing its tombstone.
type FS struct {
	layers []types.WriteFS
	closed atomic.Bool

	mu         sync.RWMutex
	tombstones map[string]uint64
	seq        uint64
}

// New returns an overlay of layers, top first. layers[0] receives all writes.
// The overlay owns the layers: Close closes them. An overlay of fewer than two
// layers has nothing to merge, so New fails for it; use the file system
// itself.
func New(layers ...types.WriteFS) (*FS, error) {
	if len(layers) < 2 {
		return nil, fmt.Errorf("overlay: at least two layers are required, got %d", len(layers))
	}
	return &FS{
		layers:     slices.Clone(layers),
		tombstones: map[string]uint64{},
	}, nil
}

// Layer returns layer i, where 0 is the top.
func (o *FS) Layer(i int) types.WriteFS {
	return o.layers[i]
}

// Tombstones returns a snapshot of the tombstones and their sequence numbers.
func (o *FS) Tombstones() map[string]uint64 {
	o.mu.RLock()
	if len(o.tombstones) == 0 {
		o.mu.RUnlock()
		return nil
	}
	snapshot := make(map[string]uint64, len(o.tombstones))
	for name, seq := range o.tombstones {
		snapshot[name] = seq
	}
	o.mu.RUnlock()
	return snapshot
}

// ClearTombstones removes each tombstone whose sequence number still matches
// snapshot. A tombstone recorded again after the snapshot is kept.
func (o *FS) ClearTombstones(snapshot map[string]uint64) {
	o.mu.Lock()
	for name, seq := range snapshot {
		if o.tombstones[name] == seq {
			delete(o.tombstones, name)
		}
	}
	o.mu.Unlock()
}

// Hidden reports whether name, or an ancestor of it, has a tombstone.
func (o *FS) Hidden(name string) bool {
	o.mu.RLock()
	hidden := o.hiddenLocked(name)
	o.mu.RUnlock()
	return hidden
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
	o.seq++
	seq := o.seq
	o.tombstones[name] = seq
	o.mu.Unlock()
	return seq
}

// dropTombstone removes the tombstone on name if it still has sequence seq.
func (o *FS) dropTombstone(name string, seq uint64) {
	o.ClearTombstones(map[string]uint64{name: seq})
}

// check returns the error for op on name when name is invalid or the overlay
// is closed.
func (o *FS) check(op, name string) error {
	if err := pathutil.Validate(op, name); err != nil {
		return err
	}
	if o.closed.Load() {
		return ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	return nil
}

// findFirst calls fn on the layers, top first, until fn returns an error other
// than fs.ErrNotExist, and returns that result: the first layer that has name
// answers for it. Layer 0 is asked without looking at the tombstones; they
// are consulted only once layer 0 does not have name, and a tombstone stops
// the search before the lower layers. If no layer has name, findFirst returns
// layer 0's error.
func findFirst[T any](o *FS, name string, fn func(l types.WriteFS) (T, error)) (T, error) {
	first, firstErr := fn(o.layers[0])
	if !errors.Is(firstErr, fs.ErrNotExist) || o.Hidden(name) {
		return first, firstErr
	}
	for _, l := range o.layers[1:] {
		v, err := fn(l)
		if !errors.Is(err, fs.ErrNotExist) {
			return v, err
		}
	}
	return first, firstErr
}

// stat returns the merged view's Stat of name.
func (o *FS) stat(name string) (fs.FileInfo, error) {
	return findFirst(o, name, func(l types.WriteFS) (fs.FileInfo, error) { return l.Stat(name) })
}

// Open opens name from the highest layer that has it. A directory that is in
// layer 0 only is returned as layer 0 opened it; any other directory is
// returned as a snapshot of its merged listing.
func (o *FS) Open(name string) (fs.File, error) {
	if err := o.check("open", name); err != nil {
		return nil, err
	}
	f, err := findFirst(o, name, func(l types.WriteFS) (fs.File, error) { return l.Open(name) })
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, ufserrors.Join(err, f.Close())
	}
	if !info.IsDir() {
		return f, nil
	}
	layers, _, err := o.dirLayers("open", name)
	if err != nil {
		return nil, ufserrors.Join(err, f.Close())
	}
	if len(layers) == 1 && layers[0] == 0 {
		// Nothing below layer 0 has this directory, so there is nothing to
		// merge and its own handle is the answer.
		return f, nil
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	entries, err := o.mergeDir("open", name, layers)
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
	return findFirst(o, name, func(l types.WriteFS) ([]byte, error) { return l.ReadFile(name) })
}

// ReadLink reads the link name from the highest layer that has it.
func (o *FS) ReadLink(name string) (string, error) {
	if err := o.check("readlink", name); err != nil {
		return "", err
	}
	return findFirst(o, name, func(l types.WriteFS) (string, error) { return l.ReadLink(name) })
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
	return findFirst(o, name, func(l types.WriteFS) (fs.FileInfo, error) { return l.Lstat(name) })
}

// ReadDir returns the merged listing of the directory name, sorted by name.
func (o *FS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := o.check("readdir", name); err != nil {
		return nil, err
	}
	return o.readDir("readdir", name)
}

// dirLayers returns the indexes of the layers, top first, that have name as a
// directory, and the FileInfo of the highest of them. The highest layer that
// has name decides whether it is a directory; a lower layer where name is
// missing or not a directory contributes nothing. Layers below the top are
// not consulted when name is hidden by a tombstone.
func (o *FS) dirLayers(op, name string) ([]int, fs.FileInfo, error) {
	var (
		layers []int
		info   fs.FileInfo
	)
	for i, l := range o.layers {
		if i == 1 && o.Hidden(name) {
			break
		}
		li, err := l.Stat(name)
		if errNotDir(err) {
			continue
		}
		if err != nil {
			return nil, nil, ufserrors.NewPathError(op, name, err)
		}
		if !li.IsDir() {
			if len(layers) == 0 {
				return nil, nil, ufserrors.NewPathError(op, name, fmt.Errorf("not a directory: %w", fs.ErrInvalid))
			}
			continue
		}
		if len(layers) == 0 {
			info = li
		}
		layers = append(layers, i)
	}
	if len(layers) == 0 {
		return nil, nil, ufserrors.NewPathError(op, name, fs.ErrNotExist)
	}
	return layers, info, nil
}

// readDir returns the merged, sorted listing of the directory name.
func (o *FS) readDir(op, name string) ([]fs.DirEntry, error) {
	layers, _, err := o.dirLayers(op, name)
	if err != nil {
		return nil, err
	}
	if len(layers) == 1 && layers[0] == 0 {
		// Only layer 0 has the directory: its listing is the answer, and it
		// is already sorted.
		entries, err := o.layers[0].ReadDir(name)
		if err != nil {
			return nil, ufserrors.NewPathError(op, name, err)
		}
		return entries, nil
	}
	return o.mergeDir(op, name, layers)
}

// mergeDir merges the listings of the layers, which dirLayers returned for
// name, with the highest layer winning. Entries of the layers below the top
// are skipped when a tombstone hides them.
func (o *FS) mergeDir(op, name string, layers []int) ([]fs.DirEntry, error) {
	merged := map[string]fs.DirEntry{}
	var entries []fs.DirEntry
	for _, i := range layers {
		layerEntries, err := o.layers[i].ReadDir(name)
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
func (o *FS) Create(name string) (types.File, error) {
	if err := o.check("create", name); err != nil {
		return nil, err
	}
	if info, err := o.stat(name); err == nil && info.IsDir() {
		return nil, ufserrors.NewPathError("create", name, fmt.Errorf("is a directory: %w", fs.ErrInvalid))
	}
	if err := o.checkAncestors(name); err != nil {
		return nil, ufserrors.NewPathError("create", name, err)
	}
	if dir := path.Dir(name); dir != pathutil.CwdPath {
		if err := o.layers[0].MkdirAll(dir, fs.ModePerm); err != nil {
			return ufserrors.ChangePathErrorOp[types.File](nil, err, "create")
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
	if name != pathutil.CwdPath {
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

// Rename moves oldPath to newPath in the merged view. When oldPath is only in
// layer 0, so no lower layer has a copy that would show through, it is
// renamed within layer 0 with [ops.Rename]. Otherwise its merged content is
// copied to newPath in layer 0 and oldPath is removed, which records a
// tombstone over the lower copies; that is not atomic.
func (o *FS) Rename(oldPath, newPath string) error {
	if err := pathutil.ValidateRename(oldPath, newPath); err != nil {
		return err
	}
	if o.onlyInTop(oldPath) {
		if _, err := o.Lstat(newPath); errors.Is(err, fs.ErrNotExist) {
			return ops.Rename(o.layers[0], oldPath, newPath)
		}
	}
	return ops.RenameByCopy(o, oldPath, o, newPath)
}

// onlyInTop reports whether name exists in layer 0 and in no lower layer
// where it is visible.
func (o *FS) onlyInTop(name string) bool {
	if _, err := o.layers[0].Lstat(name); err != nil {
		return false
	}
	if o.Hidden(name) {
		return true
	}
	for _, l := range o.layers[1:] {
		if _, err := l.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
			return false
		}
	}
	return true
}

// RemoveAll removes name and everything below it from layer 0 and records a
// tombstone that hides them in the lower layers.
func (o *FS) RemoveAll(name string) error {
	if err := o.check("removeall", name); err != nil {
		return err
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
func (o *FS) GetDeviceInfo() types.DeviceMap {
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
