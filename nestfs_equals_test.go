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

package ufs_test

import (
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestNestFSEqualsBackingFS checks that a nestFS with no mounts is the same file
// system as the one it wraps, which is what lets an operation handed two file
// systems notice that they are the same one underneath. It lives in the external
// test package because it needs a driver to provide the wrapped file system.
func TestNestFSEqualsBackingFS(t *testing.T) {
	t.Parallel()

	backing := core.MakeMemFS("equals-backing")

	plain := ufs.WrapNestFS(t.Context(), backing)
	wrapped := ufs.WrapNestFS(t.Context(), plain)
	// Closing the outer nestFS closes the ones below it, so only the outer one
	// is registered for cleanup.
	t.Cleanup(ufsTesting.ValidateClose(t, wrapped))

	if !types.Equals(plain, backing) {
		t.Error("Equals(nestFS, the file system it wraps) = false, want true")
	}
	if !types.Equals(wrapped, backing) {
		t.Error("Equals(nestFS around a nestFS, the file system underneath) = false, want true")
	}
	if !types.Equals(plain, wrapped) {
		t.Error("Equals(nestFS, a nestFS wrapped around it) = false, want true")
	}

	other := core.MakeMemFS("equals-other")
	t.Cleanup(ufsTesting.ValidateClose(t, other))
	if types.Equals(plain, other) {
		t.Error("Equals(nestFS, an unrelated file system) = true, want false")
	}

	unwrapper, ok := plain.(types.FSUnwrapper)
	if !ok {
		t.Fatal("a nestFS should implement types.FSUnwrapper")
	}
	if unwrapper.UnwrapFS() == nil {
		t.Error("UnwrapFS() of a nestFS with no mounts = nil, want the backing file system")
	}
}

// TestNestFSWithMountsKeepsIdentity checks that a nestFS holding mounts is not
// compared as the file system it wraps, because the mounts add paths and files
// that only the nestFS has.
func TestNestFSWithMountsKeepsIdentity(t *testing.T) {
	t.Parallel()

	fsys, err := ufs.New(t.Context(), "memory://?one=memory://")
	ufsTesting.Must(t, err)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))

	unwrapper, ok := fsys.(types.FSUnwrapper)
	if !ok {
		t.Fatal("a nestFS should implement types.FSUnwrapper")
	}
	if unwrapper.UnwrapFS() != nil {
		t.Error("UnwrapFS() of a nestFS with mounts returned a file system, want nil")
	}
	if !types.Equals(fsys, fsys) {
		t.Error("Equals(a nestFS with mounts, itself) = false, want true")
	}
}
