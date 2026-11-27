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

//go:build !(linux || android || openbsd || dragonfly || solaris || illumos || darwin || freebsd || netbsd)

package testing

import (
	"io/fs"
	"time"
)

// fileAtime reports that these suites cannot read an access time back on this
// platform, so chtimesAccessTime says so rather than judging a file system it
// cannot see. Windows keeps its times in a structure of its own that holds no
// access time to read, and the platforms with no file system behind them have no
// access time at all.
func fileAtime(fs.FileInfo) (time.Time, bool) {
	return time.Time{}, false
}
