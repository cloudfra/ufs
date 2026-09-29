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

//go:build !wasm

package boltfs

import (
	"context"
	"errors"
	"io/fs"
	"testing"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/testing/eventtest"
	pb "github.com/cloudfra/ufs/proto"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// batchWriter matches writelog.BatchWriter without importing it.
type batchWriter interface {
	BatchWrite(entries []*pb.WriteLogEntry) error
}

var _ batchWriter = (*boltFS)(nil)

func put(name, content string, modTime time.Time) *pb.WriteLogEntry {
	return &pb.WriteLogEntry{
		Op:      pb.WriteLogEntry_OP_PUT,
		Name:    name,
		Mode:    uint32(fs.ModePerm),
		ModTime: timestamppb.New(modTime),
		Size:    int64(len(content)),
		Content: []byte(content),
	}
}

func mkdir(name string, modTime time.Time) *pb.WriteLogEntry {
	return &pb.WriteLogEntry{
		Op:      pb.WriteLogEntry_OP_MKDIR,
		Name:    name,
		Mode:    uint32(fs.ModeDir | 0o750),
		ModTime: timestamppb.New(modTime),
	}
}

func removeAll(name string) *pb.WriteLogEntry {
	return &pb.WriteLogEntry{Op: pb.WriteLogEntry_OP_REMOVE_ALL, Name: name}
}

func batchWrite(t *testing.T, fsys ufs.FS, entries ...*pb.WriteLogEntry) error {
	t.Helper()
	return fsys.(batchWriter).BatchWrite(entries)
}

func assertFile(t *testing.T, fsys ufs.FS, name, content string, modTime time.Time) {
	t.Helper()
	got, err := fsys.ReadFile(name)
	if err != nil || string(got) != content {
		t.Errorf("ReadFile(%q) = (%q, %v), want (%q, nil)", name, got, err, content)
	}
	info, err := fsys.Stat(name)
	if err != nil {
		t.Fatalf("Stat(%q) = %v", name, err)
	}
	if !info.ModTime().Equal(modTime) {
		t.Errorf("Stat(%q).ModTime() = %v, want %v", name, info.ModTime(), modTime)
	}
}

func TestBatchWrite(t *testing.T) {
	fsys := newTestBoltFS(t)
	old := time.Date(2020, 1, 2, 3, 4, 5, 6, time.UTC)
	newer := old.Add(time.Hour)

	if err := batchWrite(t, fsys,
		put("keep", "keep", old),
		put("d/sub/f", "nested", old),
		mkdir("empty/dir", newer),
	); err != nil {
		t.Fatalf("BatchWrite() = %v", err)
	}
	assertFile(t, fsys, "keep", "keep", old)
	assertFile(t, fsys, "d/sub/f", "nested", old)
	info, err := fsys.Stat("empty/dir")
	if err != nil || !info.IsDir() || !info.ModTime().Equal(newer) || info.Mode().Perm() != 0o750 {
		t.Errorf("Stat(empty/dir) = (%v, %v), want a 0750 directory modified at %v", info, err, newer)
	}

	// Entries apply in order: the remove runs before the put that recreates
	// the directory, and a later put replaces an earlier one.
	if err := batchWrite(t, fsys,
		removeAll("d"),
		put("d/new", "first", newer),
		put("d/new", "second", newer),
		removeAll("missing/path"),
	); err != nil {
		t.Fatalf("BatchWrite() = %v", err)
	}
	if _, err := fsys.Stat("d/sub"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(d/sub) = %v, want fs.ErrNotExist", err)
	}
	assertFile(t, fsys, "d/new", "second", newer)
	assertFile(t, fsys, "keep", "keep", old)

	if err := batchWrite(t, fsys); err != nil {
		t.Errorf("BatchWrite() with no entries = %v", err)
	}
}

func TestBatchWriteRemoveAllRoot(t *testing.T) {
	fsys := newTestBoltFS(t)
	now := time.Now().UTC()
	if err := batchWrite(t, fsys, put("a", "", now), put("b/c", "", now)); err != nil {
		t.Fatal(err)
	}
	if err := batchWrite(t, fsys, removeAll("."), mkdir(".", now), put("z", "z", now)); err != nil {
		t.Fatalf("BatchWrite() = %v", err)
	}
	entries, err := fsys.ReadDir(".")
	if err != nil || len(entries) != 1 || entries[0].Name() != "z" {
		t.Errorf("ReadDir(.) = (%v, %v), want [z]", entries, err)
	}
}

func TestBatchWriteRollsBack(t *testing.T) {
	fsys := newTestBoltFS(t)
	now := time.Now().UTC()
	if err := batchWrite(t, fsys, put("existing", "before", now), mkdir("dir", now)); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		bad  *pb.WriteLogEntry
	}{
		{"put over a directory", put("dir", "x", now)},
		{"put at the root", put(".", "x", now)},
		{"put under a file", put("existing/child", "x", now)},
		{"mkdir over a file", mkdir("existing", now)},
		{"unspecified op", &pb.WriteLogEntry{Name: "x"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := batchWrite(t, fsys, put("existing", "after", now), put("added", "x", now), tc.bad)
			var pe *fs.PathError
			if !errors.As(err, &pe) || pe.Op != "batchwrite" || pe.Path != tc.bad.GetName() {
				t.Fatalf("BatchWrite() = %v, want a batchwrite *fs.PathError for %q", err, tc.bad.GetName())
			}
			assertFile(t, fsys, "existing", "before", now)
			if _, err := fsys.Stat("added"); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Stat(added) = %v, want the whole batch rolled back", err)
			}
		})
	}
}

