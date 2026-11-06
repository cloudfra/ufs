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

package host

import (
	"errors"
	"fmt"
	"sync"
)

// ErrMountLimit means the process cannot mount another volume while it holds
// one. Closing a mounted volume can wait forever on files the process holds
// open on a second volume, so a second volume is refused instead of risking a
// mount that can never be closed. Test for it with errors.Is.
var ErrMountLimit = errors.New("mount limit reached")

// volumeSet tracks the volumes a process holds so that a mount mechanism can
// refuse to hold more than volumeLimit volumes at once. A limit of zero is
// unlimited.
type volumeSet struct {
	mu    sync.Mutex
	limit int
	held  []*volume
}

// volume is one mount held by the process, tracked from the start of its mount
// until it is unmounted.
type volume struct {
	set       *volumeSet
	mountPath string
	// server is nil until the volume is mounted. It is only read or written
	// with set.mu held, and never read while a server is closing.
	server MountServer
}

// volumes is the set of volumes held by this process.
var volumes = volumeSet{limit: volumeLimit}

// claim takes a slot for mountPath, or returns an error matching ErrMountLimit
// when the set already holds its limit of volumes. The slot is held until the
// returned volume is released, which keeps the limit enforced across the whole
// lifetime of a mounted volume rather than only during the mount.
func (s *volumeSet) claim(mountPath string) (*volume, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.limit > 0 && len(s.held) >= s.limit {
		return nil, fmt.Errorf("%w: this process already holds a mount at %q", ErrMountLimit, s.held[0].mountPath)
	}
	v := &volume{set: s, mountPath: mountPath}
	s.held = append(s.held, v)
	return v, nil
}

// release gives back the volume's slot. It is safe to call more than once.
func (v *volume) release() {
	s := v.set
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, held := range s.held {
		if held == v {
			s.held = append(s.held[:i], s.held[i+1:]...)
			return
		}
	}
}

// setServer records the server that mounted the volume, which makes it one of
// the volumes ReleaseMounts can close.
func (v *volume) setServer(server MountServer) {
	s := v.set
	s.mu.Lock()
	defer s.mu.Unlock()
	v.server = server
}

// releaseAll closes every mounted volume in the set, most recently mounted
// first, and returns their joined errors. A volume whose close does not return
// keeps its slot, so releaseAll can be called again.
func (s *volumeSet) releaseAll() error {
	s.mu.Lock()
	mounted := make([]*volume, 0, len(s.held))
	for _, v := range s.held {
		if v.server != nil {
			mounted = append(mounted, v)
		}
	}
	s.mu.Unlock()

	// Closing a volume releases its slot, so the snapshot above is taken
	// before the loop to keep the mutex out of the unmount path.
	var errs []error
	for i := len(mounted) - 1; i >= 0; i-- {
		if err := mounted[i].server.Close(); err != nil {
			errs = append(errs, fmt.Errorf("unmounting %q: %w", mounted[i].mountPath, err))
		}
	}
	return errors.Join(errs...)
}

// ReleaseMounts unmounts every volume this process still holds, so that a
// panic or signal handler can leave no volume mounted behind it. It returns
// the joined errors of the volumes it could not unmount; a volume whose
// unmount does not return keeps its slot and cannot be released from inside
// the process.
func ReleaseMounts() error {
	return volumes.releaseAll()
}
