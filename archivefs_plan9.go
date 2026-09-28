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

package ufs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
)

// Archive support is unavailable on Plan 9: github.com/mholt/archives pulls
// in github.com/spf13/afero (through github.com/bodgit/sevenzip), which uses
// syscall.EBADFD, and Plan 9's syscall package does not define it.

// archiveExtList is empty so that isMountableArchivePath never matches and
// localFS and nestFS don't offer archives as mountable directories.
var archiveExtList []string

var errArchiveUnsupported = fmt.Errorf("archives are not supported on plan9: %w", errors.ErrUnsupported)

func newArchiveFSFromLocalFS(_ context.Context, name string) (*archiveFS, error) {
	return nil, fmt.Errorf("cannot mount %q as archiveFS, %w", name, errArchiveUnsupported)
}

func newArchiveFSFromFile(_ context.Context, file fs.File) (*archiveFS, error) {
	stat, err := file.Stat()
	if err != nil {
		return nil, err
	}
	return nil, fmt.Errorf("cannot mount archive %q, %w", stat.Name(), errArchiveUnsupported)
}
