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
	"slices"
	"testing"

	"github.com/cloudfra/ufs"
	pb "github.com/cloudfra/ufs/proto"
)

func withContent(e *pb.WriteLogEntry, content string) *pb.WriteLogEntry {
	e.Content = []byte(content)
	e.Size = int64(len(content))
	return e
}

// batchFS is an FS that implements BatchWriter by recording the batches.
type batchFS struct {
	ufs.FS
	batches [][]*pb.WriteLogEntry
}

func (b *batchFS) BatchWrite(entries []*pb.WriteLogEntry) error {
	b.batches = append(b.batches, entries)
	return nil
}

func TestApplyUsesBatchWriter(t *testing.T) {
	b := &batchFS{FS: newMem(t, "memory://dst")}
	entries := []*pb.WriteLogEntry{removeEntry("x"), withContent(putEntry("a", 0), "a")}
	if err := Apply(b, entries); err != nil {
		t.Fatal(err)
	}
	if len(b.batches) != 1 || !slices.Equal(b.batches[0], entries) {
		t.Errorf("BatchWrite calls = %v, want one with every entry", b.batches)
	}
	if _, err := b.Stat("a"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(a) = %v, want the entries applied only through BatchWrite", err)
	}
}

func TestApplyOneAtATime(t *testing.T) {
	dst := newMem(t, "memory://dst")
	writeFile(t, dst, "old/file", "old")
	writeFile(t, dst, "replace", "old")
	entries := []*pb.WriteLogEntry{
		removeEntry("old"),
		removeEntry("never/existed"),
		mkdirEntry("empty/dir"),
		withContent(putEntry("replace", 0), "new"),
		withContent(putEntry("deep/nested/file", 0), "nested"),
	}
	if err := Apply(dst, entries); err != nil {
		t.Fatalf("Apply() = %v", err)
	}
	if _, err := dst.Stat("old"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(old) = %v, want fs.ErrNotExist", err)
	}
	if info, err := dst.Stat("empty/dir"); err != nil || !info.IsDir() {
		t.Errorf("Stat(empty/dir) = (%v, %v), want a directory", info, err)
	}
	for name, want := range map[string]string{"replace": "new", "deep/nested/file": "nested"} {
		if got, err := dst.ReadFile(name); err != nil || string(got) != want {
			t.Errorf("ReadFile(%q) = (%q, %v), want (%q, nil)", name, got, err, want)
		}
	}
}

func TestApplyErrors(t *testing.T) {
	dst := newMem(t, "memory://dst")
	writeFile(t, dst, "file", "")
	if err := dst.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name  string
		entry *pb.WriteLogEntry
	}{
		{"unspecified op", &pb.WriteLogEntry{Name: "x"}},
		{"put over a directory", putEntry("dir", 0)},
		{"put under a file", putEntry("file/child", 0)},
		{"mkdir over a file", mkdirEntry("file")},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if err := Apply(dst, []*pb.WriteLogEntry{tc.entry}); err == nil {
				t.Error("Apply() = nil, want an error")
			}
		})
	}
}

func TestApplyWriteError(t *testing.T) {
	boom := errors.New("boom")
	dst := &failingWriteFS{FS: newMem(t, "memory://dst"), err: boom}
	if err := Apply(dst, []*pb.WriteLogEntry{withContent(putEntry("f", 0), "x")}); !errors.Is(err, boom) {
		t.Errorf("Apply() = %v, want %v", err, boom)
	}
}

// failingWriteFS returns files whose Write fails with err.
type failingWriteFS struct {
	ufs.FS
	err error
}

func (f *failingWriteFS) Create(name string) (ufs.File, error) {
	file, err := f.FS.Create(name)
	if err != nil {
		return nil, err
	}
	return failingWriteFile{File: file, err: f.err}, nil
}

type failingWriteFile struct {
	ufs.File
	err error
}

func (f failingWriteFile) Write([]byte) (int, error) { return 0, f.err }

func TestDirPerm(t *testing.T) {
	if got := dirPerm(&pb.WriteLogEntry{Mode: uint32(fs.ModeDir | 0o700)}); got != 0o700 {
		t.Errorf("dirPerm(0700) = %v, want 0700", got)
	}
	if got := dirPerm(&pb.WriteLogEntry{}); got != fs.ModePerm {
		t.Errorf("dirPerm(0) = %v, want %v", got, fs.ModePerm)
	}
}
