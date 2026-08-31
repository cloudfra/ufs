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
)

// MountServer represents a file system that is mounted and served to the host
// operating system. Call [MountServer.Wait] to block until the mount is removed,
// and [MountServer.Close] to unmount explicitly.
type MountServer interface {
	io.Closer

	// Wait blocks until the server is unmounted, either via Close or by an
	// external unmount (e.g. fusermount -u).
	Wait()
}

// HostMount mounts fsys at mountPath so the host operating system can access it
// as a regular directory tree. The returned [MountServer] must be closed when
// done; closing unmounts the file system.
//
// If fsys implements [FS] (read-write), file creation, writing, directory
// creation, and deletion are supported. Otherwise the mount is read-only.
//
// When ctx is canceled the file system is automatically unmounted.
//
// On platforms without a supported mount mechanism HostMount returns an error.
func HostMount(ctx context.Context, fsys ReadFS, mountPath string) (MountServer, error) {
	return hostMount(ctx, fsys, mountPath)
}
