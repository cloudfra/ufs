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
	"errors"
	"testing"

	"github.com/cloudfra/ufs"
)

// hostFS is a file system defined outside of the ufs package that reports
// host paths through [ufs.AbsPathGetter].
type hostFS struct {
	err error
}

var _ ufs.AbsPathGetter = hostFS{}

func (fsys hostFS) GetAbsPath(name string) (string, error) {
	return "/host/" + name, fsys.err
}

// TestAbsPathUsesAbsPathGetter checks that AbsPath resolves names through a
// file system from another package that implements AbsPathGetter, and passes
// its error on.
func TestAbsPathUsesAbsPathGetter(t *testing.T) {
	t.Parallel()

	got, err := ufs.AbsPath(hostFS{}, "file.txt")
	if err != nil {
		t.Fatalf("AbsPath() = %v, want nil", err)
	}
	if want := "/host/file.txt"; got != want {
		t.Errorf("AbsPath() = %q, want %q", got, want)
	}

	wantErr := errors.New("not on the host")
	if _, err := ufs.AbsPath(hostFS{err: wantErr}, "file.txt"); !errors.Is(err, wantErr) {
		t.Errorf("AbsPath() = %v, want %v", err, wantErr)
	}
}
