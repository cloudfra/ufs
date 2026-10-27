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

package gitfs

import (
	"context"
	"errors"
	"io"
	"path/filepath"
	"strings"
	"testing"
	"time"

	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
	"github.com/go-git/go-git/v5"
	"github.com/go-git/go-git/v5/plumbing"
	"github.com/go-git/go-git/v5/plumbing/object"
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

func TestNewGitFSInvalid(t *testing.T) {
	tests := []struct {
		name string
		url  string
	}{
		{"unsupported_url", "https://github.com/foo/bar"},
		{"invalid_url", "https://invalid.nonexistent.example/repo.git"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := New(t.Context(), tt.url); err == nil {
				t.Fatalf("New(%q) = nil error, want error", tt.url)
			}
		})
	}
}

func TestNewGitFSLocalRepo(t *testing.T) {
	srcDir, err := osutil.MkdirTemp("", "gitfssrc*.git")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := osutil.RemoveAll(srcDir); err != nil {
			t.Errorf("osutil.RemoveAll(%q) = %v", srcDir, err)
		}
	}()

	if err := initTestGitRepo(t, srcDir, map[string]string{
		"hello.txt": "hello world",
		"readme.md": "# Test Repo",
	}); err != nil {
		t.Fatalf("initTestGitRepo: %v", err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatalf("New(%q) = %v, want nil", srcDir, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	f, err := fsys.Open("hello.txt")
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", "hello.txt", err)
	}
	defer ufsTesting.ValidateClose(t, f)()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", string(data), "hello world")
	}
}

func TestNewGitFSNoGitDir(t *testing.T) {
	srcDir, err := osutil.MkdirTemp("", "gitfssrc*.git")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := osutil.RemoveAll(srcDir); err != nil {
			t.Errorf("osutil.RemoveAll(%q) = %v", srcDir, err)
		}
	}()

	if err := initTestGitRepo(t, srcDir, map[string]string{
		"hello.txt": "hello",
	}); err != nil {
		t.Fatalf("initTestGitRepo: %v", err)
	}

	fsys, err := New(t.Context(), srcDir)
	if err != nil {
		t.Fatalf("New(%q) = %v, want nil", srcDir, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	// .git and .gitignore should be stripped from the cloned result
	if _, err := fsys.Open(".git"); err == nil {
		t.Error("Open(\".git\") succeeded, want error — .git dir should be removed")
	}
}

// TestNewViaUFSNew checks that importing the package registers the driver,
// so ufs.New opens a ".git" URI with it.
func TestNewViaUFSNew(t *testing.T) {
	srcDir := filepath.Join(t.TempDir(), "src.git")
	want := map[string]string{"hello.txt": "via ufs.New"}
	if err := initTestGitRepo(t, srcDir, want); err != nil {
		t.Fatalf("initTestGitRepo: %v", err)
	}
	ufsdriversTesting.OpenWithNew(t, srcDir, want)
}

// TestPrepareGitFSInterruptedRetry checks that a retry starts from a clean
// target. An interrupted attempt leaves a .git directory behind, which makes
// go-git refuse the clone, so without a reset between attempts the retry fails
// on the leftovers of the attempt it was meant to recover from.
func TestPrepareGitFSInterruptedRetry(t *testing.T) {
	repoDir := filepath.Join(t.TempDir(), "repo.git")
	if err := initTestGitRepo(t, repoDir, map[string]string{"hello.txt": "retry"}); err != nil {
		t.Fatal(err)
	}
	// The second set of clone options asks for the main branch, so the
	// repository has to carry one.
	repo, err := git.PlainOpen(repoDir)
	if err != nil {
		t.Fatal(err)
	}
	w, err := repo.Worktree()
	if err != nil {
		t.Fatal(err)
	}
	if err := w.Checkout(&git.CheckoutOptions{
		Branch: plumbing.NewBranchReferenceName("main"),
		Create: true,
	}); err != nil {
		t.Fatal(err)
	}

	// Leftovers of an attempt that did not finish: a repository go-git refuses
	// to clone into, plus a file that is not part of the clone.
	target := t.TempDir()
	if _, err := git.PlainInit(target, false); err != nil {
		t.Fatal(err)
	}
	if err := osutil.WriteFile(filepath.Join(target, "leftover.txt"), []byte("stale")); err != nil {
		t.Fatal(err)
	}

	if err := prepareGitDirectory(t.Context(), target, repoDir); err != nil {
		t.Fatalf("prepareGitDirectory() = %v, want the retry to succeed", err)
	}

	data, err := osutil.ReadFile(filepath.Join(target, "hello.txt"))
	if err != nil {
		t.Fatalf("reading hello.txt after the retry: %v", err)
	}
	if string(data) != "retry" {
		t.Errorf("hello.txt = %q, want %q", string(data), "retry")
	}
	if osutil.Exists(filepath.Join(target, "leftover.txt")) {
		t.Error("leftover.txt survived the retry, want the target reset before cloning")
	}
}

// initTestGitRepo creates a git repo at dir with the given files committed.
func initTestGitRepo(t *testing.T, dir string, files map[string]string) error {
	t.Helper()
	repo, err := git.PlainInit(dir, false)
	if err != nil {
		return err
	}
	for name, content := range files {
		if err := osutil.WriteFile(filepath.Join(dir, name), []byte(content)); err != nil {
			return err
		}
	}
	w, err := repo.Worktree()
	if err != nil {
		return err
	}
	if err := w.AddGlob(pathutil.CwdPath); err != nil {
		return err
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

// TestNewGitFSCanceledContext checks that the clone observes the context: a
// repository that would clone fine is not opened once the context is done.
func TestNewGitFSCanceledContext(t *testing.T) {
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

// TestNewGitFSErrorNamesRepository checks that a failed clone reports the
// repository it could not clone, with the error of every attempt.
func TestNewGitFSErrorNamesRepository(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "missing.git")
	_, err := New(t.Context(), missing)
	if err == nil {
		t.Fatal("New() = nil error, want an error for a missing repository")
	}
	if !strings.Contains(err.Error(), "could not clone "+missing) {
		t.Errorf("New() = %q, want it to name the repository %q", err, missing)
	}
}
