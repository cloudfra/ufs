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
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
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

// Must reports err via tb.Error if it is non-nil. Unlike a typical Must
// helper it does not stop the test, so subsequent assertions still run.
func Must(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Error(err)
	}
}

// ToMapKeys returns the keys of m in ascending sorted order. It returns an
// empty, non-nil slice when m is empty or nil.
func ToMapKeys[T any](m map[string]T) []string {
	keys := make([]string, len(m))
	idx := 0
	for k := range m {
		keys[idx] = k
		idx++
	}
	sort.Strings(keys)
	return keys
}

// AssertContains reads the file name from fsys and reports an error via
// tb.Errorf if its contents do not contain substr. A read failure is also
// reported via tb.Error.
func AssertContains(tb testing.TB, fsys fs.FS, name string, substr string) {
	tb.Helper()
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		tb.Error(err)
	}
	if !strings.Contains(string(data), substr) {
		tb.Errorf("%q does not contain %q, (len: %d) %q", name, substr, len(data), string(data))
	}
}

// AssertDir asserts that the directory name in fsys contains exactly the
// entries in want, in order. It checks both fs.ReadDirFS.ReadDir(name) and
// fs.ReadDirFile.ReadDir(-1) on the file returned by fsys.Open(name), so both
// code paths of an implementation are exercised. It calls tb.Fatalf if fsys
// does not implement fs.ReadDirFS.
func AssertDir(tb testing.TB, fsys fs.FS, name string, want []string) {
	tb.Helper()

	rdfsys, ok := fsys.(fs.ReadDirFS)
	if !ok || rdfsys == nil {
		tb.Fatalf("%+v is not an fs.ReadDirFS", fsys)
	}
	if gotEntries, err := rdfsys.ReadDir(name); err != nil {
		tb.Errorf("cannot ReadDir(%q), %s", name, err)
	} else {
		gotEntryNames := DirEntryListToNames(gotEntries)
		if d := cmp.Diff(want, gotEntryNames); d != "" {
			tb.Errorf("fs.ReadDir(%q) mismatch, got %s, want %s diff(-want,+got):\n %v", name, gotEntryNames, want, d)
		}
	}

	if f, err := fsys.Open(name); err != nil {
		tb.Errorf("cannot open %q, %s", name, err)
	} else {
		defer ValidateClose(tb, f)()
		rdf, ok := f.(fs.ReadDirFile)
		if ok {
			if gotEntries, err := rdf.ReadDir(-1); err != nil {
				tb.Errorf("cannot ReadDir(%q), %s", name, err)
			} else {
				gotEntryNames := DirEntryListToNames(gotEntries)
				if d := cmp.Diff(want, gotEntryNames); d != "" {
					tb.Errorf("ReadDir(-1) mismatch, got %s, want %s diff(-want,+got):\n %v", gotEntryNames, want, d)
				}
			}
		} else {
			tb.Errorf("%q does not open a ReadDirFile, %s", name, reflect.TypeOf(f).Name())
		}
	}
}
