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

// errors.go carries the error mapping of the service in both directions: toStatus
// is what a handler puts on the wire, fromStatus is what a driver reads back off
// it. They sit in one file because they are one table read each way — the
// sentinel that decides the code going out is the sentinel the code decides coming
// back.

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

// fromStatus turns an error reported by a call of the service back into the error
// the file system at the other end reported.
//
// It is the inverse of toStatus: the sentinel decided the code going out and the
// code decides the sentinel coming back, so errors.Is answers the same on this
// side of a connection as it does on that one — which is what lets code written
// against this file system test for fs.ErrNotExist instead of reading a grpc
// status. The text that came with the code is kept only where no sentinel covers
// the failure, because there the text is the whole answer.
func fromStatus(err error) error {
	if err == nil {
		return nil
	}
	st, ok := status.FromError(err)
	if !ok {
		return err
	}
	switch st.Code() {
	case codes.NotFound:
		return fs.ErrNotExist
	case codes.AlreadyExists:
		return fs.ErrExist
	case codes.FailedPrecondition:
		// The one code toStatus reserves for a non-empty directory, which is the
		// only way ufserrors.ErrDirNotEmpty survives the trip.
		return ufserrors.ErrDirNotEmpty
	case codes.PermissionDenied:
		return fs.ErrPermission
	case codes.InvalidArgument:
		return fs.ErrInvalid
	case codes.Unimplemented:
		return errors.ErrUnsupported
	case codes.Canceled:
		return context.Canceled
	case codes.DeadlineExceeded:
		return context.DeadlineExceeded
	default:
		// A failure of the connection rather than of a file system — an address
		// that is down, a stream that was cut — which has no file-system sentinel.
		return st.Err()
	}
}

// pathError reports a failed call in the shape the io/fs interfaces and the
// conformance suite of drivers/testing ask for: an *fs.PathError naming the
// operation and the path, wrapping the error the server answered with.
func pathError(op, name string, err error) error {
	return ufserrors.NewPathError(op, name, fromStatus(err))
}
