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
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/osutil"
	pb "github.com/cloudfra/ufs/proto"
)

func newFileLog(t *testing.T, dir string, src ufs.ReadFS, segmentSize int64) *FileLog {
	t.Helper()
	l, err := NewFileLog(dir, src, segmentSize)
	if err != nil {
		t.Fatalf("NewFileLog() = %v", err)
	}
	t.Cleanup(func() {
		if err := l.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return l
}

func segments(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestFileLogAppendSnapshotCommit(t *testing.T) {
	src := newMem(t, "memory://src")
	writeFile(t, src, "a", "first")
	dir := filepath.Join(t.TempDir(), "log")
	l := newFileLog(t, dir, src, 0)

	mustAppend(t, l, putEntry("a", 5), mkdirEntry("d"), removeEntry("z"))
	// The payload was read at append time, so a later change to the source
	// doesn't alter what the log replays.
	writeFile(t, src, "a", "second")
	if !l.Pending("a") || l.Bytes() != 5 {
		t.Errorf("Pending(a), Bytes() = %v, %d; want true, 5", l.Pending("a"), l.Bytes())
	}

	s := mustSnapshot(t, l)
	want := []string{"remove_all z", "mkdir d", "put a=first"}
	if got := describe(s.Entries); !slices.Equal(got, want) {
		t.Errorf("Snapshot() = %q, want %q", got, want)
	}

	// Appended after the snapshot: lands in a new segment and survives Commit.
	mustAppend(t, l, putEntry("a", 6))
	if got := segments(t, dir); !slices.Equal(got, []string{"0000000000.wlog", "0000000001.wlog"}) {
		t.Errorf("segments = %q, want two", got)
	}
	if err := l.Commit(s); err != nil {
		t.Fatalf("Commit() = %v", err)
	}
	if got := segments(t, dir); !slices.Equal(got, []string{"0000000001.wlog"}) {
		t.Errorf("segments after Commit = %q, want only the newer one", got)
	}
	if !l.Pending("a") || l.Bytes() != 6 {
		t.Errorf("Pending(a), Bytes() after Commit = %v, %d; want true, 6", l.Pending("a"), l.Bytes())
	}
	if got, want := describe(mustSnapshot(t, l).Entries), []string{"put a=second"}; !slices.Equal(got, want) {
		t.Errorf("Snapshot() after Commit = %q, want %q", got, want)
	}
	if err := l.Commit(nil); err != nil {
		t.Errorf("Commit(nil) = %v", err)
	}
}

func TestFileLogRotation(t *testing.T) {
	src := newMem(t, "memory://src")
	writeFile(t, src, "f", "0123456789")
	dir := t.TempDir()
	l := newFileLog(t, dir, src, 16)
	for range 3 {
		mustAppend(t, l, putEntry("f", 10))
	}
	if got := len(segments(t, dir)); got != 3 {
		t.Errorf("segments = %d, want one per entry larger than the segment size", got)
	}
	if got, want := describe(mustSnapshot(t, l).Entries), []string{"put f=0123456789"}; !slices.Equal(got, want) {
		t.Errorf("Snapshot() = %q, want %q", got, want)
	}
}

func TestFileLogReopen(t *testing.T) {
	src := newMem(t, "memory://src")
	writeFile(t, src, "a", "a")
	dir := t.TempDir()
	l, err := NewFileLog(dir, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, l, putEntry("a", 1), removeEntry("b"))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	if err := l.Close(); err != nil {
		t.Errorf("second Close() = %v", err)
	}
	if err := l.Append(putEntry("a", 1)); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Append() after Close = %v, want fs.ErrClosed", err)
	}
	if _, err := l.Snapshot(); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("Snapshot() after Close = %v, want fs.ErrClosed", err)
	}

	reopened := newFileLog(t, dir, src, 0)
	if !reopened.Pending("a") || reopened.Bytes() != 1 {
		t.Errorf("reopened Pending(a), Bytes() = %v, %d; want true, 1", reopened.Pending("a"), reopened.Bytes())
	}
	mustAppend(t, reopened, mkdirEntry("c"))
	if got := segments(t, dir); !slices.Equal(got, []string{"0000000000.wlog", "0000000001.wlog"}) {
		t.Errorf("segments = %q, want appends in a new segment", got)
	}
	want := []string{"remove_all b", "mkdir c", "put a=a"}
	if got := describe(mustSnapshot(t, reopened).Entries); !slices.Equal(got, want) {
		t.Errorf("reopened Snapshot() = %q, want %q", got, want)
	}
}

func TestFileLogTruncatesTornRecord(t *testing.T) {
	src := newMem(t, "memory://src")
	writeFile(t, src, "a", "a")
	writeFile(t, src, "b", "b")
	dir := t.TempDir()
	l, err := NewFileLog(dir, src, 0)
	if err != nil {
		t.Fatal(err)
	}
	mustAppend(t, l, putEntry("a", 1), putEntry("b", 1))
	if err := l.Close(); err != nil {
		t.Fatal(err)
	}
	seg := filepath.Join(dir, "0000000000.wlog")
	info, err := os.Stat(seg)
	if err != nil {
		t.Fatal(err)
	}
	// Cut the last record short, as a crash mid-append would.
	if err := os.Truncate(seg, info.Size()-2); err != nil {
		t.Fatal(err)
	}

	reopened := newFileLog(t, dir, src, 0)
	if got, want := describe(mustSnapshot(t, reopened).Entries), []string{"put a=a"}; !slices.Equal(got, want) {
		t.Errorf("Snapshot() = %q, want %q", got, want)
	}
	after, err := os.Stat(seg)
	if err != nil {
		t.Fatal(err)
	}
	if after.Size() >= info.Size()-2 {
		t.Errorf("segment size = %d, want it truncated to the last whole record", after.Size())
	}
}

func TestFileLogCorruptEarlierSegment(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"0000000000.wlog", "0000000001.wlog"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte{0x05, 0x01}, osutil.DefaultFilePermissions); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := NewFileLog(dir, newMem(t, "memory://src"), 0); err == nil {
		t.Error("NewFileLog() = nil, want an error for a corrupt segment that isn't the last")
	}
}

