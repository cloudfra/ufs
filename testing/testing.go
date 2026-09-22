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
	"math/rand/v2"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"
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

// MustTime parses s as an RFC 3339 timestamp. It calls tb.Fatalf on parse
// failure.
func MustTime(tb testing.TB, s string) time.Time {
	tb.Helper()
	val, err := time.Parse(time.RFC3339, s)
	if err != nil {
		tb.Fatalf("parsing time %q: %v", s, err)
	}
	return val
}

// RandomString returns a random string of length size drawn from a
// human-friendly alphabet (uppercase and lowercase letters and digits). It is
// safe for concurrent use: it relies on the standard library's goroutine-safe
// global random source. The output is not cryptographically random.
func RandomString(size int) string {
	const alphabet = "abcdefghijklmnopqrstuvwxyz" +
		"ABCDEFGHIJKLMNOPQRSTUVWXYZ" +
		"0123456789"
	var b strings.Builder
	b.Grow(size)
	for i := 0; i < size; i++ {
		b.WriteByte(alphabet[rand.IntN(len(alphabet))]) //nolint:gosec // G404: non-cryptographic test-fixture names
	}
	return b.String()
}

// AssertInvalidPathError asserts that err is a *fs.PathError with the given Op
// and Path.
func AssertInvalidPathError(tb testing.TB, path string, err error, wantOp string) {
	tb.Helper()
	if err == nil {
		tb.Errorf("%s(%q) succeeded, want error", wantOp, path)
		return
	}
	if perr, ok := err.(*fs.PathError); ok {
		if wantOp != perr.Op {
			tb.Errorf("fs.PathError.Op mismatch, got: %q, want: %q", perr.Op, wantOp)
		}
		if path != perr.Path {
			tb.Errorf("fs.PathError.Path mismatch, got: %q, want: %q", perr.Path, path)
		}
	} else {
		tb.Errorf("%q is not a *fs.PathError, got: %q", err, reflect.TypeOf(err).Name())
	}
}
