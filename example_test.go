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
	"log"
)

// ExampleNew_memory demonstrates a volatile in-memory file system. All data is
// lost when the FS is closed or the process exits.
func ExampleNew_memory() {
	ctx := context.Background()
	fsys, err := New(ctx, "memory://")
	if err != nil {
		log.Fatal(err)
	}
	defer fsys.Close()

	f, err := fsys.Create("hello.txt")
	if err != nil {
		log.Fatal(err)
	}
	if _, err := f.WriteString("hello, world"); err != nil {
		log.Fatal(err)
	}
	if err := f.Close(); err != nil {
		log.Fatal(err)
	}

	data, err := fsys.ReadFile("hello.txt")
	if err != nil {
		log.Fatal(err)
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
		log.Fatal(err)
	}
	defer fsys.Close()

	f, err := fsys.Create("discard.txt")
	if err != nil {
		log.Fatal(err)
	}
	n, writeErr := f.WriteString("this data is discarded")
	fmt.Printf("wrote %d bytes, err=%v\n", n, writeErr)

	if err := f.Close(); err != nil {
		log.Fatal(err)
	}

	// ReadFile always returns an empty byte slice, not an error.
	data, err := fsys.ReadFile("discard.txt")
	if err != nil {
		log.Fatal(err)
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
		log.Fatalf("failed to create source FS: %v", err)
	}
	dst, err := New(ctx, "memory://")
	if err != nil {
		log.Fatalf("failed to create destination FS: %v", err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			log.Fatalf("failed to close source FS: %v", err)
		}
	}()
	defer func() {
		if err := dst.Close(); err != nil {
			log.Fatalf("failed to close destination FS: %v", err)
		}
	}()

	f, err := src.Create("hello.txt")
	if err != nil {
		log.Fatalf("failed to create file in source FS: %v", err)
	}

	if _, err = f.WriteString("hello"); err != nil {
		log.Fatalf("failed to write to file in source FS: %v", err)
	}
	if err := f.Close(); err != nil {
		log.Fatalf("failed to close file in source FS: %v", err)
	}

	if err := Copy(src, "hello.txt", dst, "copy.txt"); err != nil {
		log.Fatal(err)
	}

	data, err := dst.ReadFile("copy.txt")
	if err != nil {
		log.Fatalf("failed to read copied file: %v", err)
	}
	fmt.Println(string(data))
	// Output: hello
}

// ExampleRsync shows recursively mirroring all files from one FS into another.
func ExampleRsync() {
	ctx := context.Background()
	src, err := New(ctx, "memory://")
	if err != nil {
		log.Fatalf("failed to create source FS: %v", err)
	}
	dst, err := New(ctx, "memory://")
	if err != nil {
		log.Fatalf("failed to create destination FS: %v", err)
	}
	defer func() {
		if err := src.Close(); err != nil {
			log.Fatalf("failed to close source FS: %v", err)
		}
	}()
	defer func() {
		if err := dst.Close(); err != nil {
			log.Fatalf("failed to close destination FS: %v", err)
		}
	}()

	if err := src.MkdirAll("subdir", fs.ModePerm); err != nil {
		log.Fatalf("failed to create directory: %v", err)
	}
	for _, name := range []string{"a.txt", "subdir/b.txt"} {
		f, err := src.Create(name)
		if err != nil {
			log.Fatalf("failed to create file: %v", err)
		}
		if _, err := f.WriteString("content"); err != nil {
			log.Fatalf("failed to write to file: %v", err)
		}
		if err := f.Close(); err != nil {
			log.Fatalf("failed to close file: %v", err)
		}
	}

	if err := Rsync(src, dst, "."); err != nil {
		log.Fatal(err)
	}

	files, err := ListFiles(dst, ".")
	if err != nil {
		log.Fatalf("failed to list files: %v", err)
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
		log.Fatal(err)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			log.Fatalf("failed to close FS: %v", err)
		}
	}()

	if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
		log.Fatalf("failed to create directory: %v", err)
	}
	for _, name := range []string{"a.txt", "b.txt", "subdir/c.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			log.Fatalf("failed to create file: %v", err)
		}
		if err := f.Close(); err != nil {
			log.Fatalf("failed to close file: %v", err)
		}
	}

	files, err := ListFiles(fsys, ".")
	if err != nil {
		log.Fatalf("failed to list files: %v", err)
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
		log.Fatal(err)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			log.Fatalf("failed to close FS: %v", err)
		}
	}()

	if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
		log.Fatalf("failed to create directory: %v", err)
	}
	f, err := fsys.Create("subdir/c.txt")
	if err != nil {
		log.Fatalf("cannot create file: %v", err)
	}
	if err := f.Close(); err != nil {
		log.Fatalf("failed to close file: %v", err)
	}

	entries, err := List(fsys, ".")
	if err != nil {
		log.Fatalf("failed to list entries: %v", err)
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
		log.Fatal(err)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			log.Fatalf("failed to close FS: %v", err)
		}
	}()

	for _, name := range []string{"a.txt", "b.txt"} {
		f, err := fsys.Create(name)
		if err != nil {
			log.Fatalf("failed to create file: %v", err)
		}
		if err := f.Close(); err != nil {
			log.Fatalf("failed to close file: %v", err)
		}
	}

	if err := ForEachFilename(fsys, ".", func(name string) error {
		fmt.Println(name)
		return nil
	}); err != nil {
		log.Fatalf("failed to iterate over filenames: %v", err)
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
		log.Fatal(err)
	}
	fmt.Println(uri)

	// Open it — New accepts URIs produced by CreateURI.
	fsys, err := New(ctx, uri)
	if err != nil {
		log.Fatal(err)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			log.Fatalf("failed to close FS: %v", err)
		}
	}()
	fmt.Println(fsys.URI())
	// Output:
	// memory:
	// memory:
}