func TestFileLogIgnoresOtherFiles(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"notes.txt", "x.wlog"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("junk"), osutil.DefaultFilePermissions); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir(filepath.Join(dir, "0000000009.wlog"), osutil.DefaultDirectoryPermissions); err != nil {
		t.Fatal(err)
	}
	l := newFileLog(t, dir, newMem(t, "memory://src"), 0)
	if got := mustSnapshot(t, l).Entries; len(got) != 0 {
		t.Errorf("Snapshot() = %v, want empty", got)
	}
}

func TestFileLogSkipsRemovedPayload(t *testing.T) {
	l := newFileLog(t, t.TempDir(), newMem(t, "memory://src"), 0)
	mustAppend(t, l, putEntry("gone", 1))
	if l.Pending("gone") {
		t.Error("Pending(gone) = true, want the put of a missing file skipped")
	}
}

func TestFileLogErrors(t *testing.T) {
	src := newMem(t, "memory://src")
	if _, err := NewFileLog(t.TempDir(), src, -1); err == nil {
		t.Error("NewFileLog(negative size) = nil, want an error")
	}
	file := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(file, nil, osutil.DefaultFilePermissions); err != nil {
		t.Fatal(err)
	}
	if _, err := NewFileLog(file, src, 0); err == nil {
		t.Error("NewFileLog(a file) = nil, want an error")
	}
	l := newFileLog(t, t.TempDir(), src, 0)
	if err := l.Append(&pb.WriteLogEntry{Name: "a"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Append(unspecified op) = %v, want fs.ErrInvalid", err)
	}
	boom := errors.New("boom")
	failing := newFileLog(t, t.TempDir(), failingReadFS{FS: src, err: boom}, 0)
	if err := failing.Append(putEntry("a", 1)); !errors.Is(err, boom) {
		t.Errorf("Append() with a failing source = %v, want %v", err, boom)
	}
}
