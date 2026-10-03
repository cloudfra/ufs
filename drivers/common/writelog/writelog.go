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

// Package writelog records the mutations made to a [ufs.FS] so they can be
// replayed against another one.
//
// [FS] wraps any file system and records its successful writes into a [Log].
// [MemoryLog] keeps the pending entries in memory and reads their payloads
// from the source file system when a [Snapshot] is taken; [FileLog] appends
// entries with their payloads to segment files on disk. [Apply] replays a
// snapshot's entries against a file system, atomically when it implements
// [BatchWriter].
package writelog

import (
	"errors"
	"fmt"
	"io/fs"
	"path"
	"slices"
	"strings"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	pb "github.com/cloudfra/ufs/proto"
)

// Log records write log entries.
type Log interface {
	// Append records entry. entry.Content is empty; a log that needs the
	// payload reads it from the source file system it was created with.
	Append(entry *pb.WriteLogEntry) error
	// Snapshot returns the pending entries with payloads attached, ordered
	// for replay: removes, then mkdirs (parents first), then puts.
	Snapshot() (*Snapshot, error)
	// Commit removes the snapshot's entries that nothing newer has replaced.
	Commit(s *Snapshot) error
	// Pending reports whether name has an uncommitted OP_PUT entry.
	Pending(name string) bool
	// Bytes reports the total size of the pending OP_PUT entries.
	Bytes() int64
}

// BatchWriter is implemented by a file system that can apply entries
// atomically.
type BatchWriter interface {
	// BatchWrite applies entries in order, all or nothing.
	BatchWrite(entries []*pb.WriteLogEntry) error
}

// Snapshot is a set of pending entries taken from a [Log], ready to replay.
// Pass it back to the same log's Commit once it has been applied.
type Snapshot struct {
	// Entries are the entries in replay order, with payloads attached.
	Entries []*pb.WriteLogEntry

	// seqs maps each path the snapshot covers to its sequence number.
	seqs map[string]uint64
	// segments are the FileLog segment files the snapshot was read from.
	segments []string
}

// Apply writes entries to fsys: with BatchWrite when fsys is a
// [BatchWriter], otherwise one entry at a time (MkdirAll, Create+Write+Close,
// RemoveAll), which is not atomic. Removing a path that does not exist
// succeeds.
func Apply(fsys ufs.FS, entries []*pb.WriteLogEntry) error {
	if bw, ok := fsys.(BatchWriter); ok {
		return bw.BatchWrite(entries)
	}
	for _, e := range entries {
		if err := applyOne(fsys, e); err != nil {
			return err
		}
	}
	return nil
}

func applyOne(fsys ufs.FS, e *pb.WriteLogEntry) error {
	name := e.GetName()
	switch e.GetOp() {
	case pb.WriteLogEntry_OP_PUT:
		if dir := path.Dir(name); dir != pathutil.CwdPath {
			if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
				return err
			}
		}
		f, err := fsys.Create(name)
		if err != nil {
			return err
		}
		if _, err := f.Write(e.GetContent()); err != nil {
			return ufserrors.Join(err, f.Close())
		}
		return f.Close()
	case pb.WriteLogEntry_OP_MKDIR:
		return fsys.MkdirAll(name, dirPerm(e))
	case pb.WriteLogEntry_OP_REMOVE_ALL:
		return fsys.RemoveAll(name)
	default:
		return invalidOp(e)
	}
}

// dirPerm returns the permission bits of an OP_MKDIR entry, defaulting to
// fs.ModePerm.
func dirPerm(e *pb.WriteLogEntry) fs.FileMode {
	if perm := fs.FileMode(e.GetMode()).Perm(); perm != 0 {
		return perm
	}
	return fs.ModePerm
}

func invalidOp(e *pb.WriteLogEntry) error {
	return ufserrors.NewPathError("writelog", e.GetName(), fmt.Errorf("unsupported write log op %v: %w", e.GetOp(), fs.ErrInvalid))
}

// validate returns an error unless e has a known op and a valid name.
func validate(e *pb.WriteLogEntry) error {
	if err := pathutil.Validate("writelog", e.GetName()); err != nil {
		return err
	}
	switch e.GetOp() {
	case pb.WriteLogEntry_OP_PUT, pb.WriteLogEntry_OP_MKDIR, pb.WriteLogEntry_OP_REMOVE_ALL:
		return nil
	default:
		return invalidOp(e)
	}
}

