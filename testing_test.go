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

package ufs

import (
	"context"
	"io"
	"io/fs"
	"os"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/xyproto/randomstring"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

func mustFS(tb testing.TB, newFSFunc func(context.Context, string) (FS, error), name string) FS {
	tb.Helper()

	fsys, err := newFSFunc(tb.Context(), name)
	if err != nil {
		tb.Fatalf("FileSystem %q has an error, %s", name, err)
	}
	if fsys == nil {
		tb.Fatalf("FileSystem %q is nil", name)
	}

	return fsys
}

// randomStringMu guards randomstring.HumanFriendlyString, which reads and
// advances a package-level *rand.Rand with no internal locking of its own.
// Needed because randomString is now called concurrently from many more
// parallel subtests (across every conformance driver) than before.
var randomStringMu sync.Mutex

func randomString(size int) string {
	randomStringMu.Lock()
	defer randomStringMu.Unlock()
	return randomstring.HumanFriendlyString(size)
}

func osTempDir() string {
	return pathutil.CoerceUnix(os.TempDir())
}

func mustTemp(tb testing.TB) string {
	tempDir, err := osutil.MkdirTemp("", "")
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() {
		if err := osutil.RemoveAll(tempDir); err != nil {
			tb.Error(err)
		}
	})
	return tempDir
}

func mustTime(s string) time.Time {
	val, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return val
}

func must(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Error(err)
	}
}

func toMapKeys[T any](m map[string]T) []string {
	keys := make([]string, len(m))
	idx := 0
	for k := range m {
		keys[idx] = k
		idx++
	}
	sort.Strings(keys)
	return keys
}

func dirEntryListToNames(entries []fs.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func assertContains(t *testing.T, fsys FS, name string, substr string) {
	t.Helper()
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Error(err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("%q does not contain %q, (len: %d) %q", name, substr, len(data), string(data))
	}
}

func assertDir(t *testing.T, fsys FS, name string, want []string) {
	t.Helper()

	if gotEntries, err := fsys.ReadDir(name); err != nil {
		t.Errorf("cannot ReadDir(%q), %s", name, err)
	} else {
		gotEntryNames := dirEntryListToNames(gotEntries)
		if d := cmp.Diff(want, gotEntryNames); d != "" {
			t.Errorf("fs.ReadDir(%q) mismatch, got %s, want %s diff(-want,+got):\n %v", name, gotEntryNames, want, d)
		}
	}

	if f, err := fsys.Open(name); err != nil {
		t.Errorf("cannot open %q, %s", name, err)
	} else {
		defer validateClose(t, f)()
		rdf, ok := f.(fs.ReadDirFile)
		if ok {
			if gotEntries, err := rdf.ReadDir(-1); err != nil {
				t.Errorf("cannot ReadDir(%q), %s", name, err)
			} else {
				gotEntryNames := dirEntryListToNames(gotEntries)
				if d := cmp.Diff(want, gotEntryNames); d != "" {
					t.Errorf("ReadDir(-1) mismatch, got %s, want %s diff(-want,+got):\n %v", gotEntryNames, want, d)
				}
			}
		} else {
			t.Errorf("%q does not open a ReadDirFile, %s", name, reflect.TypeOf(f).Name())
		}
	}
}

func skipTestOnWindows(tb testing.TB) {
	if runtime.GOOS == "windows" {
		tb.Skip("test is not compatible with windows, skipping")
	}
}

func validateClose(tb testing.TB, closer io.Closer) func() {
	return func() {
		tb.Helper()
		if closer != nil {
			if err := closer.Close(); err != nil {
				tb.Errorf("failed to close %s, %s", closer, err)
			}
		}
	}
}

func wantCloseError(tb testing.TB, closer io.Closer) func() {
	return func() {
		tb.Helper()
		if closer != nil {
			if err := closer.Close(); err == nil {
				tb.Errorf("want %s.Close() error, got nil", closer)
			}
		}
	}
}
