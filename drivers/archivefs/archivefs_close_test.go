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

package archivefs

import (
	"errors"
	"io/fs"
	"sync/atomic"
	"testing"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestArchiveFSCloseClosesUnderlyingFile(t *testing.T) {
	t.Parallel()

	var closeCalled atomic.Int32
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		closeCalled.Add(1)
		return nil
	}))

	if err := afs.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if closeCalled.Load() != 1 {
		t.Error("underlying file closer was not called on archiveFS.Close()")
	}
}

func TestArchiveFSCloseWithoutCloserIsNoop(t *testing.T) {
	t.Parallel()

	afs := makeArchiveFS(emptyFS{}, "test.zip", nil)
	if err := afs.Close(); err != nil {
		t.Errorf("Close() = %v, want nil (no closer set)", err)
	}
}

func TestArchiveFSCloseReportsCloserError(t *testing.T) {
	t.Parallel()

	wantErr := errors.New("file close failed")
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		return wantErr
	}))

	err := afs.Close()
	if !errors.Is(err, wantErr) {
		t.Errorf("Close() = %v, want %v", err, wantErr)
	}
}

func TestArchiveFSCloseIdempotent(t *testing.T) {
	t.Parallel()

	var closeCalled atomic.Int32
	afs := makeArchiveFS(emptyFS{}, "test.zip", closerFunc(func() error {
		closeCalled.Add(1)
		return nil
	}))

	for range 3 {
		ufsTesting.ValidateClose(t, afs)()
	}
	if closeCalled.Load() != 1 {
		t.Errorf("closer called %d times, want exactly 1", closeCalled.Load())
	}
}

// closerFunc adapts a bare function to io.Closer.
type closerFunc func() error

func (f closerFunc) Close() error { return f() }

// emptyFS is a minimal fs.FS that contains no files.
type emptyFS struct{}

func (emptyFS) Open(name string) (fs.File, error) {
	return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
}
