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

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/emptypb"
)

// WriteFile collects one file's upload and writes it whole, as
// [types.WriteFileFS.WriteFile] does — through [polyfills.WriteFile] for a file
// system that has no whole-file write of its own, which is why the content is
// buffered rather than handed to the file system a frame at a time.
//
// Holding the content is what [maxUploadSize] is for: it is the price of that
// choice, so the handler puts a ceiling on it rather than leaving the number of
// frames a client sends to bound nothing. An upload that arrived in one frame is
// written out of that frame, so what is held is the content rather than the
// frames: only a multipart upload is joined somewhere of the server's own.
//
// What the name may sit in is the handler's business too: the rpc says the parents
// are not created, and [Server.writeParent] is how that is kept regardless of
// which file system the server was handed.
func (s *Server) WriteFile(stream proto.FS_WriteFileServer) error {
	if !s.life.Enter() {
		return errShutdown
	}
	defer s.life.Exit()
	first, err := stream.Recv()
	if err != nil {
		if errors.Is(err, io.EOF) {
			// What the comment on WriteFileRequest says — an empty stream writes
			// an empty file — is about a stream carrying no content: a first frame
			// with no data in it does write an empty file, and the handler does
			// that. A stream with no frames at all cannot be answered, because the
			// path of the file travels in the first frame, so there is nothing to
			// write to.
			return status.Error(codes.InvalidArgument,
				"a WriteFile stream has to carry a first frame, which is where the path of the file is")
		}
		return err
	}
	size := len(first.GetData())
	if size > maxUploadSize {
		return uploadTooLarge(first.GetName())
	}
	// Checked before the rest of the upload is taken, so a client writing into a
	// directory that is not there learns that at the start of the request rather
	// than after sending all of its content.
	if err := s.writeParent(first.GetName()); err != nil {
		return toStatus(err)
	}
	// An upload that fits one frame is already one whole content in a slice, so
	// it is written out of that slice and nothing is copied for it. The buffer
	// appears at the second frame, which is the point where pieces have to be
	// joined: a frame's content is the transport's to reuse once the next read
	// begins, so the pieces of a multipart upload cannot be kept where they
	// arrived.
	var joined *bytes.Buffer
	for {
		frame, err := stream.Recv()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return err
		}
		if joined == nil {
			joined = bytes.NewBuffer(append([]byte(nil), first.GetData()...))
		}
		// Only the first frame's name and mode count, which is what lets the
		// whole upload travel as one HTTP request body.
		joined.Write(frame.GetData())
		size += len(frame.GetData())
		if size > maxUploadSize {
			return uploadTooLarge(first.GetName())
		}
	}
	content := first.GetData()
	if joined != nil {
		content = joined.Bytes()
	}
	// The mode goes through as the frame carries it, 0 included: proto3 cannot
	// tell an unset field from one set to 0, so a request that names no mode is
	// indistinguishable from one that asks for 000, and substituting a default for
	// a 0 here would break the caller that asked for 000. That leaves the wire
	// format and the io/fs permission bits disagreeing about what an omitted mode
	// means, which is a question about the .proto rather than one a handler can
	// settle.
	if err := polyfills.WriteFile(s.inner, first.GetName(), content, fs.FileMode(first.GetMode())); err != nil {
		return toStatus(err)
	}
	return stream.SendAndClose(&emptypb.Empty{})
}

// uploadTooLarge is the answer for an upload whose content has passed the ceiling
// one request may carry. ResourceExhausted is the code grpc itself gives a message
// that is too big, so an upload stopped by the handler and one stopped by the
// transport at least agree on what went wrong.
func uploadTooLarge(name string) error {
	return status.Errorf(codes.ResourceExhausted,
		"the upload of %q passed the %d byte ceiling one WriteFile request may carry: a file larger than that needs an upload the server writes as it arrives, which this service has no rpc for yet",
		name, maxUploadSize)
}

// writeParent answers the promise the rpc comment makes that the parents of an
// uploaded file are not created. The served file system cannot be left to make
// that rule: memFS builds the missing levels of a name it is asked to write, a
// file system rooted on disk reports that they are missing, and a client of this
// service cannot be made to care which one a server happens to stand on. So the
// handler looks up the one directory the upload needs and reports what it finds,
// which is the one extra lookup the promise costs.
//
// Only the level in front of the name is looked at: a deeper missing level is not
// reachable, so it is reported by the same NotFound this lookup gives, and the
// name itself is left to the write, which is where a name that is already a
// directory is refused. What it returns is a file-system error, recoded like every
// other answer the handler gets from the file system.
func (s *Server) writeParent(name string) error {
	dir := path.Dir(name)
	if dir == pathutil.CwdPath {
		return nil
	}
	info, err := s.inner.Stat(dir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		// fs.ErrExist rather than a not-a-directory sentinel because that is the
		// answer ufs file systems give for this name, memFS included, and what
		// drivers/testing asks of a create under a file.
		return ufserrors.NewPathError("writefile", name, fs.ErrExist)
	}
	return nil
}
