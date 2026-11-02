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

package gitfs2

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ufsDriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/go-git/go-git/v6"
	"github.com/go-git/go-git/v6/plumbing"
	"github.com/go-git/go-git/v6/plumbing/object"
)

func TestIsGitFSUri(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"repo.git", true},
		{"REPO.GIT", true},
		{"Repo.Git", true},
		{".git", true},
		{"dir/.git", true},
		{"https://github.com/foo/bar.git", true},
		{"file.txt", false},
		{"file", false},
		{"", false},
		{"https://github.com/foo/bar", false},
		{"git.txt", false},
	}
	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			got := isGitFSUri(tt.path)
			if got != tt.want {
				t.Errorf("isGitFSUri(%q) = %v, want %v", tt.path, got, tt.want)
			}
		})
	}
}

func TestCloneOptions(t *testing.T) {
	const url = "https://example.com/repo.git"
	opts := cloneOptions(url)
	if len(opts) != 2 {
		t.Fatalf("cloneOptions() returned %d options, want 2", len(opts))
	}
	for i, opt := range opts {
		if opt.URL != url {
			t.Errorf("opts[%d].URL = %q, want %q", i, opt.URL, url)
		}
		if opt.Depth != 1 {
			t.Errorf("opts[%d].Depth = %d, want 1", i, opt.Depth)
		}
		if !opt.SingleBranch {
			t.Errorf("opts[%d].SingleBranch = false, want true", i)
		}
	}
	if opts[0].ReferenceName != "" {
		t.Errorf("opts[0].ReferenceName = %q, want empty", opts[0].ReferenceName)
	}
	if opts[1].ReferenceName != plumbing.NewBranchReferenceName("main") {
		t.Errorf("opts[1].ReferenceName = %q, want %q", opts[1].ReferenceName, plumbing.NewBranchReferenceName("main"))
	}
}

func TestCloneURL(t *testing.T) {
	tests := []struct {
		uri  string
		want string
	}{
		{"/tmp/repo.git", "/tmp/repo.git"},
		{"https://example.com/repo.git", "https://example.com/repo.git"},
		{"file://example.com/tmp/repo.git", "file://example.com/tmp/repo.git"},
		{"file:///tmp/repo.git", "/tmp/repo.git"},
		{"file:///tmp/a%20b/repo.git", "/tmp/a b/repo.git"},
		{"file:///tmp/a%23b/repo.git", "/tmp/a#b/repo.git"},
	}
	for _, test := range tests {
		if got := cloneURL(test.uri); got != test.want {
			t.Errorf("cloneURL(%q) = %q, want %q", test.uri, got, test.want)
		}
	}
}

func TestIsGitName(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{".", false},
		{"", false},
		{"hello.txt", false},
		{".hidden", false},
		{".git", true},
		{".gitignore", true},
		{".gitmodules", true},
		{".gitattributes", true},
		{".gitkeep", true},
		{".github", true},
		{".git/config", true},
		{".git/hooks/pre-commit", true},
		{"gitignore", false},
		{"sub/.gitignore", false},
		{"sub/.git/config", false},
		{"sub\\", false},
		{"sub\\.gitignore", false},
		{".gitignore\\x", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := isGitName(tt.name); got != tt.want {
				t.Errorf("isGitName(%q) = %v, want %v", tt.name, got, tt.want)
			}
		})
	}
}

func TestHideGitEntries(t *testing.T) {
	entries := []fs.DirEntry{
		fakeDirEntry("hello.txt"),
		fakeDirEntry(".git"),
		fakeDirEntry(".gitignore"),
		fakeDirEntry(".gitmodules"),
		fakeDirEntry(".hidden"),
		fakeDirEntry("sub"),
	}
	got := hideGitEntries(entries)
	want := []string{"hello.txt", ".hidden", "sub"}
	if len(got) != len(want) {
		t.Fatalf("hideGitEntries() = %v, want %v", entryNames(got), want)
	}
	for i, entry := range got {
		if entry.Name() != want[i] {
			t.Errorf("hideGitEntries()[%d] = %q, want %q", i, entry.Name(), want[i])
		}
	}
}

