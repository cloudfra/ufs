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
	"sync"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	pb "github.com/cloudfra/ufs/proto"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// recordingLog is a Log that keeps every appended entry in order.
type recordingLog struct {
	mu      sync.Mutex
	entries []*pb.WriteLogEntry
	err     error
	block   chan struct{} // when set, Append waits for it to close
}

func (r *recordingLog) Append(e *pb.WriteLogEntry) error {
	if r.block != nil {
		<-r.block
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.err != nil {
		return r.err
	}
	r.entries = append(r.entries, e)
	return nil
}

func (r *recordingLog) Snapshot() (*Snapshot, error) { return &Snapshot{}, nil }
func (r *recordingLog) Commit(*Snapshot) error       { return nil }
func (r *recordingLog) Pending(string) bool          { return false }
func (r *recordingLog) Bytes() int64                 { return 0 }

func (r *recordingLog) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]string, len(r.entries))
	for i, e := range r.entries {
		out[i] = strings.ToLower(strings.TrimPrefix(e.GetOp().String(), "OP_")) + " " + e.GetName()
	}
	return out
}

func newRecordingFS(t *testing.T, mode Mode) (*FS, *recordingLog) {
	t.Helper()
	inner, err := ufs.New(t.Context(), "memory://inner")
	if err != nil {
		t.Fatal(err)
	}
	log := &recordingLog{}
	f := NewFS(inner, log, mode)
	t.Cleanup(ufsTesting.ValidateClose(t, f))
	return f, log
}

func TestFSDriver(t *testing.T) {
	for _, mode := range []Mode{Async, Sync} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			ufsdriversTesting.WriteFS(t, func(t *testing.T) ufs.WriteFS {
				inner, err := ufs.New(t.Context(), "memory://driver")
				if err != nil {
					t.Fatal(err)
				}
				return NewFS(inner, NewMemoryLog(inner), mode)
			})
		})
	}
}

func TestFSRecordsSuccessfulWrites(t *testing.T) {
	for _, mode := range []Mode{Async, Sync} {
		t.Run(fmt.Sprint(mode), func(t *testing.T) {
			f, log := newRecordingFS(t, mode)
			if err := f.MkdirAll("d/sub", fs.ModePerm); err != nil {
				t.Fatal(err)
			}
			if err := f.MkdirAll(".", fs.ModePerm); err != nil {
				t.Fatal(err)
			}
			ufsdriversTesting.WriteFile(t, f, "d/file", "content")
			if err := f.Remove("d/file"); err != nil {
				t.Fatal(err)
			}
			if err := f.Remove("only-elsewhere"); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Remove(only-elsewhere) = %v, want fs.ErrNotExist", err)
			}
			if err := f.RemoveAll("d"); err != nil {
				t.Fatal(err)
			}

			// Failed operations are not recorded.
			if err := f.Remove("."); !errors.Is(err, fs.ErrPermission) {
				t.Errorf("Remove(.) = %v, want fs.ErrPermission", err)
			}
			ufsdriversTesting.WriteFile(t, f, "file", "")
			if err := f.MkdirAll("file/sub", fs.ModePerm); err == nil {
				t.Error("MkdirAll(file/sub) = nil, want an error")
			}
			if _, err := f.Create("../escape"); err == nil {
				t.Error("Create(../escape) = nil, want an error")
			}
			if err := f.RemoveAll("../escape"); err == nil {
				t.Error("RemoveAll(../escape) = nil, want an error")
			}

			f.Barrier()
			want := []string{
				"mkdir d/sub",
				"put d/file",
				"remove_all d/file",
				"remove_all only-elsewhere",
				"remove_all d",
				"put file",
			}
			if got := log.recorded(); !slices.Equal(got, want) {
				t.Errorf("recorded %q, want %q", got, want)
			}
		})
	}
}

func TestFSPutMetadata(t *testing.T) {
	f, log := newRecordingFS(t, Sync)
	file, err := f.Create("f")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("12345"); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	// A second Close records nothing more.
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	info, err := f.Stat("f")
	if err != nil {
		t.Fatal(err)
	}
	if len(log.entries) != 1 {
		t.Fatalf("recorded %v, want one put", log.recorded())
	}
	e := log.entries[0]
	if e.GetSize() != 5 || len(e.GetContent()) != 0 || !e.GetModTime().AsTime().Equal(info.ModTime()) || fs.FileMode(e.GetMode()) != info.Mode() {
		t.Errorf("put entry = %v, want size 5, no content and the file's mode and modTime", e)
	}
}

