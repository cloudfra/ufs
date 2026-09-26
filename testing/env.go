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

package testing

// Host-environment helpers: temp directories and platform-based skips.

import (
	"os"
	"runtime"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// TempDir returns the OS temp directory with forward slashes.
func TempDir() string {
	return pathutil.CoerceUnix(os.TempDir())
}

// MkdirTemp creates a new temp directory and removes it when tb finishes.
func MkdirTemp(tb testing.TB) string {
	tb.Helper()
	dir, err := osutil.MkdirTemp("", "")
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := osutil.RemoveAll(dir); err != nil {
			tb.Error(err)
		}
	})
	return dir
}

// SkipUnlessFSNotifySupported skips the test on platforms fsnotify does not
// support.
func SkipUnlessFSNotifySupported(tb testing.TB) {
	tb.Helper()
	switch runtime.GOOS {
	case "linux", "darwin", "windows", "freebsd", "openbsd", "netbsd", "dragonfly":
	default:
		tb.Skipf("fsnotify not supported on %s", runtime.GOOS)
	}
}
