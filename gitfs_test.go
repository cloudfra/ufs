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

package ufs

import (
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

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
			if _, err := newGitFS(tt.url); err == nil {
				t.Fatalf("newGitFS(%q) = nil error, want error", tt.url)
			}
		})
	}
}

func TestNewGitFSLocalRepo(t *testing.T) {
	srcDir, err := os.MkdirTemp("", "gitfssrc*.git")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(srcDir); err != nil {
			t.Errorf("os.RemoveAll(%q) = %v", srcDir, err)
		}
	}()

	if err := initTestGitRepo(t, srcDir, map[string]string{
		"hello.txt": "hello world",
		"readme.md": "# Test Repo",
	}); err != nil {
		t.Fatalf("initTestGitRepo: %v", err)
	}

	fsys, err := newGitFS(srcDir)
	if err != nil {
		t.Fatalf("newGitFS(%q) = %v, want nil", srcDir, err)
	}
	defer validateClose(t, fsys)()

	f, err := fsys.Open("hello.txt")
	if err != nil {
		t.Fatalf("Open(%q) = %v, want nil", "hello.txt", err)
	}
	defer validateClose(t, f)()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("content = %q, want %q", string(data), "hello world")
	}
}

func TestNewGitFSNoGitDir(t *testing.T) {
	srcDir, err := os.MkdirTemp("", "gitfssrc*.git")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := os.RemoveAll(srcDir); err != nil {
			t.Errorf("os.RemoveAll(%q) = %v", srcDir, err)
		}
	}()

	if err := initTestGitRepo(t, srcDir, map[string]string{
		"hello.txt": "hello",
	}); err != nil {
		t.Fatalf("initTestGitRepo: %v", err)
	}

	fsys, err := newGitFS(srcDir)
	if err != nil {
		t.Fatalf("newGitFS(%q) = %v, want nil", srcDir, err)
	}
	defer validateClose(t, fsys)()

	// .git and .gitignore should be stripped from the cloned result
	if _, err := fsys.Open(".git"); err == nil {
		t.Error("Open(\".git\") succeeded, want error — .git dir should be removed")
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
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	w, err := repo.Worktree()
	if err != nil {
		return err
	}
	if err := w.AddGlob(cwdPath); err != nil {
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
