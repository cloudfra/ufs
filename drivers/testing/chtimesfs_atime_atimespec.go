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

//go:build darwin || freebsd || netbsd

package testing

import (
	"io/fs"
	"syscall"
	"time"
)

// fileAtime reads the access time out of the platform stat structure that a
// local file system's [fs.FileInfo] carries, the same reading as
// chtimesfs_atime_atim.go. It is a separate file only because the Darwin and BSD
// stat structures name the access time Atimespec rather than Atim, which is what
// the build constraint selects.
func fileAtime(info fs.FileInfo) (time.Time, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	// The same width difference as in chtimesfs_atime_atim.go: int on the
	// 32-bit platforms this constraint covers, int64 on the 64-bit ones.
	return time.Unix(int64(stat.Atimespec.Sec), int64(stat.Atimespec.Nsec)), true //nolint:unconvert // int on the 32-bit platforms in this constraint
}
