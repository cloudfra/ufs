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
	"io/fs"
	"time"

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
