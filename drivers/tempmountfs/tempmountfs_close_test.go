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

package tempmountfs

import (
	"errors"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs"
)

func TestTempMountFSCloseRunsCleanupOnInnerError(t *testing.T) {
	t.Parallel()

	var cleanupCalled atomic.Int32
	angry := ufs.MakeAngryFS(ufs.AngryFSPrefix)
	tfs := makeTempMountFS(angry, "test://", "test://", func() error {
		cleanupCalled.Add(1)
		return nil
	})

	err := tfs.Close()
	if err == nil {
		t.Fatal("Close() should return error from angry lfs")
	}
	if cleanupCalled.Load() < 1 {
		t.Error("cleanup function was not called when inner FS Close failed")
	}
}

func TestTempMountFSCloseReportsBothErrors(t *testing.T) {
	t.Parallel()

	angry := ufs.MakeAngryFS(ufs.AngryFSPrefix)
	cleanupErr := errors.New("cleanup boom")
	tfs := makeTempMountFS(angry, "test://", "test://", func() error {
		return cleanupErr
	})

	err := tfs.Close()
	if err == nil {
		t.Fatal("Close() should return error")
	}
	if !errors.Is(err, cleanupErr) {
		t.Errorf("Close() error should contain cleanup error, got: %v", err)
	}
}