func TestNewInvalid(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"unsupported_url", "https://github.com/foo/bar"},
		{"missing_repo", filepath.Join(t.TempDir(), "missing.git")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(t.Context(), tt.url); err == nil {
				t.Fatalf("New(%q) = nil error, want error", tt.url)
			}
		})
	}
}

func TestNewCanceledContext(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo.git")
	if err := initTestGitRepo(t, repoDir, map[string]string{"hello.txt": "hello"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	fsys, err := New(ctx, repoDir)
	if err == nil {
		ufsTesting.ValidateClose(t, fsys)()
		t.Fatal("New() = nil error, want an error for a canceled context")
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("New() = %v, want an error that wraps context.Canceled", err)
	}
}

func TestNewLocalRepo(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo.git")
	if err := initTestGitRepo(t, repoDir, map[string]string{
		"hello.txt":  "hello world",
		"readme.md":  "# Test Repo",
		".gitignore": "secret",
	}); err != nil {
		t.Fatalf("initTestGitRepo: %v", err)
	}

	fsys := mustNewGitFS(t, repoDir)

	if got, err := fsys.ReadFile("hello.txt"); err != nil || string(got) != "hello world" {
		t.Errorf(`ReadFile("hello.txt") = %q, %v, want "hello world", nil`, got, err)
	}
	if _, err := osutil.Stat(filepath.Join(fsys.dir, "hello.txt")); err != nil {
		t.Errorf("hello.txt in the worktree: %v, want the worktree checked out", err)
	}
	if _, err := osutil.Stat(filepath.Join(fsys.dir, ".git")); err == nil {
		t.Error(`the worktree contains ".git", want the git storage outside the exposed tree`)
	}
	if _, err := osutil.Stat(filepath.Join(fsys.dir, ".gitignore")); err != nil {
		t.Errorf(`the worktree is missing ".gitignore", %v, want the committed file checked out`, err)
	}
}

func TestNewLocalRepoEncodedPath(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "a b#c", "repo.git")
	if err := initTestGitRepo(t, repoDir, map[string]string{"hello.txt": "hello world"}); err != nil {
		t.Fatalf("initTestGitRepo: %v", err)
	}

	fsys := mustNewGitFS(t, fileURI(repoDir))
	if got, err := fsys.ReadFile("hello.txt"); err != nil || string(got) != "hello world" {
		t.Errorf(`ReadFile("hello.txt") = %q, %v, want "hello world", nil`, got, err)
	}
}

func TestHiddenGitNamesReads(t *testing.T) {
	fsys := mustNewGitFS(t, mustSeedRepo(t, map[string]string{
		"hello.txt":      "hello world",
		".gitignore":     "secret",
		".gitmodules":    "[submodule]",
		".gitattributes": "* text=auto",
	}))

	names := []string{".git", ".gitignore", ".gitmodules", ".gitattributes", ".gitkeep", ".git/config"}
	for _, name := range names {
		if _, err := fsys.Stat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf(`Stat(%q) = %v, want an error that wraps fs.ErrNotExist`, name, err)
		}
		if _, err := fsys.Lstat(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf(`Lstat(%q) = %v, want an error that wraps fs.ErrNotExist`, name, err)
		}
		if f, err := fsys.Open(name); err == nil {
			if closeErr := f.Close(); closeErr != nil {
				t.Errorf(`Open(%q).Close() = %v, want nil`, name, closeErr)
			}
			t.Errorf(`Open(%q) = nil error, want fs.ErrNotExist`, name)
		}
		if _, err := fsys.ReadFile(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf(`ReadFile(%q) = %v, want an error that wraps fs.ErrNotExist`, name, err)
		}
		if _, err := fsys.ReadDir(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf(`ReadDir(%q) = %v, want an error that wraps fs.ErrNotExist`, name, err)
		}
		if _, err := fsys.ReadLink(name); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf(`ReadLink(%q) = %v, want an error that wraps fs.ErrNotExist`, name, err)
		}
	}
}

