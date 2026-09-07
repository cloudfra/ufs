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
	"fmt"
	"io/fs"
	"log/slog"
	"os"
)

// ExampleNew_memory demonstrates a volatile in-memory file system. All data is
// lost when the FS is closed or the process exits.
func ExampleNew_memory() {
	ctx := context.Background()
	fsys, err := New(ctx, "memory://")
	if err != nil {
		slog.Error("cannot mount filesystem", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("cannot close filesystem", "error", err)
			os.Exit(1)
		}
	}()

	f, err := fsys.Create("hello.txt")
	if err != nil {
		slog.Error("cannot create file", "error", err)
		os.Exit(1)
	}
	if _, err := f.WriteString("hello, world"); err != nil {
		slog.Error("cannot write to file", "error", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		slog.Error("cannot close file", "error", err)
		os.Exit(1)
	}

	data, err := fsys.ReadFile("hello.txt")
	if err != nil {
		slog.Error("cannot read file", "error", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
	// Output: hello, world
}

// ExampleNew_null demonstrates the null file system. It accepts all writes and
// Create calls without error, but data is immediately discarded. Reads always
// return empty content. Useful as a write sink in tests.
func ExampleNew_null() {
	ctx := context.Background()
	fsys, err := New(ctx, "null://")
	if err != nil {
		slog.Error("cannot mount filesystem", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("cannot close filesystem", "error", err)
			os.Exit(1)
		}
	}()

	f, err := fsys.Create("discard.txt")
	if err != nil {
		slog.Error("cannot create file", "error", err)
		os.Exit(1)
	}
	n, writeErr := f.WriteString("this data is discarded")
	fmt.Printf("wrote %d bytes, err=%v\n", n, writeErr)

	if err := f.Close(); err != nil {
		slog.Error("cannot close file", "error", err)
		os.Exit(1)
	}

	// ReadFile always returns an empty byte slice, not an error.
	data, err := fsys.ReadFile("discard.txt")
	if err != nil {
		slog.Error("cannot read file", "error", err)
		os.Exit(1)
	}
	fmt.Printf("read %d bytes\n", len(data))
	// Output:
	// wrote 22 bytes, err=<nil>
	// read 0 bytes
}

// ExampleCopy shows copying a single file between two file systems.
func ExampleCopy() {
	ctx := context.Background()
	src, err := New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create source FS", "error", err)
		os.Exit(1)
	}
	dst, err := New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create destination FS", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := src.Close(); err != nil {
			slog.Error("failed to close source FS", "error", err)
			os.Exit(1)
		}
	}()
	defer func() {
		if err := dst.Close(); err != nil {
			slog.Error("failed to close destination FS", "error", err)
			os.Exit(1)
		}
	}()

	f, err := src.Create("hello.txt")
	if err != nil {
		slog.Error("failed to create file in source FS", "error", err)
		os.Exit(1)
	}

	if _, err = f.WriteString("hello"); err != nil {
		slog.Error("failed to write to file in source FS", "error", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		slog.Error("failed to close file in source FS", "error", err)
		os.Exit(1)
	}

	if err := Copy(src, "hello.txt", dst, "copy.txt"); err != nil {
		slog.Error("failed to copy file", "error", err)
		os.Exit(1)
	}

	data, err := dst.ReadFile("copy.txt")
	if err != nil {
		slog.Error("failed to read copied file", "error", err)
		os.Exit(1)
	}
	fmt.Println(string(data))
	// Output: hello
}

// ExampleRsync shows recursively mirroring all files from one FS into another.
func ExampleRsync() {
	ctx := context.Background()
	src, err := New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create source FS", "error", err)
		os.Exit(1)
	}
	dst, err := New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create destination FS", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := src.Close(); err != nil {
			slog.Error("failed to close source FS", "error", err)
			os.Exit(1)
		}
	}()
	defer func() {
		if err := dst.Close(); err != nil {
			slog.Error("failed to close destination FS", "error", err)
			os.Exit(1)
		}
	}()

	if err := src.MkdirAll("subdir", fs.ModePerm); err != nil {
		slog.Error("failed to create directory", "error", err)
		os.Exit(1)
	}
	for _, name := range []string{"a.txt", "subdir/b.txt"} {
		f, err := src.Create(name)
		if err != nil {
			slog.Error("failed to create file", "error", err)
			os.Exit(1)
		}
		if _, err := f.WriteString("content"); err != nil {
			slog.Error("failed to write to file", "error", err)
			os.Exit(1)
		}
		if err := f.Close(); err != nil {
			slog.Error("failed to close file", "error", err)
			os.Exit(1)
		}
	}

	if err := Rsync(src, dst, "."); err != nil {
		slog.Error("failed to rsync files", "error", err)
		os.Exit(1)
	}

	files, err := ListFiles(dst, ".")
	if err != nil {
		slog.Error("failed to list files", "error", err)
		os.Exit(1)
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
	fsys, err := New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create FS", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("failed to close FS", "error", err)
			os.Exit(1)
		}
	}()

	if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
		slog.Error("failed to create directory", "error", err)
		os.Exit(1)
	}
	for _, name := range []string{"a.txt", "b.txt", "subdir/c.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			slog.Error("failed to create file", "error", err)
			os.Exit(1)
		}
		if err := f.Close(); err != nil {
			slog.Error("failed to close file", "error", err)
			os.Exit(1)
		}
	}

	files, err := ListFiles(fsys, ".")
	if err != nil {
		slog.Error("failed to list files", "error", err)
		os.Exit(1)
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
	fsys, err := New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create FS", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("failed to close FS", "error", err)
			os.Exit(1)
		}
	}()

	if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
		slog.Error("failed to create directory", "error", err)
		os.Exit(1)
	}
	f, err := fsys.Create("subdir/c.txt")
	if err != nil {
		slog.Error("cannot create file", "error", err)
		os.Exit(1)
	}
	if err := f.Close(); err != nil {
		slog.Error("failed to close file", "error", err)
		os.Exit(1)
	}

	entries, err := List(fsys, ".")
	if err != nil {
		slog.Error("failed to list entries", "error", err)
		os.Exit(1)
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
	fsys, err := New(ctx, "memory://")
	if err != nil {
		slog.Error("failed to create FS", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("failed to close FS", "error", err)
			os.Exit(1)
		}
	}()

	for _, name := range []string{"a.txt", "b.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			slog.Error("failed to create file", "error", err)
			os.Exit(1)
		}
		if err := f.Close(); err != nil {
			slog.Error("failed to close file", "error", err)
			os.Exit(1)
		}
	}

	if err := ForEachFilename(fsys, ".", func(name string) error {
		fmt.Println(name)
		return nil
	}); err != nil {
		slog.Error("failed to iterate over filenames", "error", err)
		os.Exit(1)
	}
	// Output:
	// a.txt
	// b.txt
}

// ExampleCreateURI shows building a URI for a file system with a nested mount.
func ExampleCreateURI() {
	ctx := context.Background()
	// A memory FS with no nested mounts.
	uri, err := CreateURI("memory://", nil)
	if err != nil {
		slog.Error("failed to create URI", "error", err)
		os.Exit(1)
	}
	fmt.Println(uri)

	// Open it — New accepts URIs produced by CreateURI.
	fsys, err := New(ctx, uri)
	if err != nil {
		slog.Error("cannot mount filesystem", "error", err)
		os.Exit(1)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("failed to close FS", "error", err)
			os.Exit(1)
		}
	}()
	fmt.Println(fsys.URI())
	// Output:
	// memory:
	// memory:
}
