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

package core

import (
	"context"
	"errors"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/testing/eventtest"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestMemBatchCreateWatch(t *testing.T) {
	fsys := makeMemFS("memory:")
	if err := ufs.BatchCreate(fsys, types.BatchFile{Name: "old.txt"}); err != nil {
		t.Fatal(err)
	}

	ec := eventtest.NewEventCollector[types.NotifyOp]()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	closer, err := fsys.Watch(ctx, ".", ec.Hook)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, closer)()

	err = ufs.BatchCreate(fsys,
		types.BatchFile{Name: "a/b/new.txt", Data: []byte("new")},
		types.BatchFile{Name: "old.txt", Data: []byte("replaced")},
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []eventtest.Event[types.NotifyOp]{
		{Op: types.NotifyCreate, Path: "a"},
		{Op: types.NotifyCreate, Path: "a/b"},
		{Op: types.NotifyCreate, Path: "a/b/new.txt"},
		{Op: types.NotifyWrite, Path: "old.txt"},
	} {
		ec.WaitFor(t, eventtest.EventDeadline, func(ev eventtest.Event[types.NotifyOp]) bool {
			return ev == want
		})
	}
}

// TestMemBatchCreateRollback checks that a failed batch restores a file it
// replaced and removes the directories it created.
func TestMemBatchCreateRollback(t *testing.T) {
	fsys := makeMemFS("memory:")
	if err := ufs.BatchCreate(fsys, types.BatchFile{Name: "keep.txt", Data: []byte("keep")}); err != nil {
		t.Fatal(err)
	}
	err := ufs.BatchCreate(fsys,
		types.BatchFile{Name: "keep.txt", Data: []byte("replaced")},
		types.BatchFile{Name: "x/y/z.txt"},
		types.BatchFile{Name: "keep.txt/child.txt"},
	)
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) || pathErr.Path != "keep.txt/child.txt" {
		t.Fatalf("BatchCreate() = %v, want an *fs.PathError for keep.txt/child.txt", err)
	}
	ufsTesting.AssertContains(t, fsys, "keep.txt", "keep")
	if _, err := fsys.Stat("x"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(x) = %v, want %v", err, fs.ErrNotExist)
	}
}

func TestMemBatchCreatePermAndClosed(t *testing.T) {
	fsys := makeMemFS("memory:")
	if err := ufs.BatchCreate(fsys, types.BatchFile{Name: "p.txt", Perm: 0o600}); err != nil {
		t.Fatal(err)
	}
	if info, err := fsys.Stat("p.txt"); err != nil || info.Mode() != 0o600 {
		t.Errorf("Stat(p.txt) = %v, %v, want mode %v", info, err, fs.FileMode(0o600))
	}
	if err := fsys.Close(); err != nil {
		t.Fatal(err)
	}
	if err := ufs.BatchCreate(fsys, types.BatchFile{Name: "a.txt"}); !errors.Is(err, fs.ErrClosed) {
		t.Errorf("BatchCreate() on a closed FS = %v, want %v", err, fs.ErrClosed)
	}
}
