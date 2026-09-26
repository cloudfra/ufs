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

// Package globutil implements fs.Glob for any FS that only provides ReadDir.
package globutil

import (
	"io/fs"
	"path"
	"sort"
	"strings"

	"github.com/cloudfra/ufs/internal/pathutil"
)

// GlobFS implements Glob for any FS that satisfies fs.ReadDirFS, walking
// level-by-level so the FS's own ReadDir is used (avoids routing back through
// fs.Glob which would recurse if the FS implements GlobFS).
func GlobFS(fsys fs.ReadDirFS, pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	return globWalk(fsys, pathutil.CwdPath, pattern)
}

func globWalk(fsys fs.ReadDirFS, dir, pattern string) ([]string, error) {
	// Split the leftmost path component off the pattern.
	part, rest, _ := strings.Cut(pattern, "/")

	entries, err := fsys.ReadDir(dir)
	if err != nil {
		return nil, err
	}

	var matches []string
	for _, e := range entries {
		matched, err := path.Match(part, e.Name())
		if err != nil {
			return nil, err
		}
		if !matched {
			continue
		}
		entryPath := e.Name()
		if dir != pathutil.CwdPath {
			entryPath = dir + "/" + e.Name()
		}
		if rest == "" {
			matches = append(matches, entryPath)
		} else if e.IsDir() {
			sub, err := globWalk(fsys, entryPath, rest)
			if err != nil {
				return nil, err
			}
			matches = append(matches, sub...)
		}
	}
	sort.Strings(matches)
	return matches, nil
}
