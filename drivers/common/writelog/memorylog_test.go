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
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	pb "github.com/cloudfra/ufs/proto"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func newMem(t *testing.T, name string) ufs.FS {
	t.Helper()
	fsys, err := ufs.New(t.Context(), name)
	if err != nil {
		t.Fatalf("New(%q) = %v", name, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close(%s) = %v", fsys, err)
		}
	})
	return fsys
}

func writeFile(t *testing.T, fsys ufs.WriteFS, name, content string) {
	t.Helper()
	if i := strings.LastIndex(name, "/"); i >= 0 {
		if err := fsys.MkdirAll(name[:i], fs.ModePerm); err != nil {
			t.Fatalf("MkdirAll(%q) = %v", name[:i], err)
		}
	}
	ufsdriversTesting.WriteFile(t, fsys, name, content)
}

func putEntry(name string, size int64) *pb.WriteLogEntry {
	return &pb.WriteLogEntry{Op: pb.WriteLogEntry_OP_PUT, Name: name, Mode: uint32(fs.ModePerm), Size: size}
}

func mkdirEntry(name string) *pb.WriteLogEntry {
	return &pb.WriteLogEntry{Op: pb.WriteLogEntry_OP_MKDIR, Name: name, Mode: uint32(fs.ModeDir | fs.ModePerm)}
}

// describe renders entries as "op name[=content]" strings for comparison.
func describe(entries []*pb.WriteLogEntry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		op := strings.TrimPrefix(e.GetOp().String(), "OP_")
		out[i] = fmt.Sprintf("%s %s", strings.ToLower(op), e.GetName())
		if e.GetOp() == pb.WriteLogEntry_OP_PUT {
			out[i] += "=" + string(e.GetContent())
		}
	}
	return out
}

func mustAppend(t *testing.T, l Log, entries ...*pb.WriteLogEntry) {
	t.Helper()
	for _, e := range entries {
		if err := l.Append(e); err != nil {
			t.Fatalf("Append(%v) = %v", e, err)
		}
	}
}

func mustSnapshot(t *testing.T, l Log) *Snapshot {
	t.Helper()
	s, err := l.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot() = %v", err)
	}
	return s
}

