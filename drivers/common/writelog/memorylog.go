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

package writelog

import (
	"io"
	"sync"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/ufserrors"
	pb "github.com/cloudfra/ufs/proto"
)

var _ Log = (*MemoryLog)(nil)

// MemoryLog is a [Log] that holds deduped entries in memory. It never holds
// payloads: an OP_PUT entry refers to its file in the source file system, and
// Snapshot reads the payload from there, so pending data is not held twice.
// That copy is short-lived and bounded by the snapshot being replayed.
//
// A payload read by Snapshot may be newer than its entry. That is harmless:
// the newer write has its own entry, which Commit keeps.
type MemoryLog struct {
	src ufs.ReadFS

	mu  sync.Mutex
	set pendingSet
}

// NewMemoryLog returns an empty MemoryLog that reads payloads from src.
func NewMemoryLog(src ufs.ReadFS) *MemoryLog {
	return &MemoryLog{src: src, set: newPendingSet()}
}

// Append records entry. Its payload, if any, is ignored.
func (l *MemoryLog) Append(entry *pb.WriteLogEntry) error {
	if err := validate(entry); err != nil {
		return err
	}
	e := withoutContent(entry)
	l.mu.Lock()
	l.set.add(e)
	l.mu.Unlock()
	return nil
}

// Snapshot returns the pending entries in replay order, reading each OP_PUT
// payload from the source file system. A put whose file no longer exists is
// skipped; Commit still clears it unless it was replaced.
func (l *MemoryLog) Snapshot() (*Snapshot, error) {
	l.mu.Lock()
	removes, mkdirs, puts := l.set.ordered()
	seqs := l.set.seqs()
	l.mu.Unlock()

	entries := make([]*pb.WriteLogEntry, 0, len(removes)+len(mkdirs)+len(puts))
	entries = append(entries, removes...)
	entries = append(entries, mkdirs...)
	for _, e := range puts {
		withPayload, ok, err := readPayload(l.src, e.GetName())
		if err != nil {
			return nil, err
		}
		if ok {
			entries = append(entries, withPayload)
		}
	}
	return &Snapshot{Entries: entries, seqs: seqs}, nil
}

// Commit removes the snapshot's entries that nothing newer has replaced.
func (l *MemoryLog) Commit(s *Snapshot) error {
	if s == nil {
		return nil
	}
	l.mu.Lock()
	l.set.commit(s.seqs)
	l.mu.Unlock()
	return nil
}

// Pending reports whether name has an uncommitted OP_PUT entry.
func (l *MemoryLog) Pending(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.set.pending(name)
}

// Bytes reports the total size of the pending OP_PUT entries.
func (l *MemoryLog) Bytes() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.set.bytes
}

// readPayload returns an OP_PUT entry for name with its current content, mode
// and modification time read from src. ok is false when name no longer
// exists or is no longer a file.
func readPayload(src ufs.ReadFS, name string) (entry *pb.WriteLogEntry, ok bool, err error) {
	f, err := src.Open(name)
	if errSkip(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	info, err := f.Stat()
	if err != nil {
		return nil, false, ufserrors.Join(err, f.Close())
	}
	if info.IsDir() {
		return nil, false, f.Close()
	}
	content, err := io.ReadAll(f)
	if err = ufserrors.Join(err, f.Close()); err != nil {
		return nil, false, err
	}
	return &pb.WriteLogEntry{
		Op:      pb.WriteLogEntry_OP_PUT,
		Name:    name,
		Mode:    uint32(info.Mode()),
		ModTime: timestamppb.New(info.ModTime()),
		Size:    int64(len(content)),
		Content: content,
	}, true, nil
}