// pathState is the pending state of one path.
type pathState struct {
	// seq is the sequence number of the latest change to the path.
	seq uint64
	// removed is set when a removal of the path (and its subtree) is pending.
	removed bool
	// entry is the pending OP_PUT or OP_MKDIR recorded after any removal.
	entry *pb.WriteLogEntry
}

// pendingSet dedupes entries as they arrive, keeping at most one removal and
// one later put or mkdir per path. After dedupe no pending put or mkdir is
// covered by a later removal, so replaying every removal first, then the
// mkdirs and then the puts is equivalent to replaying the entries in arrival
// order.
type pendingSet struct {
	paths map[string]*pathState
	seq   uint64
	bytes int64
}

func newPendingSet() pendingSet {
	return pendingSet{paths: map[string]*pathState{}}
}

// add records e, which must be valid.
func (p *pendingSet) add(e *pb.WriteLogEntry) {
	p.seq++
	name := e.GetName()
	switch e.GetOp() {
	case pb.WriteLogEntry_OP_REMOVE_ALL:
		for key, st := range p.paths {
			if covers(name, key) {
				p.setEntry(st, nil)
				delete(p.paths, key)
			}
		}
		p.paths[name] = &pathState{seq: p.seq, removed: true}
	case pb.WriteLogEntry_OP_MKDIR:
		st := p.state(name)
		st.seq = p.seq
		if st.entry == nil {
			st.entry = e
		}
	default:
		st := p.state(name)
		st.seq = p.seq
		p.setEntry(st, e)
	}
}

func (p *pendingSet) state(name string) *pathState {
	st, ok := p.paths[name]
	if !ok {
		st = &pathState{}
		p.paths[name] = st
	}
	return st
}

// setEntry replaces st's entry with e, keeping bytes up to date.
func (p *pendingSet) setEntry(st *pathState, e *pb.WriteLogEntry) {
	if isPut(st.entry) {
		p.bytes -= st.entry.GetSize()
	}
	st.entry = e
	if isPut(e) {
		p.bytes += e.GetSize()
	}
}

// commit removes each path whose sequence number still matches seqs.
func (p *pendingSet) commit(seqs map[string]uint64) {
	for name, seq := range seqs {
		if st, ok := p.paths[name]; ok && st.seq == seq {
			p.setEntry(st, nil)
			delete(p.paths, name)
		}
	}
}

// pending reports whether name has a pending put.
func (p *pendingSet) pending(name string) bool {
	st, ok := p.paths[name]
	return ok && isPut(st.entry)
}

// seqs returns every path's sequence number.
func (p *pendingSet) seqs() map[string]uint64 {
	seqs := make(map[string]uint64, len(p.paths))
	for name, st := range p.paths {
		seqs[name] = st.seq
	}
	return seqs
}

// ordered returns the pending entries in replay order: removals, mkdirs
// (sorted, so parents come first) and puts (sorted).
func (p *pendingSet) ordered() (removes, mkdirs, puts []*pb.WriteLogEntry) {
	for name, st := range p.paths {
		if st.removed {
			removes = append(removes, &pb.WriteLogEntry{Op: pb.WriteLogEntry_OP_REMOVE_ALL, Name: name})
		}
		switch {
		case st.entry == nil:
		case isPut(st.entry):
			puts = append(puts, st.entry)
		default:
			mkdirs = append(mkdirs, st.entry)
		}
	}
	for _, list := range [][]*pb.WriteLogEntry{removes, mkdirs, puts} {
		slices.SortFunc(list, func(a, b *pb.WriteLogEntry) int { return strings.Compare(a.GetName(), b.GetName()) })
	}
	return removes, mkdirs, puts
}

func isPut(e *pb.WriteLogEntry) bool {
	return e != nil && e.GetOp() == pb.WriteLogEntry_OP_PUT
}

// covers reports whether removing dir removes name: name is dir or below it.
func covers(dir, name string) bool {
	if dir == pathutil.CwdPath || dir == name {
		return true
	}
	return strings.HasPrefix(name, dir+pathutil.UnixSeparator)
}

// withoutContent returns a copy of e without its payload.
func withoutContent(e *pb.WriteLogEntry) *pb.WriteLogEntry {
	return &pb.WriteLogEntry{
		Op:      e.GetOp(),
		Name:    e.GetName(),
		Mode:    e.GetMode(),
		ModTime: e.GetModTime(),
		Size:    e.GetSize(),
	}
}

// errSkip reports whether err from reading a payload means the file is gone,
// so its put is skipped: a later removal is, or will be, recorded for it.
func errSkip(err error) bool {
	return errors.Is(err, fs.ErrNotExist)
}
