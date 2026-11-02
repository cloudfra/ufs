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

//go:build !aix && !wasip1

// Package gitfs2 provides a writable ufs file system with the contents of a
// git repository, cloned into a temporary directory when it is opened.
// Importing it registers URIs ending in ".git" with [ufs.New]:
//
//	import _ "github.com/cloudfra/ufs/drivers/internal/gitfs2"
//
// Git metadata is hidden from the file-system view: in the repository root,
// any name beginning with ".git" reads as fs.ErrNotExist and writes as
// fs.ErrPermission. The file system implements [types.SnapshotFS], so its
// contents can be checkpointed and a snapshot's changes committed locally.
//
// The driver is unavailable on GOOS=aix and GOOS=wasip1, where go-git does
// not build; there, these URIs are not matched.
package gitfs2

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/go-git/go-billy/v6"
	"github.com/go-git/go-billy/v6/osfs"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/cache"
	"github.com/go-git/go-git/v6/plumbing/object"
	"github.com/go-git/go-git/v6/storage"
	"github.com/go-git/go-git/v6/storage/filesystem"
)

func init() {
	ufs.Register(ufs.NewDriver("git", New, isGitFSUri, 1, true, true))
}

var (
	_ types.WriteFS    = (*gitFS)(nil)
	_ types.SnapshotFS = (*gitFS)(nil)
)

const (
	gitNamePrefix  = ".git"
	snapshotOption = "snapshot"

	commitMessage = "ufs: write committed changes"
	commitName    = "ufs"
	commitEmail   = "ufs@ufs.invalid"
)

type gitFS struct {
	inner    types.WriteFS
	uri      string
	dir      string
	repo     *git.Repository
	closer   func() error
	parent   *gitFS
	children []*gitFS
	stale    bool
}

func (fsys *gitFS) DeviceInfo() types.DeviceMap {
	return types.DefaultDeviceMap
}

func (fsys *gitFS) URI() (*url.URL, error) {
	u, err := url.Parse(fsys.uri)
	if err != nil {
		return nil, err
	}
	if fsys.parent == nil {
		return u, nil
	}
	query := u.Query()
	query.Set(snapshotOption, "true")
	u.RawQuery = query.Encode()
	return u, nil
}

func (fsys *gitFS) String() string {
	return fmt.Sprintf("gitFS(%s, tmpDir=%s)", ufs.URIOrDefault(fsys, fsys.uri), pathutil.CoerceUnix(fsys.dir))
}

func (fsys *gitFS) Open(name string) (fs.File, error) {
	if err := fsys.checkRead("open", name); err != nil {
		return nil, err
	}
	return fsys.inner.Open(name)
}

func (fsys *gitFS) ReadFile(name string) ([]byte, error) {
	if err := fsys.checkRead("readfile", name); err != nil {
		return nil, err
	}
	return fsys.inner.ReadFile(name)
}

func (fsys *gitFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := fsys.checkRead("readdir", name); err != nil {
		return nil, err
	}
	entries, err := fsys.inner.ReadDir(name)
	if err != nil {
		return nil, err
	}
	if pathutil.IsCwd(name) {
		return hideGitEntries(entries), nil
	}
	return entries, nil
}

func (fsys *gitFS) ReadLink(name string) (string, error) {
	if err := fsys.checkRead("readlink", name); err != nil {
		return "", err
	}
	return fsys.inner.ReadLink(name)
}

func (fsys *gitFS) Stat(name string) (fs.FileInfo, error) {
	if err := fsys.checkRead("stat", name); err != nil {
		return nil, err
	}
	return fsys.inner.Stat(name)
}

func (fsys *gitFS) Lstat(name string) (fs.FileInfo, error) {
	if err := fsys.checkRead("lstat", name); err != nil {
		return nil, err
	}
	return fsys.inner.Lstat(name)
}

func (fsys *gitFS) Create(name string) (types.File, error) {
	if err := fsys.checkWrite("create", name); err != nil {
		return nil, err
	}
	return fsys.inner.Create(name)
}

func (fsys *gitFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := fsys.checkWrite("mkdir", name); err != nil {
		return err
	}
	return fsys.inner.MkdirAll(name, perm)
}

func (fsys *gitFS) Remove(name string) error {
	if err := fsys.checkWrite("remove", name); err != nil {
		return err
	}
	return fsys.inner.Remove(name)
}

func (fsys *gitFS) RemoveAll(name string) error {
	if err := fsys.checkWrite("removeall", name); err != nil {
		return err
	}
	return fsys.inner.RemoveAll(name)
}

