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

package types

import (
	"context"
	"io"
)

// NotifyOp describes the kind of change observed on a path.
type NotifyOp int

const (
	// NotifyCreate indicates a file or directory was created.
	NotifyCreate NotifyOp = iota
	// NotifyWrite indicates a file was written to.
	NotifyWrite
	// NotifyRemove indicates a file or directory was removed.
	NotifyRemove
	// NotifyRename indicates a file or directory was renamed.
	NotifyRename
	// NotifyChmod indicates permissions or attributes changed.
	NotifyChmod
)

// NotifyHook is invoked for each change observed by a [Watcher]. The path
// argument is root-relative, forward-slash separated, and satisfies
// [fs.ValidPath] — it is never an OS-native or absolute path.
type NotifyHook func(op NotifyOp, path string)

// Watcher is an optional interface implemented by file systems that can
// deliver recursive change notifications for a directory subtree.
type Watcher interface {
	// Watch begins watching name (a directory) and all nested directories,
	// invoking hook for each observed change. Watching stops when ctx is
	// canceled or the returned [io.Closer] is closed, whichever comes first.
	// Closing is idempotent and must terminate all background goroutines.
	//
	// The hook is called serially from a single background goroutine; it
	// should not block for long.
	Watch(ctx context.Context, name string, hook NotifyHook) (io.Closer, error)
}
