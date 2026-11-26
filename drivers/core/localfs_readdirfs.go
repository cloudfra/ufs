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

package core

// ReadDir is the [fs.ReadDirFS] half of [types.ReadFS].

import (
	"io/fs"
	"log/slog"
	"sort"
)

func (fsys *localFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := validLocalPath("readdir", name); err != nil {
		return nil, err
	}
	f, err := fsys.osFS.Open(name)
	if err != nil {
		return nil, err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close directory during ReadDir", "path", name, "error", err)
		}
	}()
	entries, err := f.ReadDir(-1)
	if err != nil {
		return nil, err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}
