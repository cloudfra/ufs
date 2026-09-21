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

// Package testing provides shared helpers and test assets for testing ufs.
package testing

import (
	"embed"
	"io"
	"io/fs"
	"reflect"
	"runtime"
	"testing"
	"time"

	"github.com/xyproto/randomstring"
)

//go:embed testassets/files
var embedTestFiles embed.FS

// TestAssetsFS returns an [embed.FS] with all the test assets of ufs.
func TestAssetsFS() embed.FS {
	return embedTestFiles
}

// ValidateClose returns a deferred-cleanup function that closes closer (e.g.
// an io.Closer) and records a failure if Close() does not return nil. Pass
// closer == nil to make the cleanup a no-op:
//
//	defer testing.ValidateClose(t, f)()
func ValidateClose(tb testing.TB, closer io.Closer) func() {
	return func() {
		tb.Helper()
		if closer != nil {
			if err := closer.Close(); err != nil {
				tb.Errorf("failed to close %s, %s", closer, err)
			}
		}
	}
}

// WantCloseError is the inverse of ValidateClose. Register a deferred call to
// assert that Close() DID return an error. Pass closer == nil to make the
// cleanup a no-op:
//
//	defer testing.WantCloseError(t, f)()
func WantCloseError(tb testing.TB, closer io.Closer) func() {
	return func() {
		tb.Helper()
		if closer != nil {
			if err := closer.Close(); err == nil {
				tb.Errorf("want %s.Close() error, got nil", closer)
			}
		}
	}
}

// SkipTestOnWindows skips the test (or sub-test) running on Windows.
func SkipTestOnWindows(tb testing.TB) {
	if runtime.GOOS == "windows" {
		tb.Skip("test is not compatible with windows, skipping")
	}
}

// DirEntryListToNames extracts the names from a []fs.DirEntry slice in order.
func DirEntryListToNames(entries []fs.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

// MustTime parses s as RFC 3339 and panics if parsing fails.
func MustTime(s string) time.Time {
	val, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return val
}

// RandomString returns a random human-friendly string of length size.
func RandomString(size int) string {
	return randomstring.HumanFriendlyString(size)
}

// AssertInvalidPathError asserts that err is a *fs.PathError with the given Op
// and Path.
func AssertInvalidPathError(t *testing.T, path string, err error, wantOp string) {
	t.Helper()
	if err == nil {
		t.Errorf("%s(%q) succeeded, want error", wantOp, path)
		return
	}
	if perr, ok := err.(*fs.PathError); ok {
		if wantOp != perr.Op {
			t.Errorf("fs.PathError.Op mismatch, got: %q, want: %q", perr.Op, wantOp)
		}
		if path != perr.Path {
			t.Errorf("fs.PathError.Path mismatch, got: %q, want: %q", perr.Path, path)
		}
	} else {
		t.Errorf("%q is not a *fs.PathError, got: %q", err, reflect.TypeOf(err).Name())
	}
}
