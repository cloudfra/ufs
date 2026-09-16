// Copyright 2026 Jeremy Edwards
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

//go:build !windows

package ufs

import (
	"io/fs"
	"os"
	"strings"
)

// localFSNormalizePath strips the "file://" or "file:" URI prefix, leaving a plain path.
func localFSNormalizePath(name string) string {
	if after, ok := strings.CutPrefix(name, "file://"); ok {
		return after
	}
	return strings.TrimPrefix(name, "file:")
}

func validLocalPath(op, name string) error {
	return validPath(op, name)
}

func localFSWrapFile(f *os.File) fs.File {
	return f
}

func localFSNormalizeDirInfo(fi fs.FileInfo) fs.FileInfo {
	return fi
}
