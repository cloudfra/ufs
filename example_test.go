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

package ufs_test

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloudfra/ufs"
	_ "github.com/cloudfra/ufs/drivers/memfs"  // registers memory:
	_ "github.com/cloudfra/ufs/drivers/nullfs" // registers null:
)

// ExampleNew_memory demonstrates a volatile in-memory file system. All data is
// lost when the FS is closed or the process exits.
func ExampleNew_memory() {
	ctx := context.Background()
	fsys, err := ufs.New(ctx, "memory://")
	if err != nil {
		slog.Error("cannot mount filesystem", "error", err)
		return
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("cannot close filesystem", "error", err)
			return
		}
	}()

	f, err := fsys.Create("hello.txt")
	if err != nil {
		slog.Error("cannot create file", "error", err)
		return
	}
	if _, err := f.WriteString("hello, world"); err != nil {
		slog.Error("cannot write to file", "error", err)
		return
	}
	if err := f.Close(); err != nil {
		slog.Error("cannot close file", "error", err)
		return
	}

	data, err := fsys.ReadFile("hello.txt")
	if err != nil {
		slog.Error("cannot read file", "error", err)
		return
	}
	fmt.Println(string(data))
	// Output: hello, world
}

// ExampleNew_null demonstrates the null file system. It accepts all writes and
// Create calls without error, but data is immediately discarded. Reads always
// return empty content. Useful as a write sink in tests.
func ExampleNew_null() {
	ctx := context.Background()
	fsys, err := ufs.New(ctx, "null://")
	if err != nil {
		slog.Error("cannot mount filesystem", "error", err)
		return
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("cannot close filesystem", "error", err)
			return
		}
	}()

	f, err := fsys.Create("discard.txt")
	if err != nil {
		slog.Error("cannot create file", "error", err)
		return
	}
	n, writeErr := f.WriteString("this data is discarded")
	fmt.Printf("wrote %d bytes, err=%v\n", n, writeErr)

	if err := f.Close(); err != nil {
		slog.Error("cannot close file", "error", err)
		return
	}

	// ReadFile always returns an empty byte slice, not an error.
	data, err := fsys.ReadFile("discard.txt")
	if err != nil {
		slog.Error("cannot read file", "error", err)
		return
	}
	fmt.Printf("read %d bytes\n", len(data))
	// Output:
	// wrote 22 bytes, err=<nil>
	// read 0 bytes
}

// ExampleCreateURI shows building a URI for a file system with a nested mount.
func ExampleCreateURI() {
	ctx := context.Background()
	// A memory FS with no nested mounts.
	uri, err := ufs.CreateURI("memory://", nil)
	if err != nil {
		slog.Error("failed to create URI", "error", err)
		return
	}
	fmt.Println(uri)

	// Open it — New accepts URIs produced by CreateURI.
	fsys, err := ufs.New(ctx, uri)
	if err != nil {
		slog.Error("cannot mount filesystem", "error", err)
		return
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Error("failed to close FS", "error", err)
			return
		}
	}()
	u, err := fsys.URI()
	if err != nil {
		fmt.Println(err)
	} else {
		fmt.Println(u)
	}
	// Output:
	// memory:
	// memory:
}
