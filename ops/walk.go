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

package ops

import (
	"fmt"
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// WalkArgs configures traversal behavior for [Walk].
type WalkArgs struct {
	// IncludeMountedArchive controls whether virtual archive-mount directories
	// (e.g. "data.zip.d") are descended into during the walk. When false
	// (the default), such directories are skipped entirely.
	IncludeMountedArchive bool

	// ExcludeDirectory is a list of glob patterns matched against each
	// directory's base name using [path.Match]. Directories whose names match
	// any pattern are skipped along with all their contents. A nil or empty
	// slice applies no filter.
	ExcludeDirectory []string
}

// Walk walks dir in fsys, calling f for each file whose ancestor directories
// pass the filters in args. It differs from [ForEachFilename] in two ways:
// virtual archive-mount directories (e.g. "data.zip.d") are skipped by default
// (set [WalkArgs.IncludeMountedArchive] to descend into them), and directories
// whose base names match any [WalkArgs.ExcludeDirectory] glob are skipped
// entirely. The walk stops and returns the first non-nil error from f.
func Walk(fsys fs.FS, dir string, args WalkArgs, f func(string) error) error {
	adc, ok := fsys.(types.MountedArchiveDirFS)
	return fs.WalkDir(fsys, dir, func(name string, d fs.DirEntry, err error) error {
		if pathutil.IsCwd(name) {
			return nil
		}
		if err != nil {
			return err
		}
		if d.IsDir() {
			if !args.IncludeMountedArchive && ok && adc.IsMountedArchiveDir(name) {
				return fs.SkipDir
			}
			for _, pattern := range args.ExcludeDirectory {
				if matched, err := path.Match(pattern, d.Name()); matched {
					return fs.SkipDir
				} else if err != nil {
					return fmt.Errorf("error compiling the regex %q for matching, %w", pattern, err)
				}
			}
			return nil
		}
		return f(name)
	})
}
