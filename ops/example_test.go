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

package ops_test

import (
	"context"
	"fmt"
	"io/fs"
	"log/slog"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/ops"
)

// ExampleCopy shows copying a single file between two file systems.
func ExampleCopy() {
	ctx := context.Background()
	src, err := ufs.New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create source FS", "error", err)
		return
	}
	dst, err := ufs.New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create destination FS", "error", err)
		return
	}
	defer func() {
		if err := src.Close(); err != nil {
			slog.Error("failed to close source FS", "error", err)
			return
		}
	}()
	defer func() {
		if err := dst.Close(); err != nil {
			slog.Error("failed to close destination FS", "error", err)
			return
		}
	}()

	f, err := src.Create("hello.txt")
	if err != nil {
		slog.Error("failed to create file in source FS", "error", err)
		return
	}

	if _, err = f.WriteString("hello"); err != nil {
		slog.Error("failed to write to file in source FS", "error", err)
		return
	}
	if err := f.Close(); err != nil {
		slog.Error("failed to close file in source FS", "error", err)
		return
	}

	if err := ops.Copy(src, "hello.txt", dst, "copy.txt"); err != nil {
		slog.Error("failed to copy file", "error", err)
		return
	}

	data, err := dst.ReadFile("copy.txt")
	if err != nil {
		slog.Error("failed to read copied file", "error", err)
		return
	}
	fmt.Println(string(data))
	// Output: hello
}

// ExampleRsync shows recursively mirroring all files from one FS into another.
func ExampleRsync() {
	ctx := context.Background()
	src, err := ufs.New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create source FS", "error", err)
		return
	}
	dst, err := ufs.New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create destination FS", "error", err)
		return
	}
	defer func() {
		if err := src.Close(); err != nil {
			slog.Error("failed to close source FS", "error", err)
			return
		}
	}()
	defer func() {
		if err := dst.Close(); err != nil {
			slog.Error("failed to close destination FS", "error", err)
			return
		}
	}()

	if err := src.MkdirAll("subdir", fs.ModePerm); err != nil {
		slog.Error("failed to create directory", "error", err)
		return
	}
	for _, name := range []string{"a.txt", "subdir/b.txt"} {
		f, err := src.Create(name)
		if err != nil {
			slog.Error("failed to create file", "error", err)
			return
		}
		if _, err := f.WriteString("content"); err != nil {
			slog.Error("failed to write to file", "error", err)
			return
		}
		if err := f.Close(); err != nil {
			slog.Error("failed to close file", "error", err)
			return
		}
	}

	if err := ops.Rsync(src, dst, "."); err != nil {
		slog.Error("failed to rsync files", "error", err)
		return
	}

	files, err := ops.ListFiles(dst, ".")
	if err != nil {
		slog.Error("failed to list files", "error", err)
		return
	}
	for _, p := range files {
		fmt.Println(p)
	}
	// Output:
	// a.txt
	// subdir/b.txt
}

// ExampleListFiles shows listing only files (no directories) under a path.
func ExampleListFiles() {
	ctx := context.Background()
	fsys, err := ufs.New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create FS", "error", err)
		return
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("failed to close FS", "error", err)
			return
		}
	}()

	if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
		slog.Error("failed to create directory", "error", err)
		return
	}
	for _, name := range []string{"a.txt", "b.txt", "subdir/c.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			slog.Error("failed to create file", "error", err)
			return
		}
		if err := f.Close(); err != nil {
			slog.Error("failed to close file", "error", err)
			return
		}
	}

	files, err := ops.ListFiles(fsys, ".")
	if err != nil {
		slog.Error("failed to list files", "error", err)
		return
	}
	for _, p := range files {
		fmt.Println(p)
	}
	// Output:
	// a.txt
	// b.txt
	// subdir/c.txt
}

// ExampleList shows listing all entries including directories.
func ExampleList() {
	ctx := context.Background()
	fsys, err := ufs.New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create FS", "error", err)
		return
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("failed to close FS", "error", err)
			return
		}
	}()

	if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
		slog.Error("failed to create directory", "error", err)
		return
	}
	f, err := fsys.Create("subdir/c.txt")
	if err != nil {
		slog.Error("cannot create file", "error", err)
		return
	}
	if err := f.Close(); err != nil {
		slog.Error("failed to close file", "error", err)
		return
	}

	entries, err := ops.List(fsys, ".")
	if err != nil {
		slog.Error("failed to list entries", "error", err)
		return
	}
	for _, p := range entries {
		fmt.Println(p)
	}
	// Output:
	// subdir
	// subdir/c.txt
}

// ExampleForEachFilename shows streaming file names without building a slice,
// which saves memory for large trees.
func ExampleForEachFilename() {
	ctx := context.Background()
	fsys, err := ufs.New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create FS", "error", err)
		return
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("failed to close FS", "error", err)
			return
		}
	}()

	for _, name := range []string{"a.txt", "b.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			slog.Error("failed to create file", "error", err)
			return
		}
		if err := f.Close(); err != nil {
			slog.Error("failed to close file", "error", err)
			return
		}
	}

	if err := ops.ForEachFilename(fsys, ".", func(name string) error {
		fmt.Println(name)
		return nil
	}); err != nil {
		slog.Error("failed to iterate over filenames", "error", err)
		return
	}
	// Output:
	// a.txt
	// b.txt
}
