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
	"context"
	"errors"
	"io/fs"

	"github.com/cloudfra/ufs/internal/ufserrors"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// toStatus turns a file-system error into the status the service documents. The
// sentinel decides the code and the whole error text stays in the message, so a
// client reading the status back can rebuild an error whose errors.Is answers
// match the file system at the other end of the connection.
//
// The code follows the sentinel, not the text: proto/ufs.proto names no codes, so
// what has to survive the trip is the sentinel a caller tests with, and the code is
// how it travels. A non-empty directory is the one case where that sends a sentinel
// against the ordinary mapping: it travels as FailedPrecondition, the only code that
// can carry ufserrors.ErrDirNotEmpty across the wire, because that is the sentinel
// memFS, boltFS and gcsFS report, and FailedPrecondition is the only way to keep it
// apart from the fs.ErrExist a non-empty directory satisfies on the operating systems
// that report ENOTEMPTY.
//
// An error that already carries a status keeps it, so a handler that reports a
// problem of the service rather than of a file system is not recoded here.
func toStatus(err error) error {
	if err == nil {
		return nil
	}
	if _, ok := status.FromError(err); ok {
		return err
	}
	code := codes.Unknown
	switch {
	case errors.Is(err, ufserrors.ErrDirNotEmpty):
		// This case has to come before fs.ErrExist, which ENOTEMPTY satisfies.
		code = codes.FailedPrecondition
	case errors.Is(err, errors.ErrUnsupported):
		// A method the file system has no means to perform, which is the answer
		// a file handle gives for a seek it cannot do.
		code = codes.Unimplemented
	case errors.Is(err, fs.ErrNotExist):
		code = codes.NotFound
	case errors.Is(err, fs.ErrExist):
		code = codes.AlreadyExists
	case errors.Is(err, fs.ErrPermission):
		code = codes.PermissionDenied
	case errors.Is(err, fs.ErrInvalid):
		code = codes.InvalidArgument
	case errors.Is(err, context.Canceled):
		code = codes.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		code = codes.DeadlineExceeded
	}
	return status.Error(code, err.Error())
}
