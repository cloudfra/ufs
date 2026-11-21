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

package types

import (
	"context"
	"io/fs"
)

// SnapshotFS is a file system that can save checkpoints of its state while still accept new changes.
//
// Changes in snapshots may be written back to the parent file system.
type SnapshotFS interface {
	fs.FS
	WriteFS

	// Checkpoint produces a writable snapshot of the file system's current state.
	//
	// The snapshot is isolated from its parent: changes written to the snapshot
	// are invisible to the parent until CommitChanges writes them back. Both the
	// snapshot and its parent keep accepting writes while the snapshot exists.
	Checkpoint(context.Context) (SnapshotFS, error)

	// CommitChanges writes all changes pending in the snapshot back to the parent.
	//
	// If there are multiple snapshots created from the parent the other snapshots will become invalidated.
	CommitChanges(context.Context) error
}
