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

package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRunLocalDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	for _, name := range []string{"a.txt", "b.txt"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("data"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(dir); err != nil {
		t.Fatalf("run(%q) = %v, want nil", dir, err)
	}
}

func TestRunLocalDirNested(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	sub := filepath.Join(dir, "sub")
	if err := os.MkdirAll(sub, 0o755); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"root.txt", filepath.Join("sub", "nested.txt")} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("content"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := run(dir); err != nil {
		t.Fatalf("run(%q) = %v, want nil", dir, err)
	}
}

func TestRunEmptyDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := run(dir); err != nil {
		t.Fatalf("run(%q) = %v, want nil", dir, err)
	}
}

func TestRunNullFS(t *testing.T) {
	t.Parallel()
	if err := run("null://"); err != nil {
		t.Fatalf("run(null://) = %v, want nil", err)
	}
}

func TestRunInvalidPath(t *testing.T) {
	t.Parallel()
	if err := run("/no/such/directory/exists"); err == nil {
		t.Fatal("run(nonexistent) = nil, want error")
	}
}
