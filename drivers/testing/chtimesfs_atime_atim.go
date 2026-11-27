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

//go:build linux || android || openbsd || dragonfly || solaris || illumos

package testing

import (
	"io/fs"
	"syscall"
	"time"
)

// fileAtime reads the access time out of the platform stat structure that a
// local file system's [fs.FileInfo] carries, which is the only access time
// these suites can read back at all: an [fs.FileInfo] names a modification time
// and nothing else. It reports false for a file system whose Sys result holds
// anything else, as *core.memFS's does, because memFS keeps a modification time
// and no access time.
//
// The build constraint lists the platforms whose stat structure names its access
// time Atim. chtimesfs_atime_atimespec.go covers the platforms that name it
// Atimespec, chtimesfs_atime_other.go answers the rest and says why there.
func fileAtime(info fs.FileInfo) (time.Time, bool) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return time.Time{}, false
	}
	// Sec and Nsec are int on the 32-bit platforms this constraint covers and
	// int64 on the 64-bit ones, so the conversions are needed by half of them
	// and redundant for the other half.
	return time.Unix(int64(stat.Atim.Sec), int64(stat.Atim.Nsec)), true //nolint:unconvert // int on the 32-bit platforms in this constraint
}