func TestMemoryLogDedupe(t *testing.T) {
	tests := []struct {
		name    string
		files   map[string]string // source file contents
		entries []*pb.WriteLogEntry
		want    []string
	}{
		{
			name:    "put then put keeps one",
			files:   map[string]string{"a": "latest"},
			entries: []*pb.WriteLogEntry{putEntry("a", 1), putEntry("a", 6)},
			want:    []string{"put a=latest"},
		},
		{
			name:    "put then remove",
			entries: []*pb.WriteLogEntry{putEntry("a", 1), removeEntry("a")},
			want:    []string{"remove_all a"},
		},
		{
			name:    "remove then put",
			files:   map[string]string{"a": "new"},
			entries: []*pb.WriteLogEntry{removeEntry("a"), putEntry("a", 3)},
			want:    []string{"remove_all a", "put a=new"},
		},
		{
			name:    "mkdir then put under it",
			files:   map[string]string{"d/f": "x"},
			entries: []*pb.WriteLogEntry{mkdirEntry("d"), putEntry("d/f", 1)},
			want:    []string{"mkdir d", "put d/f=x"},
		},
		{
			name:    "repeated mkdir keeps one",
			entries: []*pb.WriteLogEntry{mkdirEntry("d"), mkdirEntry("d")},
			want:    []string{"mkdir d"},
		},
		{
			name: "remove of a parent drops pending children",
			entries: []*pb.WriteLogEntry{
				mkdirEntry("d"), putEntry("d/f", 1), putEntry("d/sub/g", 1), removeEntry("d/sub"), putEntry("dx", 1),
				removeEntry("d"),
			},
			files: map[string]string{"dx": "keep"},
			want:  []string{"remove_all d", "put dx=keep"},
		},
		{
			name:    "remove of the root drops everything",
			entries: []*pb.WriteLogEntry{putEntry("a", 1), mkdirEntry("d"), removeEntry(".")},
			want:    []string{"remove_all ."},
		},
		{
			name:    "replay order is removes, mkdirs parents first, puts",
			files:   map[string]string{"b": "b", "a/x": "ax"},
			entries: []*pb.WriteLogEntry{putEntry("b", 1), mkdirEntry("a/y"), putEntry("a/x", 2), mkdirEntry("a"), removeEntry("z")},
			want:    []string{"remove_all z", "mkdir a", "mkdir a/y", "put a/x=ax", "put b=b"},
		},
		{
			name:    "put of a removed file is skipped",
			entries: []*pb.WriteLogEntry{putEntry("gone", 1)},
			want:    []string{},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			src := newMem(t, "memory://src")
			for name, content := range tc.files {
				writeFile(t, src, name, content)
			}
			l := NewMemoryLog(src)
			mustAppend(t, l, tc.entries...)
			if got := describe(mustSnapshot(t, l).Entries); !slices.Equal(got, tc.want) {
				t.Errorf("Snapshot() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestMemoryLogPayloadMetadata(t *testing.T) {
	src := newMem(t, "memory://src")
	writeFile(t, src, "f", "content")
	info, err := src.Stat("f")
	if err != nil {
		t.Fatal(err)
	}
	l := NewMemoryLog(src)
	entry := putEntry("f", 1)
	entry.Content = []byte("ignored")
	mustAppend(t, l, entry)
	if got := l.Bytes(); got != 1 {
		t.Errorf("Bytes() = %d, want the entry's size 1", got)
	}
	s := mustSnapshot(t, l)
	if len(s.Entries) != 1 {
		t.Fatalf("Snapshot() = %v, want one entry", s.Entries)
	}
	e := s.Entries[0]
	if string(e.GetContent()) != "content" || e.GetSize() != 7 || !e.GetModTime().AsTime().Equal(info.ModTime()) || fs.FileMode(e.GetMode()) != info.Mode() {
		t.Errorf("Snapshot() entry = %v, want the source file's content and metadata", e)
	}
}

func TestMemoryLogSkipsDirectoryPayload(t *testing.T) {
	src := newMem(t, "memory://src")
	if err := src.MkdirAll("now-a-dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	l := NewMemoryLog(src)
	mustAppend(t, l, putEntry("now-a-dir", 1))
	if got := mustSnapshot(t, l).Entries; len(got) != 0 {
		t.Errorf("Snapshot() = %v, want the put of a path that is now a directory skipped", got)
	}
}

func TestMemoryLogCommit(t *testing.T) {
	src := newMem(t, "memory://src")
	writeFile(t, src, "a", "a")
	writeFile(t, src, "b", "b")
	l := NewMemoryLog(src)
	mustAppend(t, l, putEntry("a", 1), putEntry("b", 1), removeEntry("c"))
	if !l.Pending("a") || l.Pending("c") || l.Pending("missing") {
		t.Errorf("Pending(a, c, missing) = %v, %v, %v; want true, false, false", l.Pending("a"), l.Pending("c"), l.Pending("missing"))
	}
	if got := l.Bytes(); got != 2 {
		t.Errorf("Bytes() = %d, want 2", got)
	}
	s := mustSnapshot(t, l)

	// b is written again and c removed again after the snapshot; both stay
	// pending once the snapshot commits.
	mustAppend(t, l, putEntry("b", 5), removeEntry("c"))
	if err := l.Commit(s); err != nil {
		t.Fatal(err)
	}
	if l.Pending("a") {
		t.Error("Pending(a) = true after Commit")
	}
	if !l.Pending("b") {
		t.Error("Pending(b) = false, want the newer write kept")
	}
	if got := l.Bytes(); got != 5 {
		t.Errorf("Bytes() = %d, want 5", got)
	}
	want := []string{"remove_all c", "put b=b"}
	if got := describe(mustSnapshot(t, l).Entries); !slices.Equal(got, want) {
		t.Errorf("Snapshot() after Commit = %q, want %q", got, want)
	}
	if err := l.Commit(nil); err != nil {
		t.Errorf("Commit(nil) = %v", err)
	}
}

func TestMemoryLogRejectsInvalidEntries(t *testing.T) {
	l := NewMemoryLog(newMem(t, "memory://src"))
	if err := l.Append(&pb.WriteLogEntry{Name: "a"}); !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("Append(unspecified op) = %v, want fs.ErrInvalid", err)
	}
	ufsTesting.AssertInvalidPathError(t, "../a", l.Append(putEntry("../a", 1)), "writelog")
}

// failingReadFS fails Open with err.
type failingReadFS struct {
	ufs.FS
	err error
}

func (f failingReadFS) Open(string) (fs.File, error) { return nil, f.err }

func TestMemoryLogSnapshotReadError(t *testing.T) {
	boom := errors.New("boom")
	l := NewMemoryLog(failingReadFS{FS: newMem(t, "memory://src"), err: boom})
	mustAppend(t, l, putEntry("a", 1))
	if _, err := l.Snapshot(); !errors.Is(err, boom) {
		t.Errorf("Snapshot() = %v, want %v", err, boom)
	}
}
