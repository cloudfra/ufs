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
	f.WriteString("hello, world")
	f.Close()

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
	f.Close()
	fmt.Printf("wrote %d bytes, err=%v\n", n, writeErr)

	// ReadFile always returns an empty byte slice, not an error.
	data, _ := fsys.ReadFile("discard.txt")
	fmt.Printf("read %d bytes\n", len(data))
	// Output:
	// wrote 22 bytes, err=<nil>
	// read 0 bytes
}

// ExampleCopy shows copying a single file between two file systems.
func ExampleCopy() {
	ctx := context.Background()
	src, _ := New(ctx, "memory://")
	dst, _ := New(ctx, "memory://")
	defer src.Close()
	defer dst.Close()

	f, _ := src.Create("hello.txt")
	f.WriteString("hello")
	f.Close()

	if err := Copy(src, "hello.txt", dst, "copy.txt"); err != nil {
		log.Fatal(err)
	}

	data, _ := dst.ReadFile("copy.txt")
	fmt.Println(string(data))
	// Output: hello
}

// ExampleRsync shows recursively mirroring all files from one FS into another.
func ExampleRsync() {
	ctx := context.Background()
	src, _ := New(ctx, "memory://")
	dst, _ := New(ctx, "memory://")
	defer src.Close()
	defer dst.Close()

	src.MkdirAll("subdir", fs.ModePerm)
	for _, name := range []string{"a.txt", "subdir/b.txt"} {
		f, _ := src.Create(name)
		f.WriteString("content")
		f.Close()
	}

	if err := Rsync(src, dst, "."); err != nil {
		log.Fatal(err)
	}

	files, _ := ListFiles(dst, ".")
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
	fsys, _ := New(ctx, "memory://")
	defer fsys.Close()

	fsys.MkdirAll("subdir", fs.ModePerm)
	for _, name := range []string{"a.txt", "b.txt", "subdir/c.txt"} {
		f, _ := fsys.Create(name)
		f.Close()
	}

	files, _ := ListFiles(fsys, ".")
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
	fsys, _ := New(ctx, "memory://")
	defer fsys.Close()

	fsys.MkdirAll("subdir", fs.ModePerm)
	f, _ := fsys.Create("subdir/c.txt")
	f.Close()

	entries, _ := List(fsys, ".")
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
	fsys, _ := New(ctx, "memory://")
	defer fsys.Close()

	for _, name := range []string{"a.txt", "b.txt"} {
		f, _ := fsys.Create(name)
		f.Close()
	}

	ForEachFilename(fsys, ".", true, func(name string) error {
		fmt.Println(name)
		return nil
	})
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
	defer fsys.Close()
	fmt.Println(fsys.String())
	// Output:
	// memory:
	// nestFS(memory://)
}
