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

// Tests for [ufs.FSBuilder.MountFS] using [embedfs.New], kept in a separate
// external (ufs_test) package because drivers/embedfs imports ufs.
package ufs_test

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/embedfs"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestFSBuilderBuildURIWithFSMountErrors(t *testing.T) {
	t.Parallel()
	b := ufs.NewFSBuilder("memory://").MountFS("assets", embedfs.New("assets", ufsTesting.TestAssetsFS()))
	_, err := b.BuildURI()
	if err == nil {
		t.Fatal("BuildURI() with MountFS = nil, want error")
	}
}

func TestFSBuilderMountFS(t *testing.T) {
	t.Parallel()
	embedFSys := embedfs.New("assets", ufsTesting.TestAssetsFS())
	fsys, err := ufs.NewFSBuilder("memory://").MountFS("assets", embedFSys).Build(t.Context())
	if err != nil {
		t.Fatalf("Build() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	entries, err := fsys.ReadDir("assets/testassets/files")
	if err != nil {
		t.Fatalf("ReadDir(assets/...) = %v, want nil", err)
	}
	if len(entries) == 0 {
		t.Error("ReadDir returned empty entries under embed mount, want non-empty")
	}
}

func TestFSBuilderMountFSWriteBlocked(t *testing.T) {
	t.Parallel()
	embedFSys := embedfs.New("assets", ufsTesting.TestAssetsFS())
	fsys, err := ufs.NewFSBuilder("memory://").MountFS("assets", embedFSys).Build(t.Context())
	if err != nil {
		t.Fatalf("Build() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	_, err = fsys.Create("assets/newfile.txt")
	if !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Create under embedFS mount = %v, want fs.ErrPermission", err)
	}
}