func (fsys *gitFS) Close() error {
	errs := []error{fsys.inner.Close()}
	if fsys.closer != nil {
		errs = append(errs, fsys.closer())
	}
	return ufserrors.Join(errs...)
}

func (fsys *gitFS) Checkpoint(ctx context.Context) (types.SnapshotFS, error) {
	if err := checkStale("checkpoint", fsys); err != nil {
		return nil, err
	}

	snapshot := &gitFS{uri: fsys.uri, parent: fsys}
	inner, err := core.NewTempMountFS(ctx, fsys.uri, func(dir string) error {
		snapshot.dir = dir
		return nil
	})
	if err != nil {
		return nil, err
	}
	snapshot.inner = inner

	if err := syncTree(ctx, snapshot, fsys); err != nil {
		return nil, ufserrors.Join(fmt.Errorf("cannot copy %s into a snapshot, %w", fsys, err), inner.Close())
	}

	fsys.children = append(fsys.children, snapshot)
	return snapshot, nil
}

func (fsys *gitFS) CommitChanges(ctx context.Context) error {
	if err := checkStale("commitchanges", fsys); err != nil {
		return err
	}

	if fsys.parent != nil {
		if err := syncTree(ctx, fsys.parent, fsys); err != nil {
			return fmt.Errorf("cannot copy the changes of %s into %s, %w", fsys, fsys.parent, err)
		}
		if err := fsys.parent.CommitChanges(ctx); err != nil {
			return err
		}
	} else if err := fsys.commitRepo(ctx); err != nil {
		return err
	}

	invalidateSiblings(fsys)
	return nil
}

func (fsys *gitFS) commitRepo(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if fsys.repo == nil {
		return fmt.Errorf("cannot commit %s, it has no git repository", fsys)
	}

	worktree, err := fsys.repo.Worktree()
	if err != nil {
		return fmt.Errorf("cannot open the worktree of %s, %w", fsys, err)
	}
	status, err := worktree.Status()
	if err != nil {
		return fmt.Errorf("cannot read the status of %s, %w", fsys, err)
	}
	if status.IsClean() {
		return nil
	}

	for name, file := range status {
		switch file.Worktree {
		case git.Unmodified, git.Added, git.Renamed, git.Copied, git.UpdatedButUnmerged:
			continue
		case git.Deleted:
			if _, err := worktree.Remove(name); err != nil {
				return fmt.Errorf("cannot stage the deletion of %s in %s, %w", name, fsys, err)
			}
		default:
			if _, err := worktree.Add(name); err != nil {
				return fmt.Errorf("cannot stage %s in %s, %w", name, fsys, err)
			}
		}
	}

	_, err = worktree.Commit(commitMessage, &git.CommitOptions{
		Author: &object.Signature{
			Name:  commitName,
			Email: commitEmail,
			When:  time.Now(),
		},
	})
	if err != nil {
		return fmt.Errorf("cannot commit the changes of %s, %w", fsys, err)
	}
	return nil
}

func checkStale(op string, fsys *gitFS) error {
	if fsys.parent == nil || !fsys.stale {
		return nil
	}
	return fmt.Errorf("cannot %s on %s, %s changed since it was snapshotted", op, fsys, fsys.parent)
}

func invalidateSiblings(fsys *gitFS) {
	parent := fsys
	if fsys.parent != nil {
		parent = fsys.parent
	}
	for _, child := range parent.children {
		if child != fsys {
			child.stale = true
		}
	}
}

func (fsys *gitFS) checkRead(op string, name string) error {
	if err := pathutil.Validate(op, name); err != nil {
		return err
	}
	if isGitName(name) {
		return ufserrors.NewPathError(op, name, fs.ErrNotExist)
	}
	return nil
}

func (fsys *gitFS) checkWrite(op string, name string) error {
	if err := pathutil.Validate(op, name); err != nil {
		return err
	}
	if isGitName(name) {
		return ufserrors.NewPathError(op, name, fs.ErrPermission)
	}
	return nil
}

func isGitName(name string) bool {
	if pathutil.IsCwd(name) {
		return false
	}
	first, _, found := strings.Cut(pathutil.CoerceUnix(name), "/")
	if !found {
		first = name
	}
	return strings.HasPrefix(first, gitNamePrefix)
}