func TestFSCloseOfRemovedFileRecordsNothing(t *testing.T) {
	f, log := newRecordingFS(t, Sync)
	file, err := f.Create("f")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Remove("f"); err != nil {
		t.Fatal(err)
	}
	if err := f.MkdirAll("f", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if want := []string{"remove_all f", "mkdir f"}; !slices.Equal(log.recorded(), want) {
		t.Errorf("recorded %q, want %q", log.recorded(), want)
	}
}

func TestFSSyncReturnsLogErrors(t *testing.T) {
	f, log := newRecordingFS(t, Sync)
	boom := errors.New("boom")
	log.err = boom
	var pe *fs.PathError
	if err := f.MkdirAll("d", fs.ModePerm); !errors.Is(err, boom) || !errors.As(err, &pe) || pe.Op != "mkdir" {
		t.Errorf("MkdirAll() = %v, want a mkdir *fs.PathError wrapping %v", err, boom)
	}
	if err := f.RemoveAll("d"); !errors.Is(err, boom) {
		t.Errorf("RemoveAll() = %v, want %v", err, boom)
	}
	if err := f.Remove("missing"); !errors.Is(err, boom) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Remove(missing) = %v, want both fs.ErrNotExist and %v", err, boom)
	}
	file, err := f.Create("f")
	if err != nil {
		t.Fatal(err)
	}
	if err := file.Close(); !errors.Is(err, boom) {
		t.Errorf("Close() = %v, want %v", err, boom)
	}
}

func TestFSAsyncLogsErrors(t *testing.T) {
	f, log := newRecordingFS(t, Async)
	log.err = errors.New("boom")
	if err := f.MkdirAll("d", fs.ModePerm); err != nil {
		t.Errorf("MkdirAll() = %v, want nil: async record errors are only logged", err)
	}
	f.Barrier()
}

func TestFSBarrierWaitsForRecords(t *testing.T) {
	f, log := newRecordingFS(t, Async)
	log.block = make(chan struct{})

	// The queue holds asyncQueueSize entries and the blocked recorder at most
	// one more, so writing two more than the queue holds must block.
	const writes = asyncQueueSize + 2
	written := make(chan struct{})
	go func() {
		defer close(written)
		for i := range writes {
			if err := f.MkdirAll(fmt.Sprintf("d%d", i), fs.ModePerm); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	select {
	case <-written:
		t.Fatal("writes to a full queue returned without blocking")
	case <-time.After(50 * time.Millisecond):
	}

	barrier := make(chan struct{})
	go func() {
		defer close(barrier)
		f.Barrier()
	}()
	select {
	case <-barrier:
		t.Fatal("Barrier() returned before the records were written")
	case <-time.After(50 * time.Millisecond):
	}

	close(log.block)
	<-written
	<-barrier
	f.Barrier()
	if got := len(log.recorded()); got != writes {
		t.Errorf("recorded %d entries, want %d", got, writes)
	}
}

func TestFSSyncBarrierReturns(t *testing.T) {
	f, _ := newRecordingFS(t, Sync)
	f.Barrier()
}

func TestFSCloseFlushesQueue(t *testing.T) {
	inner, err := ufs.New(t.Context(), "memory://inner")
	if err != nil {
		t.Fatal(err)
	}
	log := &recordingLog{}
	f := NewFS(inner, log, Async)
	if err := f.MkdirAll("d", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for range 3 {
		if err := f.Close(); err != nil {
			t.Fatalf("Close() = %v", err)
		}
	}
	if want := []string{"mkdir d"}; !slices.Equal(log.recorded(), want) {
		t.Errorf("recorded %q, want %q", log.recorded(), want)
	}
	f.Barrier()
	if err := f.MkdirAll("e", fs.ModePerm); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("MkdirAll() after Close = %v, want fs.ErrClosed", err)
	}
	// A late record, such as from a file closed after the FS, is dropped.
	if err := f.record(removeEntry("late")); err != nil {
		t.Errorf("record() after Close = %v, want nil", err)
	}
}

func TestFSString(t *testing.T) {
	f, _ := newRecordingFS(t, Sync)
	if got, want := f.String(), "writelog("+f.FS.String()+")"; got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}
