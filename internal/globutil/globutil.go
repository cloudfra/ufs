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
//
// Like fs.Glob, it ignores I/O errors such as a directory that cannot be read;
// the only error it returns is path.ErrBadPattern.
func GlobFS(fsys fs.ReadDirFS, pattern string) ([]string, error) {
	return GlobFSFunc(fsys, pattern, nil)
}

// GlobFSFunc is GlobFS, except that a directory for which skip returns true
// is not searched when the pattern component that matched it contains a
// wildcard. The directory itself is still a match, and a pattern component
// that names it literally still searches it.
func GlobFSFunc(fsys fs.ReadDirFS, pattern string, skip func(dir string) bool) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	return globWalk(fsys, pathutil.CwdPath, pattern, skip)
}

// HasMeta reports whether pattern contains any of the characters that
// path.Match treats specially.
func HasMeta(pattern string) bool {
	return strings.ContainsAny(pattern, `*?[\`)
}

func globWalk(fsys fs.ReadDirFS, dir, pattern string, skip func(dir string) bool) ([]string, error) {
	// Split the leftmost path component off the pattern.
	part, rest, _ := strings.Cut(pattern, "/")

	entries, err := fsys.ReadDir(dir)
	if err != nil {
		// A directory that cannot be read has no matches.
		return nil, nil
	}
	wildcard := skip != nil && HasMeta(part)

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
			if wildcard && skip(entryPath) {
				continue
			}
			sub, err := globWalk(fsys, entryPath, rest, skip)
			if err != nil {
				return nil, err
			}
			matches = append(matches, sub...)
		}
	}
	sort.Strings(matches)
	return matches, nil
}