func TestHiddenGitNamesWrites(t *testing.T) {
	fsys := mustNewGitFS(t, mustSeedRepo(t, map[string]string{
		"hello.txt":   "hello world",
		".gitignore":  "secret",
		".gitmodules": "[submodule \"sub\"]\npath = sub\n",
	}))

	names := []string{".git", ".gitignore", ".gitmodules", ".gitattributes", ".gitfoo", ".git/config", ".gitignore/x"}
	for _, name := range names {
		if f, err := fsys.Create(name); err == nil {
			if closeErr := f.Close(); closeErr != nil {
				t.Errorf(`Create(%q).Close() = %v, want nil`, name, closeErr)
			}
			t.Errorf(`Create(%q) = nil error, want fs.ErrPermission`, name)
		} else if !errors.Is(err, fs.ErrPermission) {
			t.Errorf(`Create(%q) = %v, want an error that wraps fs.ErrPermission`, name, err)
		}
		if err := fsys.MkdirAll(name, fs.ModePerm); !errors.Is(err, fs.ErrPermission) {
			t.Errorf(`MkdirAll(%q) = %v, want an error that wraps fs.ErrPermission`, name, err)
		}
		if err := fsys.Remove(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf(`Remove(%q) = %v, want an error that wraps fs.ErrPermission`, name, err)
		}
		if err := fsys.RemoveAll(name); !errors.Is(err, fs.ErrPermission) {
			t.Errorf(`RemoveAll(%q) = %v, want an error that wraps fs.ErrPermission`, name, err)
		}
	}

	if got, err := fsys.ReadFile(".gitignore"); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`ReadFile(".gitignore") after the failed writes = %q, %v, want the hidden file untouched`, got, err)
	}
}

func TestHiddenGitNamesReadDir(t *testing.T) {
	repoDir := mustSeedRepo(t, map[string]string{
		"hello.txt":      "hello world",
		".gitignore":     "secret",
		".gitmodules":    "[submodule]",
		".gitattributes": "* text=auto",
		".hidden":        "still visible",
		"sub/.gitignore": "nested",
		"sub/file.txt":   "nested file",
	})
	fsys := mustNewGitFS(t, repoDir)

	got, err := fsys.ReadDir(".")
	if err != nil {
		t.Fatalf(`ReadDir(".") = %v, want nil`, err)
	}
	want := []string{".hidden", "hello.txt", "sub"}
	names := entryNames(got)
	if strings.Join(names, " ") != strings.Join(want, " ") {
		t.Errorf(`ReadDir(".") = %v, want %v`, names, want)
	}

	for _, name := range want {
		if _, err := fsys.Stat(name); err != nil {
			t.Errorf(`Stat(%q) = %v, want nil`, name, err)
		}
	}

	subEntries, err := fsys.ReadDir("sub")
	if err != nil {
		t.Fatalf(`ReadDir("sub") = %v, want nil`, err)
	}
	subNames := entryNames(subEntries)
	wantSub := []string{".gitignore", "file.txt"}
	if strings.Join(subNames, " ") != strings.Join(wantSub, " ") {
		t.Errorf(`ReadDir("sub") = %v, want %v`, subNames, wantSub)
	}

	if got, err := fsys.ReadFile("sub/.gitignore"); err != nil || string(got) != "nested" {
		t.Errorf(`ReadFile("sub/.gitignore") = %q, %v, want "nested", nil`, got, err)
	}
	if f, err := fsys.Create("sub/.gitignore"); err != nil {
		t.Errorf(`Create("sub/.gitignore") = %v, want nil`, err)
	} else if err := f.Close(); err != nil {
		t.Errorf(`Create("sub/.gitignore").Close() = %v, want nil`, err)
	}
}

