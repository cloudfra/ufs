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

package telemetryfs

// Stat and Lstat are the [fs.StatFS] half of [types.ReadFS], each reported
// around a delegation to the wrapped file system.

import (
	"io/fs"
)

func (fsys *telemetryFS) Stat(name string) (fs.FileInfo, error) {
	span, start := fsys.begin(opStat)
	info, err := fsys.inner.Stat(name)
	fsys.end(opStat, name, span, start, err)
	return info, err
}

func (fsys *telemetryFS) Lstat(name string) (fs.FileInfo, error) {
	span, start := fsys.begin(opLstat)
	info, err := fsys.inner.Lstat(name)
	fsys.end(opLstat, name, span, start, err)
	return info, err
}
