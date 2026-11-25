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

// fileinfo.go carries the File resource of the service in both directions:
// toProtoFile and toProtoTime are what a handler writes, fromProtoFile and
// fromProtoTime are what a driver reads back. They sit in one file because a File
// on the wire is one shape seen from both ends.

import (
	"io/fs"
	"path"
	"time"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

// fromProtoTime reads a time back, giving the zero time.Time for a field that
// was left unset, which is how a file system that does not report a time reaches
// this side of the connection.
func fromProtoTime(t *timestamppb.Timestamp) time.Time {
	if t == nil {
		return time.Time{}
	}
	return t.AsTime()
}

// fromProtoFile reads one File resource back as metadata.
//
// A File is named by its whole path relative to the root — GetFile answers with
// the path it was asked about and ListFiles with the path of each child — while
// fs.FileInfo reports a single element, so the last element of the path on the
// wire is what the metadata carries.
func fromProtoFile(file *proto.File) types.FileInfo {
	return types.NewFileInfo(
		path.Base(file.GetName()),
		file.GetSize(),
		fs.FileMode(file.GetMode()), //nolint:gosec // G115: the mode is the fs.FileMode value the server wrote, so the round trip is exact
		fromProtoTime(file.GetModTime()),
	)
}
