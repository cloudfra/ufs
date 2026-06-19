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

package ufs

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"sync"
)

var _ Watcher = (*memFS)(nil)

// Watch implements [Watcher] for in-memory file systems. It watches name (a
// directory) and all nested paths, invoking hook for each mutation performed
// through the memFS API (Create, Write, Remove, RemoveAll, MkdirAll).
func (fsys *memFS) Watch(ctx context.Context, name string, hook NotifyHook) (io.Closer, error) {
	if fsys.isClosed() {
		return nil, pathError("watch", name, fs.ErrClosed)
	}
	if err := validPath("watch", name); err != nil {
		return nil, err
	}

	fsys.mu.RLock()
	if name != cwdPath {
		node, ok := fsys.nodes[name]
		if !ok {
			fsys.mu.RUnlock()
			return nil, pathError("watch", name, fs.ErrNotExist)
		}
		if !node.isDir {
			fsys.mu.RUnlock()
			return nil, pathError("watch", name, fs.ErrInvalid)
		}
	}
	fsys.mu.RUnlock()

	ctx, cancel := context.WithCancel(ctx)
	mw := &memWatcher{
		fsys:   fsys,
		prefix: name,
		hook:   hook,
		cancel: cancel,
		done:   make(chan struct{}),
		events: make(chan memNotifyEvent, 256),
	}

	fsys.watchersMu.Lock()
	fsys.watchers = append(fsys.watchers, mw)
	fsys.watchersMu.Unlock()

	go mw.loop(ctx)

	return mw, nil
}

type memWatcher struct {
	fsys   *memFS
	prefix string
	hook   NotifyHook
	cancel context.CancelFunc

	closeOnce sync.Once
	done      chan struct{}
	events    chan memNotifyEvent
}

type memNotifyEvent struct {
	op   NotifyOp
	path string
}

func (mw *memWatcher) Close() error {
	mw.closeOnce.Do(func() {
		mw.cancel()
		<-mw.done
		mw.fsys.removeWatcher(mw)
	})
	return nil
}

func (mw *memWatcher) loop(ctx context.Context) {
	defer close(mw.done)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-mw.events:
			if !ok {
				return
			}
			mw.hook(ev.op, ev.path)
		}
	}
}

// matches reports whether path falls under this watcher's watched prefix.
func (mw *memWatcher) matches(path string) bool {
	if mw.prefix == cwdPath {
		return path != cwdPath
	}
	return path == mw.prefix || strings.HasPrefix(path, mw.prefix+"/")
}

// send enqueues an event if the path matches. Non-blocking: drops events if
// the channel is full (best-effort, same as OS watchers).
func (mw *memWatcher) send(op NotifyOp, path string) {
	if !mw.matches(path) {
		return
	}
	select {
	case mw.events <- memNotifyEvent{op: op, path: path}:
	default:
	}
}

func (fsys *memFS) removeWatcher(mw *memWatcher) {
	fsys.watchersMu.Lock()
	defer fsys.watchersMu.Unlock()
	for i, w := range fsys.watchers {
		if w == mw {
			fsys.watchers = append(fsys.watchers[:i], fsys.watchers[i+1:]...)
			return
		}
	}
}

// notify sends an event to all active watchers.
func (fsys *memFS) notify(op NotifyOp, path string) {
	fsys.watchersMu.RLock()
	watchers := fsys.watchers
	fsys.watchersMu.RUnlock()
	for _, mw := range watchers {
		mw.send(op, path)
	}
}
