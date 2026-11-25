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

// grpcUpload is the handle [grpcFS.Create] returns: [types.File] write side,
// delivered by the WriteFile rpc when it is closed.

import (
	"context"
	"io/fs"
	"path"
	"sync"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
)

var _ types.File = (*grpcUpload)(nil)

// createPerm is the mode a handle from [grpcFS.Create] asks the server for.
// Create takes no permission argument, and the mode field of the rpc has no way
// to say "yours to choose" — a zero on the wire is a file with no permission
// bits, not an unset one — so the handle states the mode Go's os.Create uses and
// leaves the served file system's umask to trim it.
const createPerm = fs.FileMode(0o666)

// grpcUpload is a writing handle over the WriteFile rpc: what is written goes
// into the upload in order, and the file the name holds is replaced by the whole
// of it when the stream ends.
//
// This is a whole-file write seen from the inside. The bytes leave as they are
// handed over, so a caller need not hold a large file in memory, but the rpc has
// no file behind it: it has no position to move back to and nothing to read out
// of, and the answer to whether the write worked — including what the served file
// system made of the name — comes at Close, because a client-streaming rpc answers
// only once its last frame is in. That is why [grpcFS.Create] asks the served
// file system about the name before it returns a handle: an open that is going to
// be refused has to be refused there.
type grpcUpload struct {
	fsys *grpcFS
	name string

	mu sync.Mutex
	// stream is the upload, and started says its first frame — the one that names
	// the file and the mode of a file that does not exist yet — has been sent.
	stream  proto.FS_WriteFileClient
	started bool
	cancel  context.CancelFunc
	// written counts the bytes the upload took, which is what [grpcUpload.Stat]
	// reports while the file is still only what the handle is making it.
	written int64
	// failure is why the upload ended early, reported again by every call that
	// came after it instead of waiting on a stream that is already over.
	failure error
	closed  bool
	// closeErr is kept so that closing a handle twice reports the same answer,
	// which is what io.Closer means by closing twice not being an error.
	closeErr error
}

func newGrpcUpload(name string, fsys *grpcFS) *grpcUpload {
	return &grpcUpload{fsys: fsys, name: name}
}

// Write adds p to the upload, in frames no larger than the connection is asked
// to carry.
func (u *grpcUpload) Write(p []byte) (int, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return 0, ufserrors.NewPathError("write", u.name, fs.ErrClosed)
	}
	if u.failure != nil {
		return 0, ufserrors.NewPathError("write", u.name, u.failure)
	}
	written := 0
	for len(p) > 0 {
		n := min(len(p), frameBytes)
		if err := u.sendLocked(p[:n]); err != nil {
			return written, err
		}
		written += n
		u.written += int64(n)
		p = p[n:]
	}
	return written, nil
}

// WriteString writes s, which is the method [types.File] asks of a handle and
// the one the conformance suite of drivers/testing writes content with.
func (u *grpcUpload) WriteString(s string) (int, error) {
	return u.Write([]byte(s))
}

// sendLocked sends one frame, opening the upload with the first one. It is
// called with the mutex held.
func (u *grpcUpload) sendLocked(data []byte) error {
	if !u.started {
		ctx, cancel := context.WithCancel(u.fsys.ctx)
		stream, err := u.fsys.client.WriteFile(ctx)
		if err != nil {
			cancel()
			u.failure = fromStatus(err)
			return pathError("write", u.name, err)
		}
		u.stream, u.cancel = stream, cancel
	}
	// The path and the mode travel in the first frame only, which is what lets
	// the whole upload be one HTTP request body. A file with nothing in it still
	// needs that frame, because a stream with no frames names no file to write.
	frame := &proto.WriteFileRequest{Data: data}
	if !u.started {
		frame.Name = u.name
		frame.Mode = uint32(createPerm)
		u.started = true
	}
	if err := u.stream.Send(frame); err != nil {
		u.failure = fromStatus(err)
		return pathError("write", u.name, err)
	}
	return nil
}

// Read is refused: the upload has nothing to read back out of, since the file it
// writes is not in the served file system until the stream ends. What was written
// is read back through [grpcFS.ReadFile] or a handle from [grpcFS.Open] once
// Close has run.
func (u *grpcUpload) Read([]byte) (int, error) {
	return 0, ufserrors.NewPathError("read", u.name, fs.ErrInvalid)
}

// ReadAt is refused for the same reason Read is.
func (u *grpcUpload) ReadAt([]byte, int64) (int, error) {
	return 0, ufserrors.NewPathError("readat", u.name, fs.ErrInvalid)
}

// Seek is refused: an upload only moves forward, so there is no position here to
// move back to and rewrite from. A caller that has to change part of a file
// writes the whole of it, which is what the rpc offers.
func (u *grpcUpload) Seek(int64, int) (int64, error) {
	return 0, ufserrors.NewPathError("seek", u.name, fs.ErrInvalid)
}

// Stat reports what the handle has made of the file so far rather than what the
// served file system has: a whole-file write is not visible there until the
// upload ends, which is the same reason a handle from an upload-backing driver
// reports the bytes it buffered instead of a size it cannot know yet. The mode is
// the one the handle asks for, before the served file system's umask has had its
// say.
func (u *grpcUpload) Stat() (fs.FileInfo, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed && u.closeErr != nil {
		return nil, u.closeErr
	}
	return types.NewFileInfo(path.Base(u.name), u.written, createPerm, time.Time{}), nil
}

// Close ends the upload and answers what the served file system made of the
// whole file. Closing a handle twice reports what the first close did, and a
// handle that was never written still writes an empty file, because Create
// promises to replace what the name holds. An upload that broke under an earlier
// write is reported broken again here, the way an os.File that lost data reports
// it at Close: what Close answers is whether the file is there, and a caller that
// copied into the handle and checked only Close would otherwise be told it was.
func (u *grpcUpload) Close() error {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.closed {
		return u.closeErr
	}
	u.closed = true
	if u.cancel != nil {
		defer u.cancel()
	}
	if u.failure != nil {
		// Nothing was written: the server only makes the file once the whole
		// stream is in, so a stream that ended early leaves no file behind, and
		// the answer is the failure that ended it rather than success.
		u.closeErr = pathError("close", u.name, u.failure)
		return u.closeErr
	}
	if !u.started {
		if err := u.sendLocked(nil); err != nil {
			u.closeErr = err
			return err
		}
	}
	if _, err := u.stream.CloseAndRecv(); err != nil {
		u.failure = fromStatus(err)
		u.closeErr = pathError("close", u.name, err)
		return u.closeErr
	}
	return nil
}
