// Copyright 2026 Cloudfra
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

package syncfs

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
)

// These tests check the exclusion File promises by counting, inside the
// handle it wraps, how many calls are in progress at once, rather than
// relying on the race detector to notice an overlap: every call must be alone
// in the handle except a native ReadAt, which may overlap other ReadAt calls
// but nothing else.

// probe counts the calls in progress on a handle and records any overlap that
// File should have prevented. Every call yields a few times while inside, so
// an overlap the wrapper allowed is likely to be seen.
type probe struct {
	exclusive  atomic.Int32
	positional atomic.Int32

	mu         sync.Mutex
	violations []string
}

func (p *probe) violate(format string, a ...any) {
	p.mu.Lock()
	if len(p.violations) < 5 {
		p.violations = append(p.violations, fmt.Sprintf(format, a...))
	}
	p.mu.Unlock()
}

// enterExclusive marks a call that must be alone in the handle.
func (p *probe) enterExclusive(op string) {
	if n := p.exclusive.Add(1); n != 1 {
		p.violate("%s ran alongside %d other exclusive calls", op, n-1)
	}
	if n := p.positional.Load(); n != 0 {
		p.violate("%s ran alongside %d positional calls", op, n)
	}
	yield()
}

func (p *probe) exitExclusive() { p.exclusive.Add(-1) }

// enterPositional marks a native ReadAt, which may overlap other ReadAt calls
// but not an exclusive call.
func (p *probe) enterPositional(op string) {
	p.positional.Add(1)
	if n := p.exclusive.Load(); n != 0 {
		p.violate("%s ran alongside %d exclusive calls", op, n)
	}
	yield()
}

func (p *probe) exitPositional() { p.positional.Add(-1) }

func (p *probe) report(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, v := range p.violations {
		t.Error(v)
	}
}

// outcomeIrrelevant takes the results of a call made only for the traffic it
// puts through the wrapper: the probe, not the call's answer, is what these
// tests judge.
func outcomeIrrelevant(...any) {}

func yield() {
	for range 3 {
		runtime.Gosched()
	}
}

// probeFile has every method File can forward to, so every one goes through
// the probe. Its contents never change: WriteAt and Write accept the bytes and
// discard them, which keeps ReadAt answers checkable while writes run.
type probeFile struct {
	*probe
	pos int64
}

func (f *probeFile) Stat() (fs.FileInfo, error) {
	f.enterExclusive("Stat")
	defer f.exitExclusive()
	return types.NewFileInfo("probe", int64(len(content)), 0o644, time.Time{}), nil
}

func (f *probeFile) Read(p []byte) (int, error) {
	f.enterExclusive("Read")
	defer f.exitExclusive()
	if f.pos >= int64(len(content)) {
		return 0, io.EOF
	}
	n := copy(p, content[f.pos:])
	f.pos += int64(n)
	return n, nil
}

func (f *probeFile) Write(p []byte) (int, error) {
	f.enterExclusive("Write")
	defer f.exitExclusive()
	return len(p), nil
}

func (f *probeFile) Seek(offset int64, whence int) (int64, error) {
	f.enterExclusive("Seek")
	defer f.exitExclusive()
	switch whence {
	case io.SeekStart:
	case io.SeekCurrent:
		offset += f.pos
	case io.SeekEnd:
		offset += int64(len(content))
	default:
		return 0, fs.ErrInvalid
	}
	f.pos = offset
	return offset, nil
}

func (f *probeFile) ReadDir(int) ([]fs.DirEntry, error) {
	f.enterExclusive("ReadDir")
	defer f.exitExclusive()
	return nil, io.EOF
}

func (f *probeFile) Close() error {
	f.enterExclusive("Close")
	defer f.exitExclusive()
	return nil
}

func (f *probeFile) ReadAt(p []byte, off int64) (int, error) {
	f.enterPositional("ReadAt")
	defer f.exitPositional()
	if off >= int64(len(content)) {
		return 0, io.EOF
	}
	n := copy(p, content[off:])
	if n < len(p) {
		return n, io.EOF
	}
	return n, nil
}

func (f *probeFile) WriteAt(p []byte, _ int64) (int, error) {
	f.enterExclusive("WriteAt")
	defer f.exitExclusive()
	return len(p), nil
}

