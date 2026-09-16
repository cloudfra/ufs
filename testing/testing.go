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

import (
	"context"
	"embed"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/google/go-cmp/cmp"
	"github.com/xyproto/randomstring"
)

const (
	unixPathSeparator    = "/"
	windowsPathSeparator = "\\"
	CwdPath              = "."
)

var (
	TestassetFilenameList = []string{
		CwdPath,
		"files/index.html",
		"archives/nested-testassets.zip",
	}

	TestassetDirList = map[string][]string{
		CwdPath:    {},
		"files":    {},
		"archives": {},
	}

	TestassetCreateFileList = []string{"a.txt", "b.txt", "a/b.txt"}
)

//go:embed testassets/files
var embedTestFiles embed.FS

func EmbedTestFiles() embed.FS {
	return embedTestFiles
}

func Must(tb testing.TB, err error) {
	tb.Helper()
	if err != nil {
		tb.Error(err)
	}
}

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

func DirEntryListToNames(entries []fs.DirEntry) []string {
	names := make([]string, len(entries))
	for i, entry := range entries {
		names[i] = entry.Name()
	}
	return names
}

func AssertContains(t *testing.T, fsys fs.FS, name string, substr string) {
	t.Helper()
	data, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Error(err)
	}
	if !strings.Contains(string(data), substr) {
		t.Errorf("%q does not contain %q, (len: %d) %q", name, substr, len(data), string(data))
	}
}

func AssertDir(t *testing.T, fsys fs.ReadDirFS, name string, want []string) {
	t.Helper()

	if gotEntries, err := fsys.ReadDir(name); err != nil {
		t.Errorf("cannot ReadDir(%q), %s", name, err)
	} else {
		gotEntryNames := DirEntryListToNames(gotEntries)
		if d := cmp.Diff(want, gotEntryNames); d != "" {
			t.Errorf("fs.ReadDir(%q) mismatch, got %s, want %s diff(-want,+got):\n %v", name, gotEntryNames, want, d)
		}
	}

	if f, err := fsys.Open(name); err != nil {
		t.Errorf("cannot open %q, %s", name, err)
	} else {
		defer ValidateClose(t, f)()
		rdf, ok := f.(fs.ReadDirFile)
		if ok {
			if gotEntries, err := rdf.ReadDir(-1); err != nil {
				t.Errorf("cannot ReadDir(%q), %s", name, err)
			} else {
				gotEntryNames := DirEntryListToNames(gotEntries)
				if d := cmp.Diff(want, gotEntryNames); d != "" {
					t.Errorf("ReadDir(-1) mismatch, got %s, want %s diff(-want,+got):\n %v", gotEntryNames, want, d)
				}
			}
		} else {
			t.Errorf("%q does not open a ReadDirFile, %s", name, reflect.TypeOf(f).Name())
		}
	}
}

func SkipTestOnWindows(tb testing.TB) {
	if runtime.GOOS == "windows" {
		tb.Skip("test is not compatible with windows, skipping")
	}
}

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

func MustTemp(tb testing.TB) string {
	tempDir, err := os.MkdirTemp("", "")
	if err != nil {
		tb.Fatal(err)
	}

	tb.Cleanup(func() {
		if err := os.RemoveAll(filepath.Clean(tempDir)); err != nil {
			tb.Error(err)
		}
	})
	return tempDir
}

func MustTime(s string) time.Time {
	val, err := time.Parse(time.RFC3339, s)
	if err != nil {
		panic(err)
	}
	return val
}

func MustFS(tb testing.TB, newFSFunc func(context.Context, string) (ufs.FS, error), name string) ufs.FS {
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

func randomString(size int) string {
	return randomstring.HumanFriendlyString(size)
}

func osTempDir() string {
	return coerceUnix(os.TempDir())
}

func coerceUnix(name string) string {
	return strings.ReplaceAll(name, windowsPathSeparator, unixPathSeparator)
}
