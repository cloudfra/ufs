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

package ufs

import (
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/drivers/wrappers/readwrapfs"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestNestFSIsSame checks that a nest compares as one file system and one set of
// mounts, which its URI cannot express because two file systems can report the
// same URI while holding separate data.
func TestNestFSIsSame(t *testing.T) {
	t.Parallel()

	inner := mustBaseFS(t, "memory://")

	if !types.SameFS(makeNestFS(t.Context(), inner), makeNestFS(t.Context(), inner)) {
		t.Error("two nestFS over one file system and no mounts should report the same data")
	}

	separate := makeNestFS(t.Context(), mustBaseFS(t, "memory://"))
	if types.SameFS(makeNestFS(t.Context(), inner), separate) {
		t.Error("two nestFS over separate file systems should not report the same data")
	}

	mounted := makeNestFS(t.Context(), inner)
	ufsTesting.Must(t, mounted.addMount("data", makeNestFS(t.Context(), mustBaseFS(t, "memory://data"))))
	if types.SameFS(makeNestFS(t.Context(), inner), mounted) {
		t.Error("a nestFS with a mount should not report the same data as the same file system without it")
	}

	shared := makeNestFS(t.Context(), mustBaseFS(t, "memory://data"))
	first := makeNestFS(t.Context(), inner)
	second := makeNestFS(t.Context(), inner)
	ufsTesting.Must(t, first.addMount("data", shared))
	ufsTesting.Must(t, second.addMount("data", shared))
	if !types.SameFS(first, second) {
		t.Error("two nestFS mounting one file system at one path should report the same data")
	}

	elsewhere := makeNestFS(t.Context(), mustBaseFS(t, "memory://data"))
	other := makeNestFS(t.Context(), inner)
	ufsTesting.Must(t, other.addMount("data", elsewhere))
	if types.SameFS(first, other) {
		t.Error("two nestFS mounting separate file systems at one path should not report the same data")
	}
}

// TestNestFSIsNotWrapperFS pins that nestFS keeps types.WrapperFS unimplemented,
// because a nest's mounts are part of its data and are not reachable through one
// inner file system.
func TestNestFSIsNotWrapperFS(t *testing.T) {
	t.Parallel()

	nest := makeNestFS(t.Context(), mustBaseFS(t, "memory://"))
	if _, ok := any(nest).(types.WrapperFS); ok {
		t.Error("a nestFS must not implement types.WrapperFS, because its mounts are part of its data")
	}
	if innermost := types.InnermostFS(nest); innermost != fs.FS(nest) {
		t.Errorf("types.InnermostFS(nestFS) = %v, want the nestFS itself", innermost)
	}
}

// TestWrappedNestFSIsSame checks that a wrapper around a nest is still compared
// as that nest, which is how a wrapper reaches the data it presents without the
// nest itself being reduced to the file system under it.
func TestWrappedNestFSIsSame(t *testing.T) {
	t.Parallel()

	inner := mustBaseFS(t, "memory://")
	first := makeNestFS(t.Context(), inner)
	second := makeNestFS(t.Context(), inner)

	if !types.SameFS(readwrapfs.FromFS(first), second) {
		t.Error("a read-only view of a nestFS should report the same data as that nestFS")
	}
	if !types.SameFS(second, readwrapfs.FromFS(first)) {
		t.Error("a nestFS should report the same data as a read-only view of that nestFS")
	}
	if !types.SameFS(readwrapfs.FromFS(readwrapfs.FromFS(first)), second) {
		t.Error("two read-only views over a nestFS should report the same data as that nestFS")
	}

	mounted := makeNestFS(t.Context(), inner)
	ufsTesting.Must(t, mounted.addMount("data", makeNestFS(t.Context(), mustBaseFS(t, "memory://data"))))
	if types.SameFS(readwrapfs.FromFS(first), mounted) {
		t.Error("a read-only view of a mountless nestFS should not report the same data as a nestFS with a mount")
	}

	if types.SameFS(readwrapfs.FromFS(first), inner) {
		t.Error("a read-only view of a nestFS should not report the same data as the file system under the nest")
	}
	if types.SameFS(inner, readwrapfs.FromFS(first)) {
		t.Error("the file system under a nestFS should not report the same data as a read-only view of the nest")
	}
}