func TestCommitChanges(t *testing.T) {
	repoDir := mustSeedRepo(t, map[string]string{
		"hello.txt": "hello world",
		"readme.md": "# Test Repo",
	})
	fsys := mustNewGitFS(t, repoDir)
	before := headHash(t, fsys.repo)

	writeTestFile(t, fsys, "new.txt", "new content")
	if err := fsys.MkdirAll("sub/deep", fs.ModePerm); err != nil {
		t.Fatalf(`MkdirAll("sub/deep") = %v, want nil`, err)
	}
	writeTestFile(t, fsys, "sub/deep/nested.txt", "nested")
	if err := fsys.Remove("readme.md"); err != nil {
		t.Fatalf(`Remove("readme.md") = %v, want nil`, err)
	}

	if err := fsys.CommitChanges(t.Context()); err != nil {
		t.Fatalf("CommitChanges() = %v, want nil", err)
	}

	after := headHash(t, fsys.repo)
	if after == before {
		t.Fatal("CommitChanges() did not advance HEAD")
	}
	commit, err := fsys.repo.CommitObject(after)
	if err != nil {
		t.Fatalf("CommitObject(%s) = %v, want nil", after, err)
	}
	if commit.Message != strings.TrimSpace(commitMessage) {
		t.Errorf("commit message = %q, want %q", commit.Message, strings.TrimSpace(commitMessage))
	}
	for name, want := range map[string]string{
		"new.txt":             "new content",
		"sub/deep/nested.txt": "nested",
		"hello.txt":           "hello world",
	} {
		got, err := commitFile(t, commit, name)
		if err != nil {
			t.Errorf("the commit does not contain %q, %v", name, err)
			continue
		}
		if got != want {
			t.Errorf("the committed %q = %q, want %q", name, got, want)
		}
	}
	if _, err := commitFile(t, commit, "readme.md"); err == nil {
		t.Error(`the commit still contains "readme.md", want the deletion committed`)
	}

	headAfterFirst := after
	if err := fsys.CommitChanges(t.Context()); err != nil {
		t.Fatalf("CommitChanges() with no changes = %v, want nil", err)
	}
	if got := headHash(t, fsys.repo); got != headAfterFirst {
		t.Errorf("CommitChanges() with no changes advanced HEAD from %s to %s, want no commit", headAfterFirst, got)
	}
}

func TestCommitChangesHidesGitNames(t *testing.T) {
	repoDir := mustSeedRepo(t, map[string]string{
		"hello.txt":      "hello world",
		".gitignore":     "secret",
		"sub/.gitignore": "nested",
	})
	fsys := mustNewGitFS(t, repoDir)
	before := headHash(t, fsys.repo)

	writeTestFile(t, fsys, "tracked.txt", "tracked")
	if got, err := fsys.ReadFile(".gitignore"); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`ReadFile(".gitignore") = %q, %v, want fs.ErrNotExist`, got, err)
	}

	if err := fsys.CommitChanges(t.Context()); err != nil {
		t.Fatalf("CommitChanges() = %v, want nil", err)
	}
	after := headHash(t, fsys.repo)
	if after == before {
		t.Fatal("CommitChanges() did not advance HEAD")
	}
	commit, err := fsys.repo.CommitObject(after)
	if err != nil {
		t.Fatalf("CommitObject(%s) = %v, want nil", after, err)
	}
	if got, err := commitFile(t, commit, "tracked.txt"); err != nil || got != "tracked" {
		t.Errorf(`the commit does not contain "tracked.txt": %q, %v`, got, err)
	}
}

