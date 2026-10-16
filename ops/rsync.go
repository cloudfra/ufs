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

package ops

import (
	"io/fs"
	"path"

	"github.com/cloudfra/ufs/drivers/types"
)

// Rsync copies all files under dir from srcFS into destFS, preserving the
// relative path structure. Parent directories in destFS are created with
// [fs.ModePerm] as needed. Existing files in destFS are overwritten. The copy
// is not atomic: if an error occurs mid-walk, destFS may be partially written.
//
// dir must satisfy [fs.ValidPath]; use "." to copy the entire file system.
func Rsync(srcFS fs.FS, destFS types.WriteFS, dir string) error {
	// TODO: Prevent archive traversal.
	return ForEachFilename(srcFS, dir, func(name string) error {
		dir, _ := path.Split(name)
		dir = path.Clean(dir)
		if err := destFS.MkdirAll(dir, fs.ModePerm); err != nil {
			return err
		}
		if err := Copy(srcFS, name, destFS, name); err != nil {
			return err
		}
		return nil
	})
}
