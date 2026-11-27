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

package grpcfs

// WriteFile is the [fs.WriteFileFS] half of what the service answers: the
// same upload as [grpcFS.Create] in one call rather than through a handle.

import (
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

// WriteFile writes content to name whole, as the WriteFile rpc does: the file is
// created if it does not exist and truncated first, and the parents are not
// created. A file small enough for one frame is sent in one message.
//
// The closed check and the whole upload are one read-lock section. A
// client-streaming rpc is several sends and the answer that follows them, so a
// connection given up partway through would take the middle of an upload off the
// wire rather than refuse it at the start. The path validation stays outside it.
func (fsys *grpcFS) WriteFile(name string, content []byte, perm fs.FileMode) error {
	if err := pathutil.ValidateFileName("writefile", name); err != nil {
		return err
	}
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return ufserrors.NewPathError("writefile", name, fs.ErrClosed)
	}
	err := fsys.writeFileLocked(name, content, perm)
	fsys.mu.RUnlock()
	return err
}

// writeFileLocked is the upload behind WriteFile. Its caller holds the read lock
// for the whole of it, every frame and the answer, which is what keeps the
// connection alive underneath them; the helper takes no lock itself.
func (fsys *grpcFS) writeFileLocked(name string, content []byte, perm fs.FileMode) error {
	stream, err := fsys.client.WriteFile(fsys.ctx)
	if err != nil {
		return pathError("writefile", name, err)
	}
	first := true
	for len(content) > 0 || first {
		n := min(len(content), frameBytes)
		req := &proto.WriteFileRequest{Data: content[:n]}
		if first {
			// The path and the mode travel in the first frame only, which is what
			// lets the whole upload be one HTTP request body. An empty file still
			// needs that first frame, because a stream with no frames names no
			// file to write.
			req.Name = name
			req.Mode = uint32(perm)
			first = false
		}
		if err := stream.Send(req); err != nil {
			return pathError("writefile", name, err)
		}
		content = content[n:]
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		return pathError("writefile", name, err)
	}
	return nil
}
