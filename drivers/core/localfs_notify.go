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

package core

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

var _ types.Watcher = (*localFS)(nil)

// Watch implements [Watcher] for local file systems. It recursively watches
// name and all nested directories using github.com/fsnotify/fsnotify,
// translating OS-native absolute paths back to root-relative, forward-slash
// paths before invoking hook.
func (fsys *localFS) Watch(ctx context.Context, name string, hook types.NotifyHook) (io.Closer, error) {
	if err := validLocalPath("watch", name); err != nil {
		return nil, err
	}

	absRoot := fsys.osFS.Name()
	watchRoot, err := filepath.Abs(filepath.Join(absRoot, name))
	if err != nil {
		return nil, &fs.PathError{Op: "watch", Path: name, Err: err}
	}

	fi, err := osutil.Stat(watchRoot)
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

	if err := lw.addRecursive(ctx, watchRoot); err != nil {
		slog.WarnContext(ctx, "got error while recursively adding files to local watcher", "error", err, "root", watchRoot)
		if err := w.Close(); err != nil {
			slog.WarnContext(ctx, "error while closing fsnotify watcher in response to recursive add failure", "error", err, "root", watchRoot)
		}
		cancel()
		return nil, err
	}

	lw.wg.Add(1)
	go lw.loop(ctx)

	return lw, nil
}

type localWatcher struct {
	fsys      *localFS
	watcher   *fsnotify.Watcher
	hook      types.NotifyHook
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

// addWalkAttempts is how many times addRecursive walks a directory before the
// failure to watch it is reported. Losing a watch is not benign: the subtree
// under it silently stops producing events. The failure is usually transient,
// most often on Windows, where a directory that has just been renamed into the
// watched tree is not always readable yet.
const addWalkAttempts = 3

// addWalkRetryDelay is how long addRecursive waits before re-walking a
// directory that could not be watched.
const addWalkRetryDelay = 20 * time.Millisecond

// addRecursive adds an fsnotify watch on dir and every directory beneath it.
// Directories that could not be watched are re-walked and the ones still
// unwatched are reported in the error: WalkDir hands the callback an error and
// stops descending when it cannot stat or read one, which used to reach neither
// the caller nor the log and left that subtree unwatched in silence.
func (lw *localWatcher) addRecursive(ctx context.Context, dir string) error {
	unwatched, err := lw.walkAndAdd(dir)
	for attempt := 1; len(unwatched) > 0; attempt++ {
		if attempt == addWalkAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(addWalkRetryDelay):
		}
		retry := make([]string, 0, len(unwatched))
		var retryErr error
		for _, p := range unwatched {
			un, e := lw.walkAndAdd(p)
			retry = append(retry, un...)
			if retryErr == nil {
				retryErr = e
			}
		}
		unwatched, err = retry, retryErr
	}
	if len(unwatched) == 0 {
		return nil
	}
	return fmt.Errorf("could not watch %s: %w", strings.Join(unwatched, ", "), err)
}

// walkAndAdd adds an fsnotify watch on every directory under dir, returning the
// directories it could not watch and the first error that caused one.
func (lw *localWatcher) walkAndAdd(dir string) ([]string, error) {
	var unwatched []string
	var firstErr error
	failed := func(p string, err error) {
		unwatched = append(unwatched, p)
		if firstErr == nil {
			firstErr = err
		}
	}
	if err := filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			// WalkDir will not descend, so the whole subtree under p is left
			// unwatched.
			failed(p, err)
			return nil
		}
		if d.IsDir() {
			if err := lw.watcher.Add(p); err != nil {
				failed(p, err)
			}
		}
		return nil
	}); err != nil {
		failed(dir, err)
	}
	return unwatched, firstErr
}

// toRelPath converts an OS-native absolute path to a root-relative,
// forward-slash path suitable for the NotifyHook. Returns "", false if the
// path falls outside the FS root.
func (lw *localWatcher) toRelPath(absPath string) (string, bool) {
	absPath = pathutil.CoerceUnix(absPath)
	root := pathutil.CoerceUnix(lw.absRoot)
	if !strings.HasSuffix(root, "/") {
		root += "/"
	}

	rel, ok := strings.CutPrefix(absPath, root)
	if !ok {
		if absPath == strings.TrimSuffix(root, "/") {
			return pathutil.CwdPath, true
		}
		return "", false
	}
	if rel == "" {
		return pathutil.CwdPath, true
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
			// select picks at random when ctx is also done, so check it
			// again: nothing is delivered once the watch is canceled.
			if !ok || ctx.Err() != nil {
				return
			}
			lw.handleEvent(ctx, ev)
		case err, ok := <-lw.watcher.Errors:
			// fsnotify reports dropped events and failed watches here rather
			// than returning them, so they are invisible unless logged. The
			// channel is closed only when the watcher is closed.
			if !ok {
				return
			}
			slog.ErrorContext(ctx, "Got error on watch", "error", err)
		}
	}
}

func (lw *localWatcher) handleEvent(ctx context.Context, ev fsnotify.Event) {
	rel, ok := lw.toRelPath(ev.Name)
	if !ok || rel == pathutil.CwdPath {
		return
	}

	op, valid := convertOp(ev.Op)
	if !valid {
		return
	}

	if ev.Has(fsnotify.Create) {
		fi, err := osutil.Stat(ev.Name)
		// A Stat that fails for a reason other than the path already being
		// gone is ambiguous, so the walk runs anyway: skipping it would leave
		// a new directory unwatched without saying so.
		if (err == nil && fi.IsDir()) || (err != nil && !errors.Is(err, fs.ErrNotExist)) {
			// New directory: register watches for it and any children that
			// appeared before the watch was installed.
			if err := lw.addRecursive(ctx, ev.Name); err != nil {
				slog.WarnContext(ctx, "got error while recursively adding files to local watcher", "error", err, "event.name", ev.Name, "event.op", ev.Op)
			}
		}
	}

	if ev.Has(fsnotify.Remove) || ev.Has(fsnotify.Rename) {
		// fsnotify auto-cleans watches when paths are deleted. We used to call
		// Remove synchronously but on Windows during rapid create/delete churn
		// it can hang (watch already removed or handle transient lock), blocking
		// the event loop. Use a goroutine with short timeout as a safety net.
		done := make(chan struct{})
		go func() {
			if err := lw.watcher.Remove(ev.Name); err != nil {
				slog.Warn("error while removing file from fsnotify watcher", "error", err, "event.name", ev.Name, "event.op", ev.Op)
			}
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
		}
	}

	lw.hook(op, rel)
}

func convertOp(op fsnotify.Op) (types.NotifyOp, bool) {
	switch {
	case op.Has(fsnotify.Create):
		return types.NotifyCreate, true
	case op.Has(fsnotify.Write):
		return types.NotifyWrite, true
	case op.Has(fsnotify.Remove):
		return types.NotifyRemove, true
	case op.Has(fsnotify.Rename):
		return types.NotifyRename, true
	case op.Has(fsnotify.Chmod):
		return types.NotifyChmod, true
	default:
		return 0, false
	}
}