func TestSnapshot(t *testing.T) {
	repoDir := mustSeedRepo(t, map[string]string{
		"hello.txt": "hello world",
		"readme.md": "# Test Repo",
	})
	fsys := mustNewGitFS(t, repoDir)
	rootHash := headHash(t, fsys.repo)

	snapshotFS, err := fsys.Checkpoint(t.Context())
	if err != nil {
		t.Fatalf("Checkpoint() = %v, want nil", err)
	}
	snapshot, ok := snapshotFS.(*gitFS)
	if !ok {
		t.Fatalf("Checkpoint() = %T, want *gitFS", snapshotFS)
	}
	if snapshot.dir == fsys.dir {
		t.Errorf("the snapshot worktree = %q, want a directory other than %q", snapshot.dir, fsys.dir)
	}

	if got, err := snapshot.ReadFile("hello.txt"); err != nil || string(got) != "hello world" {
		t.Errorf(`snapshot.ReadFile("hello.txt") = %q, %v, want "hello world", nil`, got, err)
	}

	writeTestFile(t, snapshot, "snapshot.txt", "written in the snapshot")
	if err := snapshot.Remove("readme.md"); err != nil {
		t.Fatalf(`snapshot.Remove("readme.md") = %v, want nil`, err)
	}

	if _, err := fsys.Stat("snapshot.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`parent Stat("snapshot.txt") before CommitChanges = %v, want fs.ErrNotExist`, err)
	}
	if _, err := fsys.Stat("readme.md"); err != nil {
		t.Errorf(`parent Stat("readme.md") before CommitChanges = %v, want the file still there`, err)
	}

	if _, err := snapshot.Create(".gitignore"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf(`snapshot.Create(".gitignore") = %v, want fs.ErrPermission`, err)
	}
	if _, err := snapshot.Stat(".git"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`snapshot.Stat(".git") = %v, want fs.ErrNotExist`, err)
	}

	if err := snapshot.CommitChanges(t.Context()); err != nil {
		t.Fatalf("snapshot.CommitChanges() = %v, want nil", err)
	}

	if got, err := fsys.ReadFile("snapshot.txt"); err != nil || string(got) != "written in the snapshot" {
		t.Errorf(`parent ReadFile("snapshot.txt") after CommitChanges = %q, %v, want the snapshot change`, got, err)
	}
	if _, err := fsys.Stat("readme.md"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`parent Stat("readme.md") after CommitChanges = %v, want fs.ErrNotExist`, err)
	}

	commitHash := headHash(t, fsys.repo)
	if commitHash == rootHash {
		t.Fatal("CommitChanges() on a snapshot did not advance HEAD")
	}
	commit, err := fsys.repo.CommitObject(commitHash)
	if err != nil {
		t.Fatalf("CommitObject(%s) = %v, want nil", commitHash, err)
	}
	if got, err := commitFile(t, commit, "snapshot.txt"); err != nil || got != "written in the snapshot" {
		t.Errorf(`the commit does not contain "snapshot.txt": %q, %v`, got, err)
	}
	if _, err := commitFile(t, commit, "readme.md"); err == nil {
		t.Error(`the commit still contains "readme.md", want the deletion committed`)
	}
}

func TestSnapshotIsolation(t *testing.T) {
	repoDir := mustSeedRepo(t, map[string]string{"hello.txt": "hello world"})
	fsys := mustNewGitFS(t, repoDir)

	snapshotFS, err := fsys.Checkpoint(t.Context())
	if err != nil {
		t.Fatalf("Checkpoint() = %v, want nil", err)
	}
	snapshot := snapshotFS.(*gitFS)

	writeTestFile(t, snapshot, "only-in-snapshot.txt", "snapshot")
	if _, err := fsys.Stat("only-in-snapshot.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`parent Stat("only-in-snapshot.txt") = %v, want fs.ErrNotExist`, err)
	}
	writeTestFile(t, fsys, "only-in-parent.txt", "parent")

	if got, err := snapshot.ReadFile("only-in-parent.txt"); err == nil || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`snapshot.ReadFile("only-in-parent.txt") = %q, %v, want fs.ErrNotExist`, got, err)
	}
	if got, err := fsys.ReadFile("hello.txt"); err != nil || string(got) != "hello world" {
		t.Errorf(`parent ReadFile("hello.txt") = %q, %v, want "hello world", nil`, got, err)
	}
	writeTestFile(t, snapshot, "hello.txt", "changed in the snapshot")
	if got, err := fsys.ReadFile("hello.txt"); err != nil || string(got) != "hello world" {
		t.Errorf(`parent ReadFile("hello.txt") after the snapshot write = %q, %v, want "hello world", nil`, got, err)
	}
}

