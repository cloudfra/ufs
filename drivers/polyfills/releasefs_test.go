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

package polyfills

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// The fakes have to satisfy exactly the interfaces they claim, since which of
// them a file system implements is what decides the dispatch of the standin.
var (
	_ fs.FS           = plainFS{}
	_ types.ReleaseFS = (*releasingFS)(nil)
)

// errRelease is what a file system answers when letting go of what it holds does
// not work.
var errRelease = errors.New("cannot close the archive")

// releaseLog counts the releases a fake file system was asked for, which is how
// a test tells the call-through from the standin answering nil.
type releaseLog struct {
	calls int
}

func (l *releaseLog) add() {
	l.calls++
}

// releasingFS implements [types.ReleaseFS] over a file system that has something
// of its own to let go of. It answers releaseErr, which is nil for a file system
// that manages to let go.
type releasingFS struct {
	*store
	log        *releaseLog
	releaseErr error
}

func (fsys *releasingFS) Open(name string) (fs.File, error) { return fsys.open(name) }

func (fsys *releasingFS) Release() error {
	fsys.log.add()
	return fsys.releaseErr
}

func TestRelease(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		wrap func(*store, *releaseLog) fs.FS
		// releases is how many times the case asks for a release.
		releases int
		// wantCalls is how many times the file system's own Release was reached.
		wantCalls int
		wantErr   string
	}{
		{
			// Only the file system knows what it holds on to, so its own Release has
			// to do the work.
			name:      "the file system's own Release is used",
			wrap:      func(s *store, l *releaseLog) fs.FS { return &releasingFS{store: s, log: l} },
			releases:  1,
			wantCalls: 1,
		},
		{
			// A file system that acquired nothing answers nil for itself, and the
			// standin does not turn that into an unimplemented error.
			name:     "a file system that is not a types.ReleaseFS answers nil",
			wrap:     func(s *store, _ *releaseLog) fs.FS { return plainFS{s} },
			releases: 1,
		},
		{
			// The reason the file system could not let go is what the caller is told;
			// the standin adds nothing to it.
			name:      "the failure of the file system is answered",
			wrap:      func(s *store, l *releaseLog) fs.FS { return &releasingFS{store: s, log: l, releaseErr: errRelease} },
			releases:  1,
			wantCalls: 1,
			wantErr:   errRelease.Error(),
		},
		{
			// Releasing is not the operation that finishes a file system, so a repeat
			// reaches the file system again rather than being answered on its behalf.
			name:      "a second release reaches the file system again",
			wrap:      func(s *store, l *releaseLog) fs.FS { return &releasingFS{store: s, log: l} },
			releases:  3,
			wantCalls: 3,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			log := &releaseLog{}
			fsys := tc.wrap(s, log)

			var err error
			for range tc.releases {
				err = Release(fsys)
			}
			checkWantErr(t, "Release", err, tc.wantErr)

			if log.calls != tc.wantCalls {
				t.Errorf("Release() called the file system's own Release %d times, want %d", log.calls, tc.wantCalls)
			}
		})
	}
}