func TestBatchWriteInvalidPath(t *testing.T) {
	fsys := newTestBoltFS(t)
	err := batchWrite(t, fsys, put("ok", "", time.Now()), put("../escape", "", time.Now()))
	ufsTesting.AssertInvalidPathError(t, "../escape", err, "batchwrite")
	if _, err := fsys.Stat("ok"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(ok) = %v, want nothing applied", err)
	}
}

func TestBatchWriteMissingModTime(t *testing.T) {
	fsys := newTestBoltFS(t)
	before := time.Now()
	entry := put("f", "x", before)
	entry.ModTime = nil
	if err := batchWrite(t, fsys, entry); err != nil {
		t.Fatal(err)
	}
	info, err := fsys.Stat("f")
	if err != nil || info.ModTime().Before(before.Truncate(time.Second)) {
		t.Errorf("Stat(f) = (%v, %v), want a modification time of about now", info, err)
	}
}

func TestNewExposesBatchWrite(t *testing.T) {
	fsys, err := New(t.Context(), testBoltFSURI(t))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	if _, ok := fsys.(batchWriter); !ok {
		t.Errorf("New() = %T, want it to implement BatchWrite", fsys)
	}
}

func TestBatchWriteClosed(t *testing.T) {
	fsys, err := makeBoltFS(testBoltFSURI(t))
	if err != nil {
		t.Fatal(err)
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}
	if err := fsys.BatchWrite([]*pb.WriteLogEntry{put("f", "", time.Now())}); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("BatchWrite() after Close = %v, want fs.ErrClosed", err)
	}
}

func TestBatchWriteNotifiesAfterCommit(t *testing.T) {
	fsys := newTestBoltFS(t)
	now := time.Now().UTC()
	if err := batchWrite(t, fsys, put("existing", "", now), put("gone/f", "", now)); err != nil {
		t.Fatal(err)
	}

	ec := eventtest.NewEventCollector[ufs.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closer, err := fsys.(ufs.Watcher).Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	// A failed batch notifies nothing.
	if err := batchWrite(t, fsys, put("never", "", now), put(".", "", now)); err == nil {
		t.Fatal("BatchWrite() = nil, want an error")
	}

	if err := batchWrite(t, fsys,
		put("existing", "x", now),
		put("new", "x", now),
		mkdir("dir/sub", now),
		removeAll("gone"),
	); err != nil {
		t.Fatal(err)
	}
	want := []eventtest.Event[ufs.NotifyOp]{
		{Op: ufs.NotifyWrite, Path: "existing"},
		{Op: ufs.NotifyCreate, Path: "new"},
		{Op: ufs.NotifyCreate, Path: "dir"},
		{Op: ufs.NotifyCreate, Path: "dir/sub"},
		{Op: ufs.NotifyRemove, Path: "gone/f"},
		{Op: ufs.NotifyRemove, Path: "gone"},
	}
	for _, w := range want {
		ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[ufs.NotifyOp]) bool { return ev == w })
	}
	if ec.HasEvent(func(ev eventtest.Event[ufs.NotifyOp]) bool { return ev.Path == "never" }) {
		t.Error("got an event for an entry of a failed batch")
	}
}