// cursorOnly is a probe handle with only the methods fs.File requires, so the
// wrapper takes its emulated and refusing paths: the positional calls become
// exclusive, and Write, Seek and ReadDir are refused after asking Stat for the
// name to report.
type cursorOnly struct{ f *probeFile }

func (c cursorOnly) Stat() (fs.FileInfo, error) { return c.f.Stat() }
func (c cursorOnly) Read(p []byte) (int, error) { return c.f.Read(p) }
func (c cursorOnly) Close() error               { return c.f.Close() }

// hammer calls every method of f from several goroutines at once. ReadAt
// answers are checked against content, since nothing changes it.
func hammer(t *testing.T, f *File) {
	t.Helper()
	ops := []func(i int){
		func(i int) {
			off := int64(i % len(content))
			buf := make([]byte, 4)
			n, err := f.ReadAt(buf, off)
			want := content[off:min(off+4, int64(len(content)))]
			if string(buf[:n]) != want {
				t.Errorf("ReadAt(4, %d) = %q, %v, want %q", off, buf[:n], err, want)
			}
		},
		func(i int) { outcomeIrrelevant(f.WriteAt([]byte("x"), int64(i%len(content)))) },
		func(int) { outcomeIrrelevant(f.Read(make([]byte, 3))) },
		func(int) { outcomeIrrelevant(f.Write([]byte("y"))) },
		func(i int) { outcomeIrrelevant(f.Seek(int64(i%len(content)), io.SeekStart)) },
		func(int) { outcomeIrrelevant(f.Stat()) },
		func(int) { outcomeIrrelevant(f.ReadDir(1)) },
	}
	var wg sync.WaitGroup
	for g := range 8 {
		wg.Go(func() {
			for i := range 200 {
				ops[(g+i)%len(ops)](i)
			}
		})
	}
	wg.Wait()
}

// TestFileExclusion calls every method of a handle that has every method,
// from several goroutines at once, and checks that only native ReadAt calls
// ever overlapped.
func TestFileExclusion(t *testing.T) {
	t.Parallel()
	inner := &probeFile{probe: &probe{}}
	f := NewFile(inner)
	hammer(t, f)
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	inner.report(t)
}

// TestFileExclusionEmulated does the same for a handle with only Stat, Read and
// Close, where the positional calls are emulated at the cursor and Write, Seek
// and ReadDir are refused: every call, the Stat behind each refusal included,
// must be alone in the handle.
func TestFileExclusionEmulated(t *testing.T) {
	t.Parallel()
	inner := &probeFile{probe: &probe{}}
	f := NewFile(cursorOnly{inner})

	// A ReadAt off the cursor is refused, so only check that the refusals are
	// the documented ones and that the overlap counters stay clean.
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if _, err := f.Seek(0, io.SeekStart); !errors.Is(err, errors.ErrUnsupported) {
					t.Errorf("Seek on a handle without Seek = %v, want ErrUnsupported", err)
				}
				if _, err := f.Write([]byte("x")); !errors.Is(err, fs.ErrPermission) {
					t.Errorf("Write on a handle without Write = %v, want ErrPermission", err)
				}
				if _, err := f.WriteAt([]byte("x"), 0); !errors.Is(err, fs.ErrPermission) {
					t.Errorf("WriteAt on a handle without Write = %v, want ErrPermission", err)
				}
				if _, err := f.ReadDir(1); !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("ReadDir on a file = %v, want ErrInvalid", err)
				}
				outcomeIrrelevant(f.ReadAt(make([]byte, 2), 0))
				outcomeIrrelevant(f.Read(make([]byte, 1)))
				outcomeIrrelevant(f.Stat())
			}
		})
	}
	wg.Wait()
	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	inner.report(t)
}

// TestNativeReadAtOverlaps parks one native ReadAt inside the handle and checks
// that a second gets in beside it, which it could not if positional calls took
// the exclusive lock.
func TestNativeReadAtOverlaps(t *testing.T) {
	t.Parallel()
	inner := &parkFile{entered: make(chan struct{}), release: make(chan struct{})}
	f := NewFile(inner)

	var wg sync.WaitGroup
	for range 2 {
		wg.Go(func() { outcomeIrrelevant(f.ReadAt(make([]byte, 1), 0)) })
	}
	for i := range 2 {
		select {
		case <-inner.entered:
		case <-time.After(10 * time.Second):
			t.Fatalf("only %d of 2 ReadAt calls got into the handle at once", i)
		}
	}
	close(inner.release)
	wg.Wait()
}

