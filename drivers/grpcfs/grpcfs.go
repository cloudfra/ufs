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

// Package grpcfs serves a ufs file system over the FS service of
// proto/ufs.proto: grpcfs.NewServer answers that service from a file system the
// process already holds, which is how the file system is read and written from
// another process or another machine.
//
// The service maps one rpc onto each method reachable from the types.WriteFS
// interface of drivers/types, so the server is a translation of one interface
// rather than an invention: it calls methods on a types.WriteFS and answers what
// it gets back. That decides what is not on the wire. Close and Release are
// absent because a server cannot decide when every client it serves is done with
// a file system it was handed; IsSame, InnerFS and ExternalPath are absent
// because none of them is a question about a location; and every optional
// interface a file system may or may not carry — Rename, Mkdir, OpenFile,
// BatchCreate, the link and mode-bit setters, Watcher — is absent
// because an rpc for each would make the surface a server offers depend on what
// the file system standing behind it happens to implement. A server therefore
// offers the same rpc surface for every file system it is handed.
package grpcfs

import (
	"context"
	"errors"
	"io/fs"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// toStatus turns a file-system error into the status the service documents. The
// sentinel decides the code and the whole error text stays in the message, so a
// client reading the status back can rebuild an error whose errors.Is answers
// match the file system at the other end of the connection.
//
// The four codes the service comment names are the common cases. A non-empty
// directory has no code of its own there, so it travels as FailedPrecondition,
// the only code that can carry ufserrors.ErrDirNotEmpty across the wire: that is
// the sentinel memFS, boltFS and gcsFS report, and FailedPrecondition is the only
// way to keep it apart from the fs.ErrExist a non-empty directory satisfies on the
// operating systems that report ENOTEMPTY.
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

// toProtoFile writes one node's metadata as a File resource.
//
// name is the path the caller asked for rather than info.Name(): a File is named
// by its whole path relative to the root, while fs.FileInfo reports only the last
// element, which is why the mapping needs the caller's path to stay faithful.
func toProtoFile(name string, info fs.FileInfo) *proto.File {
	return &proto.File{
		Name:    name,
		Size:    info.Size(),
		Mode:    uint32(info.Mode()),
		ModTime: toProtoTime(info.ModTime()),
		IsDir:   info.IsDir(),
	}
}

// toProtoTime leaves a time a file system does not report unset on the wire,
// which is what the File and FileSystemInfo comments ask for.
func toProtoTime(t time.Time) *timestamppb.Timestamp {
	if t.IsZero() {
		return nil
	}
	return timestamppb.New(t)
}

// toProtoDeviceMap writes the path-keyed device map a file system reports. The
// answer stays minimal, as types.DeviceMap is: a path with no entry of its own is
// backed by the entry of its nearest ancestor.
func toProtoDeviceMap(devices types.DeviceMap) map[string]*proto.DeviceInfo {
	if len(devices) == 0 {
		return nil
	}
	out := make(map[string]*proto.DeviceInfo, len(devices))
	for name, info := range devices {
		out[name] = &proto.DeviceInfo{
			Name:        info.Name(),
			DeviceType:  info.DeviceType(),
			ThreadCount: int32(info.ThreadCount()), //nolint:gosec // G115: the count is a small number of threads, the proto field is int32
		}
	}
	return out
}
