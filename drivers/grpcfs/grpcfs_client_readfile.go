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

// ReadFile is the [fs.ReadFileFS] half of [types.ReadFS]. The readRange
// helper it shares with [grpcFile.ReadAt] is the same rpc with no handle around
// it.

import (
	"errors"
	"io"
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

// ReadFile returns the whole content of the file at name.
//
// ReadFile takes no lock of its own: readRange makes the closed decision and runs
// the rpc at its own use site, and a read lock held across that delegation would
// deadlock the pair against a Close waiting in between.
func (fsys *grpcFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.ValidateFileName("readfile", name); err != nil {
		return nil, err
	}
	return fsys.readRange("readfile", name, 0, 0)
}

// readRange asks the ReadFile rpc for the part of a file that starts at offset
// and runs for limit bytes, a limit of none meaning to the end, and concatenates
// the frames it streams back. The closed check and the whole stream are one
// read-lock section: the frames arrive over several receives, so a connection
// given up partway through would answer part of a file rather than none of it.
//
// It is the use site for both [grpcFS.ReadFile] and the ranged read of
// [grpcFile.ReadAt]. A handle calls in while holding its own mutex, which is the
// one order these two locks are ever taken in: nothing reaches a handle's mutex
// from under this file system's lock, so no Close can be waiting between them.
func (fsys *grpcFS) readRange(op, name string, offset, limit int64) ([]byte, error) {
	fsys.mu.RLock()
	if fsys.closed.Load() {
		fsys.mu.RUnlock()
		return nil, ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	data, err := fsys.readRangeLocked(op, name, offset, limit)
	fsys.mu.RUnlock()
	if err != nil {
		return nil, err
	}
	return data, nil
}

// readRangeLocked is the rpc and its frames behind a ranged read. Its caller
// holds the read lock for the whole of it, which is what keeps the connection
// alive underneath every frame; the helper takes no lock itself.
func (fsys *grpcFS) readRangeLocked(op, name string, offset, limit int64) ([]byte, error) {
	stream, err := fsys.client.ReadFile(fsys.ctx, &proto.ReadFileRequest{Name: name, Offset: offset, Limit: limit})
	if err != nil {
		return nil, pathError(op, name, err)
	}
	var data []byte
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return data, nil
		}
		if err != nil {
			return nil, pathError(op, name, err)
		}
		data = append(data, frame.GetData()...)
	}
}
