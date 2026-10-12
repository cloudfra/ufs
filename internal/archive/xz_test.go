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
	"encoding/binary"
	"errors"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	fastxz "github.com/mikelolasagasti/xz"

	"github.com/cloudfra/ufs/internal/osutil"
)

const (
	testXzArchive = testArchivesDir + "testassets.tar.xz"

	// LZMA2 dictionary size property bytes, see section 5.3.1 of
	// https://tukaani.org/xz/xz-file-format.txt.
	xzDictProp96MiB  = 29
	xzDictProp256MiB = 32
	xzDictProp384MiB = 33
)

// writeXzArchiveWithDict copies the xz test archive into a temp directory as
// name, rewriting its first block header to declare the LZMA2 dictionary size
// encoded by dictProp. Declaring a larger dictionary than the encoder used is
// valid: the data still decodes, but the decoder must allow that size.
func writeXzArchiveWithDict(t *testing.T, name string, dictProp byte) string {
	t.Helper()
	data, err := osutil.ReadFile(testXzArchive)
	if err != nil {
		t.Fatal(err)
	}

	// The block header follows the 12 byte stream header. Its first byte
	// encodes its size and its last 4 bytes are a CRC32 of the rest.
	const streamHeaderSize = 12
	if len(data) < streamHeaderSize+1 {
		t.Fatalf("%s is too short to be an xz file", testXzArchive)
	}
	headerSize := (int(data[streamHeaderSize]) + 1) * 4
	if len(data) < streamHeaderSize+headerSize {
		t.Fatalf("%s has a truncated block header", testXzArchive)
	}
	header := data[streamHeaderSize : streamHeaderSize+headerSize]
	body := header[:headerSize-4]

	// LZMA2 filter: ID 0x21, 1 byte of properties holding the dictionary size.
	i := bytes.Index(body, []byte{0x21, 0x01})
	if i < 0 || i+2 >= len(body) {
		t.Fatalf("%s has no LZMA2 filter in its first block header", testXzArchive)
	}
	body[i+2] = dictProp
	binary.LittleEndian.PutUint32(header[headerSize-4:], crc32.ChecksumIEEE(body))

	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, data, osutil.DefaultFilePermissions); err != nil {
		t.Fatal(err)
	}
	return path
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
	testCases := []struct {
		name     string
		dictProp byte
	}{
		{name: "96MiB", dictProp: xzDictProp96MiB},
		{name: "256MiB", dictProp: xzDictProp256MiB},
	}
	for _, tc := range testCases {
		t.Run(tc.name+" local path", func(t *testing.T) {
			path := writeXzArchiveWithDict(t, "large-dict.tar.xz", tc.dictProp)
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
	}

	t.Run("96MiB open file", func(t *testing.T) {
		path := writeXzArchiveWithDict(t, "large-dict.tar.xz", xzDictProp96MiB)
		file, err := osutil.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := NewFromFile(t.Context(), filepath.Base(path), file)
		if err != nil {
			if closeErr := file.Close(); closeErr != nil {
				t.Errorf("Close() = %v", closeErr)
			}
			t.Fatalf("NewFromFile() = %v", err)
		}
		t.Cleanup(func() {
			if err := got.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
		})
		assertSameFiles(t, got, want)
	})

	t.Run("name without xz extension", func(t *testing.T) {
		path := writeXzArchiveWithDict(t, "large-dict.bin", xzDictProp96MiB)
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
}

func TestXzDictionaryOverLimit(t *testing.T) {
	t.Parallel()
	path := writeXzArchiveWithDict(t, "huge-dict.tar.xz", xzDictProp384MiB)
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
	path := writeXzArchiveWithDict(t, "large-dict.tar.xz", xzDictProp96MiB)
	data, err := osutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
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
