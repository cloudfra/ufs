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

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/proto"
)

// ReadFile returns the whole content of the file at name.
func (fsys *grpcFS) ReadFile(name string) ([]byte, error) {
	if err := fsys.closedErr("readfile", name); err != nil {
		return nil, err
	}
	if err := pathutil.ValidateFileName("readfile", name); err != nil {
		return nil, err
	}
	return fsys.readRange("readfile", name, 0, 0)
}

// readRange asks the ReadFile rpc for the part of a file that starts at offset
// and runs for limit bytes, a limit of none meaning to the end, and concatenates
// the frames it streams back.
func (fsys *grpcFS) readRange(op, name string, offset, limit int64) ([]byte, error) {
	stream, err := fsys.client.ReadFile(fsys.ctx, &proto.ReadFileRequest{Name: name, Offset: offset, Limit: limit})
	if err != nil {
		return nil, pathError(op, name, err)
	}
	var data []byte
	first := true
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			return data, nil
		}
		if err != nil {
			return nil, pathError(op, name, err)
		}
		if first {
			first = false
			// The first frame says how big the whole answer is. An answer of
			// one frame is that frame's own bytes, which the message does not
			// share with anything once it is decoded, so it is kept rather
			// than copied; a longer one is collected into one allocation
			// rather than grown frame by frame.
			chunk := frame.GetData()
			if size := frame.GetSize(); size > int64(len(chunk)) {
				data = make([]byte, 0, min(size, maxPreallocate))
				data = append(data, chunk...)
			} else {
				data = chunk
			}
			continue
		}
		data = append(data, frame.GetData()...)
	}
}
