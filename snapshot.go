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
	pb "github.com/jeremyje/ufs/proto"
)

type SnapshotFS interface {
	// Sync writes the changes to the underlying WriteFS and returns an error if the operation fails. It is used to ensure that all changes made to the snapshot are persisted to the underlying storage.
	Sync() error
	GetLog() []*pb.SnapshotLogEntry
}

var (
	_ SnapshotFS = (*snapshotFS)(nil)
)

type snapshotFS struct {
	fsys WriteFS
	log  []*pb.SnapshotLogEntry
}

// Snapshot creates a new snapshot of the provided WriteFS. It returns a SnapshotFS that can be used to track changes made to the underlying WriteFS. The snapshot maintains a log of operations performed on the WriteFS, which can be retrieved using the GetLog method.
func Snapshot(fsys FS) SnapshotFS {
	return &snapshotFS{
		fsys: fsys.(WriteFS),
		log:  []*pb.SnapshotLogEntry{},
	}
}
