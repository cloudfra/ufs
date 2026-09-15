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

package ufs

import (
	"context"
	"io"
	"io/fs"
	"strings"
	"sync"

	bolt "go.etcd.io/bbolt"
)

var _ Watcher = (*boltFS)(nil)

// Watch implements [Watcher] for bolt-backed file systems. It watches name (a
// directory) and all nested paths, invoking hook for each mutation performed
// through the boltFS API (Create, file Close after a write, Remove,
// RemoveAll, MkdirAll).
func (fsys *boltFS) Watch(ctx context.Context, name string, hook NotifyHook) (io.Closer, error) {
	if fsys.isClosed() {
		return nil, pathError("watch", name, fs.ErrClosed)
	}
	if err := validPath("watch", name); err != nil {
		return nil, err
	}

	if name != cwdPath {
		err := fsys.withDB(func(db *bolt.DB) error {
			return db.View(func(tx *bolt.Tx) error {
				_, err := dirBucket(tx, name)
				return err
			})
		})
		if err != nil {
			return nil, pathError("watch", name, err)
		}
	}

	ctx, cancel := context.WithCancel(ctx)
	bw := &boltWatcher{
		fsys:   fsys,
		prefix: name,
		hook:   hook,
		cancel: cancel,
		done:   make(chan struct{}),
		events: make(chan boltNotifyEvent, 256),
	}

	fsys.watchersMu.Lock()
	fsys.watchers = append(fsys.watchers, bw)
	fsys.watchersMu.Unlock()

	go bw.loop(ctx)

	return bw, nil
}

type boltWatcher struct {
	fsys   *boltFS
	prefix string
	hook   NotifyHook
	cancel context.CancelFunc

	closeOnce sync.Once
	done      chan struct{}
	events    chan boltNotifyEvent
}

type boltNotifyEvent struct {
	op   NotifyOp
	path string
}

func (bw *boltWatcher) Close() error {
	bw.closeOnce.Do(func() {
		bw.cancel()
		<-bw.done
		bw.fsys.removeWatcher(bw)
	})
	return nil
}

func (bw *boltWatcher) loop(ctx context.Context) {
	defer close(bw.done)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-bw.events:
			if !ok {
				return
			}
			bw.hook(ev.op, ev.path)
		}
	}
}

// matches reports whether path falls under this watcher's watched prefix.
func (bw *boltWatcher) matches(path string) bool {
	if bw.prefix == cwdPath {
		return path != cwdPath
	}
	return path == bw.prefix || strings.HasPrefix(path, bw.prefix+"/")
}

// send enqueues an event if the path matches. Non-blocking: drops events if
// the channel is full (best-effort, same as OS watchers).
func (bw *boltWatcher) send(op NotifyOp, path string) {
	if !bw.matches(path) {
		return
	}
	select {
	case bw.events <- boltNotifyEvent{op: op, path: path}:
	default:
	}
}

func (fsys *boltFS) removeWatcher(bw *boltWatcher) {
	fsys.watchersMu.Lock()
	defer fsys.watchersMu.Unlock()
	for i, w := range fsys.watchers {
		if w == bw {
			fsys.watchers = append(fsys.watchers[:i], fsys.watchers[i+1:]...)
			return
		}
	}
}

// notify sends an event to all active watchers.
func (fsys *boltFS) notify(op NotifyOp, path string) {
	fsys.watchersMu.RLock()
	watchers := fsys.watchers
	fsys.watchersMu.RUnlock()
	for _, bw := range watchers {
		bw.send(op, path)
	}
}
