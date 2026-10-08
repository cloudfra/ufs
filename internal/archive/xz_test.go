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

package archive

import (
	"bytes"
	"errors"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	fastxz "github.com/mikelolasagasti/xz"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/testing/archivetest"
)

const testXzArchive = testArchivesDir + "testassets.tar.xz"

// xzArchiveWithDict returns the xz test archive rewritten to declare the
// LZMA2 dictionary size encoded by dictProp.
func xzArchiveWithDict(t *testing.T, dictProp byte) []byte {
	t.Helper()
	data, err := osutil.ReadFile(testXzArchive)
	if err != nil {
		t.Fatal(err)
	}
	return archivetest.XzWithDict(t, data, dictProp)
}

// writeXzArchiveWithDict writes the result of xzArchiveWithDict into a temp
// directory as name and returns its path, for tests that need a local file.
func writeXzArchiveWithDict(t *testing.T, name string, dictProp byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, xzArchiveWithDict(t, dictProp), osutil.DefaultFilePermissions); err != nil {
		t.Fatal(err)
	}
	return path
}

// mustNewFromMemory opens the archive in data, under the file name name,
// without writing it to disk, and closes it when the test ends.
func mustNewFromMemory(t *testing.T, name string, data []byte) FS {
	t.Helper()
	fsys, err := NewFromFile(t.Context(), name, archivetest.NewFile(name, data))
	if err != nil {
		t.Fatalf("NewFromFile(%q) = %v", name, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return fsys
}

// assertSameFiles fails the test unless a sample of files, one at the root and
// one nested, reads the same from got as from want. Every read of a compressed
// tar decompresses it again and allocates the whole dictionary, so comparing
// every file would dominate the test run.
func assertSameFiles(t *testing.T, got, want fs.FS) {
	t.Helper()
	for _, path := range []string{"index.html", "assets/deep/x/y/1.txt"} {
		wantData, err := fs.ReadFile(want, path)
		if err != nil {
			t.Fatalf("reference ReadFile(%q) = %v", path, err)
		}
		gotData, err := fs.ReadFile(got, path)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v", path, err)
			continue
		}
		if !bytes.Equal(gotData, wantData) {
			t.Errorf("ReadFile(%q) = %q, want %q", path, gotData, wantData)
		}
	}
}

func TestXzLargeDictionary(t *testing.T) {
	want, err := New(t.Context(), testXzArchive)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := want.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})

	// The decoder allocates the declared dictionary on every open, so these
	// subtests are not run in parallel.
	t.Run("96MiB local path", func(t *testing.T) {
		path := writeXzArchiveWithDict(t, "large-dict.tar.xz", archivetest.XzDict96MiB)
		got, err := New(t.Context(), path)
		if err != nil {
			t.Fatalf("New() = %v", err)
		}
		t.Cleanup(func() {
			if err := got.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		})
		assertSameFiles(t, got, want)
	})

	t.Run("96MiB open file", func(t *testing.T) {
		got := mustNewFromMemory(t, "large-dict.tar.xz", xzArchiveWithDict(t, archivetest.XzDict96MiB))
		assertSameFiles(t, got, want)
	})

	t.Run("256MiB open file", func(t *testing.T) {
		got := mustNewFromMemory(t, "large-dict.tar.xz", xzArchiveWithDict(t, archivetest.XzDict256MiB))
		assertSameFiles(t, got, want)
	})

	t.Run("name without xz extension", func(t *testing.T) {
		got := mustNewFromMemory(t, "large-dict.bin", xzArchiveWithDict(t, archivetest.XzDict96MiB))
		assertSameFiles(t, got, want)
	})
}

func TestXzDictionaryOverLimit(t *testing.T) {
	t.Parallel()
	path := writeXzArchiveWithDict(t, "huge-dict.tar.xz", archivetest.XzDict384MiB)
	fsys, err := New(t.Context(), path)
	if err == nil {
		if closeErr := fsys.Close(); closeErr != nil {
			t.Errorf("Close() = %v", closeErr)
		}
		t.Fatal("New() = nil, want an error for a 384 MiB dictionary")
	}
	if !errors.Is(err, fastxz.ErrMemlimit) {
		t.Errorf("New() = %v, want fastxz.ErrMemlimit", err)
	}

	// The failed mount must release the file so it can be removed on Windows.
	if err := os.Remove(path); err != nil {
		t.Errorf("Remove() after a failed mount = %v", err)
	}
}

func TestXzDecompressorDictMax(t *testing.T) {
	t.Parallel()
	data := xzArchiveWithDict(t, archivetest.XzDict96MiB)
	plain, err := osutil.ReadFile(testArchivesDir + "testassets.tar")
	if err != nil {
		t.Fatal(err)
	}

	t.Run("below declared dictionary", func(t *testing.T) {
		t.Parallel()
		r, err := xzDecompressor{dictMax: 64 << 20}.OpenReader(bytes.NewReader(data))
		if !errors.Is(err, fastxz.ErrMemlimit) {
			t.Errorf("OpenReader() = (%v, %v), want fastxz.ErrMemlimit", r, err)
		}
	})

	t.Run("at declared dictionary", func(t *testing.T) {
		t.Parallel()
		r, err := xzDecompressor{dictMax: 96 << 20}.OpenReader(bytes.NewReader(data))
		if err != nil {
			t.Fatalf("OpenReader() = %v", err)
		}
		got, err := io.ReadAll(r)
		if err != nil {
			t.Fatalf("ReadAll() = %v", err)
		}
		if !bytes.Equal(got, plain) {
			t.Errorf("decompressed %d bytes that differ from the %d byte tar", len(got), len(plain))
		}
		if err := r.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})

	t.Run("not xz", func(t *testing.T) {
		t.Parallel()
		if r, err := (xzDecompressor{dictMax: xzDictMax}).OpenReader(bytes.NewReader(plain)); err == nil {
			t.Errorf("OpenReader(tar) = %v, want an error", r)
		}
	})
}