// TestCloseWaitsForNativeReadAt parks a native ReadAt inside the handle, then
// closes the wrapper, and checks that the inner Close did not run until the
// ReadAt had returned, and that a ReadAt after Close reaches a closed handle.
func TestCloseWaitsForNativeReadAt(t *testing.T) {
	t.Parallel()
	inner := &parkFile{entered: make(chan struct{}), release: make(chan struct{})}
	f := NewFile(inner)

	readDone := make(chan struct{})
	go func() {
		outcomeIrrelevant(f.ReadAt(make([]byte, 1), 0))
		close(readDone)
	}()
	<-inner.entered

	closeDone := make(chan error)
	go func() { closeDone <- f.Close() }()

	// Give Close every chance to run early: it must stay blocked behind the
	// parked ReadAt however long the ReadAt takes.
	for range 100 {
		runtime.Gosched()
	}
	select {
	case err := <-closeDone:
		t.Fatalf("Close() = %v returned while a ReadAt was still in the handle", err)
	default:
	}

	close(inner.release)
	<-readDone
	if err := <-closeDone; err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
	if inner.closedDuringRead.Load() {
		t.Error("the inner Close ran while a ReadAt was in progress")
	}
	if _, err := f.ReadAt(make([]byte, 1), 0); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("ReadAt after Close = %v, want fs.ErrClosed", err)
	}
}

// parkFile is a handle whose ReadAt reports on entered and then waits for
// release, so a test can hold calls inside it for as long as it needs.
type parkFile struct {
	entered          chan struct{}
	release          chan struct{}
	reading          atomic.Int32
	closed           atomic.Bool
	closedDuringRead atomic.Bool
}

func (f *parkFile) Stat() (fs.FileInfo, error) {
	return types.NewFileInfo("park", 1, 0o644, time.Time{}), nil
}

func (f *parkFile) Read([]byte) (int, error) { return 0, io.EOF }

func (f *parkFile) ReadAt(p []byte, _ int64) (int, error) {
	if f.closed.Load() {
		return 0, fs.ErrClosed
	}
	f.reading.Add(1)
	defer f.reading.Add(-1)
	f.entered <- struct{}{}
	<-f.release
	return copy(p, "p"), nil
}

func (f *parkFile) Close() error {
	if f.reading.Load() != 0 {
		f.closedDuringRead.Store(true)
	}
	f.closed.Store(true)
	return nil
}

// TestEmulatedReadAtUnderCursorTraffic reads at every offset of a handle that
// can only seek while other goroutines move its cursor with Read, Seek and
// emulated WriteAt. Each ReadAt must still return the bytes at its own offset,
// which it cannot if a cursor move lands between its seek and its read.
func TestEmulatedReadAtUnderCursorTraffic(t *testing.T) {
	t.Parallel()
	f, _ := newSeekFile()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for range 3 {
		wg.Go(func() {
			buf := make([]byte, 2)
			for i := 0; ; i++ {
				select {
				case <-stop:
					return
				default:
				}
				switch i % 3 {
				case 0:
					outcomeIrrelevant(f.Read(buf))
				case 1:
					outcomeIrrelevant(f.Seek(int64(i%len(content)), io.SeekStart))
				default:
					// Writing back the byte already there keeps the
					// content fixed while still moving the cursor.
					off := i % len(content)
					outcomeIrrelevant(f.WriteAt([]byte{content[off]}, int64(off)))
				}
			}
		})
	}
	var readers sync.WaitGroup
	for off := range len(content) {
		readers.Go(func() {
			for range 20 {
				buf := make([]byte, 3)
				n, err := f.ReadAt(buf, int64(off))
				want := content[off:min(off+3, len(content))]
				if string(buf[:n]) != want || (err != nil && !errors.Is(err, io.EOF)) {
					t.Errorf("ReadAt(3, %d) = %q, %v, want %q", off, buf[:n], err, want)
					return
				}
			}
		})
	}
	readers.Wait()
	close(stop)
	wg.Wait()
}
