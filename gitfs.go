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

//go:build !aix
// +build !aix

package ufs

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
)

func init() {
	Register(NewDriver("git", newGitFS, isGitFSUri, 1, true, true))
}

func prepareGitDirectory(name string, gitURL string) error {
	var err error
	for _, opts := range cloneOptions(gitURL) {
		if _, err = git.PlainClone(name, false, opts); err == nil {
			break
		}
	}
	if err != nil {
		return fmt.Errorf("could not clone %s, %w", name, err)
	}

	osutil.TryDeleteDirectory(filepath.Join(name, ".git"))
	osutil.TryDeleteFile(filepath.Join(name, ".gitignore"))
	osutil.TryDeleteFile(filepath.Join(name, ".gitmodules"))
	return nil
}

func newGitFS(ctx context.Context, name string) (FS, error) {
	if !isGitFSUri(name) {
		return nil, fmt.Errorf("%q is not a valid git repository", name)
	}

	return newTempMountFS(ctx, name, func(tempDir string) error {
		return prepareGitDirectory(tempDir, name)
	})
}

func cloneOptions(filePath string) []*git.CloneOptions {
	return []*git.CloneOptions{
		{
			URL:          filePath,
			Progress:     os.Stdout,
			Depth:        1,
			SingleBranch: true,
		},
		{
			URL:           filePath,
			Progress:      os.Stdout,
			Depth:         1,
			SingleBranch:  true,
			ReferenceName: plumbing.NewBranchReferenceName("main"),
		},
	}
}

func isGitFSUri(uri string) bool {
	return strings.HasSuffix(strings.ToLower(uri), ".git")
}