func TestSnapshotInvalidatesSiblings(t *testing.T) {
	repoDir := mustSeedRepo(t, map[string]string{"hello.txt": "hello world"})
	fsys := mustNewGitFS(t, repoDir)

	firstFS, err := fsys.Checkpoint(t.Context())
	if err != nil {
		t.Fatalf("Checkpoint() = %v, want nil", err)
	}
	secondFS, err := fsys.Checkpoint(t.Context())
	if err != nil {
		t.Fatalf("Checkpoint() = %v, want nil", err)
	}
	first := firstFS.(*gitFS)
	second := secondFS.(*gitFS)

	writeTestFile(t, first, "first.txt", "first")
	writeTestFile(t, second, "second.txt", "second")

	if err := first.CommitChanges(t.Context()); err != nil {
		t.Fatalf("first.CommitChanges() = %v, want nil", err)
	}
	if _, err := fsys.ReadFile("first.txt"); err != nil || string(mustRead(t, fsys, "first.txt")) != "first" {
		t.Errorf(`parent ReadFile("first.txt") = %v, want the first snapshot committed`, err)
	}

	if err := second.CommitChanges(t.Context()); err == nil {
		t.Error("second.CommitChanges() = nil error, want an error after a sibling snapshot committed")
	}
	if _, err := second.Checkpoint(t.Context()); err == nil {
		t.Error("second.Checkpoint() = nil error, want an error after a sibling snapshot committed")
	}
	if _, err := fsys.Stat("second.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf(`parent Stat("second.txt") = %v, want fs.ErrNotExist`, err)
	}
}

func TestCheckpointCanceledContext(t *testing.T) {
	fsys := mustNewGitFS(t, mustSeedRepo(t, map[string]string{"hello.txt": "hello world"}))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if _, err := fsys.Checkpoint(ctx); err == nil {
		t.Error("Checkpoint() = nil error, want an error for a canceled context")
	}
}

func TestURI(t *testing.T) {
	repoURI := fileURI(mustSeedRepo(t, map[string]string{"hello.txt": "hello world"}))
	fsys := mustNewGitFS(t, repoURI)

	u, err := fsys.URI()
	if err != nil {
		t.Fatalf("URI() = %v, want nil", err)
	}
	if u.String() != repoURI {
		t.Errorf("URI() = %q, want %q", u.String(), repoURI)
	}
	if got := fsys.String(); !strings.Contains(got, repoURI) {
		t.Errorf("String() = %q, want it to contain %q", got, repoURI)
	}

	snapshotFS, err := fsys.Checkpoint(t.Context())
	if err != nil {
		t.Fatalf("Checkpoint() = %v, want nil", err)
	}
	snapshot := snapshotFS.(*gitFS)
	snapURI, err := snapshot.URI()
	if err != nil {
		t.Fatalf("snapshot.URI() = %v, want nil", err)
	}
	if snapURI.Query().Get(snapshotOption) != "true" {
		t.Errorf("snapshot.URI() = %q, want it to report %s=true", snapURI, snapshotOption)
	}
	if got := snapshot.String(); !strings.Contains(got, repoURI) {
		t.Errorf("snapshot.String() = %q, want it to contain %q", got, repoURI)
	}
}

