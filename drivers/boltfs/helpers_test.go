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
	"fmt"
	"io"
	"io/fs"
	"path"
	"slices"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/cloudfra/ufs"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// testFileSystem mirrors the root package's shared testFileSystem harness:
// it round-trips content through Create/Write/Close/Open for a set of files
// (including nested ones) and then runs the standard fstest.TestFS
// conformance checks over the result.
func testFileSystem(t *testing.T, newFSFunc func(ctx context.Context, name string) (ufs.FS, error), name string) {
	t.Helper()
	fsys, err := newFSFunc(t.Context(), name)
	if err != nil {
		t.Fatalf("FileSystem %q has an error, %s", name, err)
	}
	if fsys == nil {
		t.Fatalf("FileSystem %q is nil", name)
	}

	wantFiles := []string{"a", "ab/b/c", "ab/d/c", "def", "abc", "abc.txt", "temp/abc.txt"}

	for _, dir := range []string{"ab/b", "temp", "ab/d"} {
		if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
			t.Fatalf("cannot create directory %q, %s", dir, err)
		}
	}

	for _, name := range wantFiles {
		t.Run(fmt.Sprintf("crud_%s", name), func(t *testing.T) {
			wantData := ufsTesting.RandomString(1000)
			wf, err := fsys.Create(name)
			if err != nil {
				t.Fatalf("cannot create file %q, %s", name, err)
			}
			info, err := wf.Stat()
			if err != nil {
				t.Fatalf("cannot Stat() %q, %s", name, err)
			}
			if info.IsDir() {
				t.Errorf("%q is a directory, want file", name)
			}
			if n, err := io.WriteString(wf, wantData); err != nil {
				t.Errorf("cannot write file content to %q, %s", name, err)
			} else if n != len(wantData) {
				t.Errorf("contents written to file does not match the size got %d, want %d", n, len(wantData))
			}
			if err := wf.Close(); err != nil {
				t.Errorf("failed to Close() write file %q, %s", name, err)
			}

			rf, err := fsys.Open(name)
			if err != nil {
				t.Fatalf("cannot open file %q, %s", name, err)
			}
			defer ufsTesting.ValidateClose(t, rf)()
			info, err = rf.Stat()
			if err != nil {
				t.Fatalf("cannot Stat() %q, %s", name, err)
			}
			if info.IsDir() {
				t.Errorf("%q is a directory, want file", name)
			}
			if info.Name() != path.Base(name) {
				t.Errorf("Name() = %q, want %q", info.Name(), path.Base(name))
			}
			got, err := io.ReadAll(rf)
			if err != nil {
				t.Errorf("cannot read file content of %q, %s", name, err)
			} else if diff := cmp.Diff(wantData, string(got)); diff != "" {
				t.Errorf("io.ReadAll(%s) mismatch (-want +got):\n%s", name, diff)
			}
		})
	}

	if err := fstest.TestFS(fsys, wantFiles...); err != nil {
		t.Errorf("fstest.TestFS failed for %q: %v", name, err)
	}

	if err := fsys.Close(); err != nil {
		t.Errorf("error on Close(), %v", err)
	}
}

type notifyEvent struct {
	op   ufs.NotifyOp
	path string
}

type eventCollector struct {
	mu     sync.Mutex
	events []notifyEvent
	ch     chan struct{}
}

func newEventCollector() *eventCollector {
	return &eventCollector{ch: make(chan struct{}, 1024)}
}

func (c *eventCollector) hook(op ufs.NotifyOp, path string) {
	c.mu.Lock()
	c.events = append(c.events, notifyEvent{op: op, path: path})
	c.mu.Unlock()
	select {
	case c.ch <- struct{}{}:
	default:
	}
}

func (c *eventCollector) waitFor(t *testing.T, deadline time.Duration, match func(notifyEvent) bool) {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		c.mu.Lock()
		found := slices.ContainsFunc(c.events, match)
		c.mu.Unlock()
		if found {
			return
		}
		select {
		case <-timer.C:
			c.mu.Lock()
			events := c.events
			c.mu.Unlock()
			t.Fatalf("timed out waiting for matching event; collected: %v", events)
			return
		case <-c.ch:
		}
	}
}

func (c *eventCollector) hasEvent(match func(notifyEvent) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.events, match)
}

const eventDeadline = 5 * time.Second
