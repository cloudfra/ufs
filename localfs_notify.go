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
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

var _ Watcher = (*localFS)(nil)

// Watch implements [Watcher] for local file systems. It recursively watches
// name and all nested directories using github.com/fsnotify/fsnotify,
// translating OS-native absolute paths back to root-relative, forward-slash
// paths before invoking hook.
func (fsys *localFS) Watch(ctx context.Context, name string, hook NotifyHook) (io.Closer, error) {
	if err := validLocalPath("watch", name); err != nil {
		return nil, err
	}

	absRoot := fsys.osFS.Name()
	watchRoot, err := filepath.Abs(filepath.Join(absRoot, name))
	if err != nil {
		return nil, &fs.PathError{Op: "watch", Path: name, Err: err}
	}

	fi, err := os.Stat(watchRoot)
	if err != nil {
		return nil, &fs.PathError{Op: "watch", Path: name, Err: err}
	}
	if !fi.IsDir() {
		return nil, &fs.PathError{Op: "watch", Path: name, Err: fs.ErrInvalid}
	}

	w, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	ctx, cancel := context.WithCancel(ctx)
	lw := &localWatcher{
		fsys:      fsys,
		watcher:   w,
		hook:      hook,
		cancel:    cancel,
		absRoot:   absRoot,
		watchRoot: watchRoot,
	}

	// Watch the root directory synchronously so events are delivered
	// immediately when Watch returns.
	if err := w.Add(watchRoot); err != nil {
		_ = w.Close()
		cancel()
		return nil, err
	}

	lw.wg.Add(1)
	go lw.loop(ctx)

	// Add subdirectory watches in the background so Watch() is not blocked
	// by a potentially slow filepath.WalkDir on large trees.
	lw.wg.Add(1)
	go func() {
		defer lw.wg.Done()
		_ = lw.addRecursive(watchRoot)
	}()

	return lw, nil
}

type localWatcher struct {
	fsys      *localFS
	watcher   *fsnotify.Watcher
	hook      NotifyHook
	cancel    context.CancelFunc
	absRoot   string
	watchRoot string

	closeOnce sync.Once
	wg        sync.WaitGroup
}

func (lw *localWatcher) Close() error {
	var err error
	lw.closeOnce.Do(func() {
		lw.cancel()
		err = lw.watcher.Close()
		lw.wg.Wait()
	})
	return err
}

// addRecursive walks dir and adds an fsnotify watch on every directory.
func (lw *localWatcher) addRecursive(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			return lw.watcher.Add(p)
		}
		return nil
	})
}

// toRelPath converts an OS-native absolute path to a root-relative,
// forward-slash path suitable for the NotifyHook. Returns "", false if the
// path falls outside the FS root.
func (lw *localWatcher) toRelPath(absPath string) (string, bool) {
	absPath = coerceUnix(absPath)
	root := coerceUnix(lw.absRoot)
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}

	rel, ok := strings.CutPrefix(absPath, root)
	if !ok {
		if absPath == strings.TrimSuffix(root, "/") {
			return cwdPath, true
		}
		return "", false
	}
	if rel == "" {
		return cwdPath, true
	}
	if !fs.ValidPath(rel) {
		return "", false
	}
	return rel, true
}

func (lw *localWatcher) loop(ctx context.Context) {
	defer lw.wg.Done()

	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-lw.watcher.Events:
			if !ok {
				return
			}
			lw.handleEvent(ev)
		case _, ok := <-lw.watcher.Errors:
			if !ok {
				return
			}
		}
	}
}

func (lw *localWatcher) handleEvent(ev fsnotify.Event) {
	rel, ok := lw.toRelPath(ev.Name)
	if !ok || rel == cwdPath {
		return
	}

	op, valid := convertOp(ev.Op)
	if !valid {
		return
	}

	if ev.Has(fsnotify.Create) {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			// New directory: register watches for it and any children that
			// appeared before the watch was installed.
			_ = lw.addRecursive(ev.Name)
		}
	}

	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		// fsnotify auto-cleans watches when paths are deleted. We used to call
		// Remove synchronously but on Windows during rapid create/delete churn
		// it can hang (watch already removed or handle transient lock), blocking
		// the event loop. Use a goroutine with short timeout as a safety net.
		done := make(chan struct{})
		go func() {
			_ = lw.watcher.Remove(ev.Name)
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
		}
	}

	lw.hook(op, rel)
}

func convertOp(op fsnotify.Op) (NotifyOp, bool) {
	switch {
	case op.Has(fsnotify.Create):
		return NotifyCreate, true
	case op.Has(fsnotify.Write):
		return NotifyWrite, true
	case op.Has(fsnotify.Remove):
		return NotifyRemove, true
	case op.Has(fsnotify.Rename):
		return NotifyRename, true
	case op.Has(fsnotify.Chmod):
		return NotifyChmod, true
	default:
		return 0, false
	}
}