func TestGitFS2Driver(t *testing.T) {
	ufsDriversTesting.WriteFSWithBuckets(t, func(t *testing.T) types.WriteFS {
		repoURI := fileURI(mustSeedRepo(t, map[string]string{
			"hello.txt": "hello world",
			"readme.md": "# Test Repo",
		}))
		fsys, err := New(t.Context(), repoURI)
		if err != nil {
			t.Fatalf("New(%q) = %v, want nil", repoURI, err)
		}
		return fsys
	})
}

func fileURI(path string) string {
	return (&url.URL{Scheme: "file", Path: "/" + strings.TrimPrefix(filepath.ToSlash(path), "/")}).String()
}

func mustSeedRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	repoDir := filepath.Join(t.TempDir(), "repo.git")
	if err := initTestGitRepo(t, repoDir, files); err != nil {
		t.Fatalf("initTestGitRepo: %v", err)
	}
	return repoDir
}

func mustNewGitFS(t *testing.T, repoURI string) *gitFS {
	t.Helper()
	fsys, err := New(t.Context(), repoURI)
	if err != nil {
		t.Fatalf("New(%q) = %v, want nil", repoURI, err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	gfsys, ok := fsys.(*gitFS)
	if !ok {
		t.Fatalf("New(%q) = %T, want *gitFS", repoURI, fsys)
	}
	return gfsys
}

func writeTestFile(t *testing.T, fsys types.WriteFS, name string, content string) {
	t.Helper()
	f, err := fsys.Create(name)
	if err != nil {
		t.Fatalf("Create(%q) = %v, want nil", name, err)
	}
	if _, err := f.Write([]byte(content)); err != nil {
		t.Fatalf("Create(%q).Write() = %v, want nil", name, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Create(%q).Close() = %v, want nil", name, err)
	}
}

func mustRead(t *testing.T, fsys types.WriteFS, name string) []byte {
	t.Helper()
	data, err := fsys.ReadFile(name)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v, want nil", name, err)
	}
	return data
}

func headHash(t *testing.T, repo *git.Repository) plumbing.Hash {
	t.Helper()
	ref, err := repo.Head()
	if err != nil {
		t.Fatalf("Head() = %v, want nil", err)
	}
	return ref.Hash()
}

func commitFile(t *testing.T, commit *object.Commit, name string) (string, error) {
	t.Helper()
	file, err := commit.File(name)
	if err != nil {
		return "", err
	}
	reader, err := file.Reader()
	if err != nil {
		return "", err
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	if err := reader.Close(); err != nil {
		return "", err
	}
	return string(data), nil
}

func entryNames(entries []fs.DirEntry) []string {
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

type fakeDirEntry string

func (entry fakeDirEntry) Name() string               { return string(entry) }
func (entry fakeDirEntry) IsDir() bool                { return false }
func (entry fakeDirEntry) Type() fs.FileMode          { return 0 }
func (entry fakeDirEntry) Info() (fs.FileInfo, error) { return nil, fs.ErrNotExist }

func initTestGitRepo(t *testing.T, dir string, files map[string]string) error {
	t.Helper()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return err
	}
	names := make([]string, 0, len(files))
	for name, content := range files {
		if err := osutil.MkdirAll(filepath.Dir(filepath.Join(dir, name))); err != nil {
			return err
		}
		if err := osutil.WriteFile(filepath.Join(dir, name), []byte(content)); err != nil {
			return err
		}
		names = append(names, name)
	}
	w, err := repo.Worktree()
	if err != nil {
		return err
	}
	for _, name := range names {
		if _, err := w.Add(name); err != nil {
			return err
		}
	}
	_, err = w.Commit("init", &git.CommitOptions{
		Author: &object.Signature{
			Name:  "Test",
			Email: "test@test.com",
			When:  time.Now(),
		},
	})
	return err
}