func hideGitEntries(entries []fs.DirEntry) []fs.DirEntry {
	kept := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), gitNamePrefix) {
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func syncTree(ctx context.Context, dst types.WriteFS, src types.WriteFS) error {
	if err := copyTree(ctx, dst, src); err != nil {
		return err
	}
	return pruneTree(ctx, dst, src)
}

func copyTree(ctx context.Context, dst types.WriteFS, src types.WriteFS) error {
	return fs.WalkDir(src, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if pathutil.IsCwd(name) {
			return nil
		}
		if entry.IsDir() {
			if _, err := dst.Stat(name); errors.Is(err, fs.ErrNotExist) {
				return dst.MkdirAll(name, fs.ModePerm)
			}
			return nil
		}

		data, err := src.ReadFile(name)
		if err != nil {
			return err
		}
		existing, err := dst.ReadFile(name)
		if err == nil && bytes.Equal(existing, data) {
			return nil
		}
		if err != nil && !errors.Is(err, fs.ErrNotExist) {
			return err
		}

		f, err := dst.Create(name)
		if err != nil {
			return err
		}
		if _, err := f.Write(data); err != nil {
			return ufserrors.Join(err, f.Close())
		}
		return f.Close()
	})
}

func pruneTree(ctx context.Context, dst types.WriteFS, src types.WriteFS) error {
	return fs.WalkDir(dst, ".", func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if pathutil.IsCwd(name) {
			return nil
		}
		if _, err := src.Stat(name); err == nil {
			return nil
		} else if !errors.Is(err, fs.ErrNotExist) {
			return err
		}
		if err := dst.RemoveAll(name); err != nil {
			return err
		}
		if entry.IsDir() {
			return fs.SkipDir
		}
		return nil
	})
}

// New returns a file system with the contents of the git repository name,
// which must end in ".git". The repository is cloned into a temporary
// directory that is removed when the file system is closed; its git storage
// lives in a second temporary directory, so no ".git" entry is ever visible.
func New(ctx context.Context, name string) (types.WriteFS, error) {
	if !isGitFSUri(name) {
		return nil, fmt.Errorf("%q is not a valid git repository", name)
	}

	cloneErrs := []error{}
	for _, opts := range cloneOptions(cloneURL(name)) {
		if err := ctx.Err(); err != nil {
			cloneErrs = append(cloneErrs, err)
			break
		}

		fsys, err := cloneRepo(ctx, name, opts)
		if err != nil {
			cloneErrs = append(cloneErrs, err)
			continue
		}
		return fsys, nil
	}
	return nil, ufserrors.Join(cloneErrs...)
}

func cloneRepo(ctx context.Context, uri string, opts *git.CloneOptions) (types.WriteFS, error) {
	gitDir, gitCloser, err := osutil.CreateTempDirectory()
	if err != nil {
		return nil, ufserrors.Join(err, gitCloser())
	}

	var repo *git.Repository
	var workDir string
	inner, err := core.NewTempMountFS(ctx, uri, func(dir string) error {
		workDir = dir
		cloned, cloneErr := git.CloneContext(ctx, storageDisk(billyDisk(gitDir)), billyDisk(dir), opts)
		if cloneErr != nil {
			return cloneErr
		}
		repo = cloned
		return osutil.RemoveAll(filepath.Join(dir, gitNamePrefix))
	})
	if err != nil {
		return nil, ufserrors.Join(err, gitCloser())
	}

	return &gitFS{
		inner:  inner,
		uri:    uri,
		dir:    workDir,
		repo:   repo,
		closer: gitCloser,
	}, nil
}

func billyDisk(dir string) billy.Filesystem {
	return osfs.New(dir, osfs.WithBoundOS())
}

func storageDisk(bFS billy.Filesystem) storage.Storer {
	return filesystem.NewStorageWithOptions(bFS, cache.NewObjectLRUDefault(), filesystem.Options{})
}

// cloneURL returns the address handed to go-git. go-git v6 does not
// percent-decode the path of a file:// endpoint, so a local repository whose
// path holds a space or a "#" would not be found; the decoded path is used
// instead, and other URIs are passed through unchanged.
func cloneURL(uri string) string {
	u, err := url.Parse(uri)
	if err != nil || u.Scheme != "file" || u.Host != "" {
		return uri
	}
	return u.Path
}

func cloneOptions(remoteURL string) []*git.CloneOptions {
	return []*git.CloneOptions{
		{
			URL:          remoteURL,
			Depth:        1,
			SingleBranch: true,
		},
		{
			URL:           remoteURL,
			Depth:         1,
			SingleBranch:  true,
			ReferenceName: plumbing.NewBranchReferenceName("main"),
		},
	}
}

func isGitFSUri(uri string) bool {
	return strings.HasSuffix(strings.ToLower(uri), ".git")
}
