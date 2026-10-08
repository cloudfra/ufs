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

package archivetest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"testing"
)

// zipContents returns the entry names of the zip in data, in order, and the
// contents of its files.
func zipContents(t *testing.T, data []byte) ([]string, map[string]string) {
	t.Helper()
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatalf("zip.NewReader() = %v", err)
	}
	var names []string
	files := map[string]string{}
	for _, zf := range zr.File {
		names = append(names, zf.Name)
		if zf.FileInfo().IsDir() {
			continue
		}
		rc, err := zf.Open()
		if err != nil {
			t.Fatal(err)
		}
		content, err := io.ReadAll(rc)
		if err != nil {
			t.Fatal(err)
		}
		if err := rc.Close(); err != nil {
			t.Fatal(err)
		}
		files[zf.Name] = string(content)
	}
	return names, files
}

func TestZip(t *testing.T) {
	t.Parallel()
	data := Zip(t,
		Entry{Name: "b.txt", Data: []byte("bee")},
		Entry{Name: "dir/", Data: []byte("ignored")},
		Entry{Name: "dir/a.txt", Data: []byte("ay")},
		Entry{Name: "empty.txt"},
	)
	names, files := zipContents(t, data)
	if want := []string{"b.txt", "dir/", "dir/a.txt", "empty.txt"}; !slices.Equal(names, want) {
		t.Errorf("entries = %v, want %v", names, want)
	}
	for name, want := range map[string]string{"b.txt": "bee", "dir/a.txt": "ay", "empty.txt": ""} {
		if got, ok := files[name]; !ok || got != want {
			t.Errorf("%s = %q (present %t), want %q", name, got, ok, want)
		}
	}
}

func TestZipEmpty(t *testing.T) {
	t.Parallel()
	names, _ := zipContents(t, Zip(t))
	if len(names) != 0 {
		t.Errorf("entries = %v, want none", names)
	}
}

func TestZipDir(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "sub"), 0o700); err != nil {
		t.Fatal(err)
	}
	for name, content := range map[string]string{"root.txt": "root", filepath.Join("sub", "nested.txt"): "nested"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	names, files := zipContents(t, ZipDir(t, dir))
	if want := []string{"root.txt", "sub/nested.txt"}; !slices.Equal(names, want) {
		t.Errorf("entries = %v, want %v", names, want)
	}
	if files["root.txt"] != "root" || files["sub/nested.txt"] != "nested" {
		t.Errorf("files = %v, want the contents written", files)
	}
}

// testXzBlockHeader is an xz stream header followed by a 20 byte block header
// with an LZMA2 filter declaring a 64 MiB dictionary (property byte 28).
func testXzBlockHeader() []byte {
	data := []byte{0xfd, 0x37, 0x7a, 0x58, 0x5a, 0x00, 0x00, 0x04, 0xe6, 0xd6, 0xb4, 0x46}
	body := []byte{0x04, 0xc0, 0xc1, 0xc6, 0x08, 0x80, 0x90, 0x12, 0x21, 0x01, 28, 0x00, 0x00, 0x00, 0x00, 0x00}
	data = append(data, body...)
	data = binary.LittleEndian.AppendUint32(data, crc32.ChecksumIEEE(body))
	return append(data, "payload"...)
}

func TestXzWithDict(t *testing.T) {
	t.Parallel()
	original := testXzBlockHeader()
	pristine := bytes.Clone(original)

	got := XzWithDict(t, original, XzDict96MiB)

	if !bytes.Equal(original, pristine) {
		t.Error("XzWithDict() changed its input")
	}
	if len(got) != len(original) {
		t.Fatalf("len = %d, want %d", len(got), len(original))
	}
	const dictPropOffset = 12 + 10
	if got[dictPropOffset] != XzDict96MiB {
		t.Errorf("dictionary property = %d, want %d", got[dictPropOffset], XzDict96MiB)
	}
	header := got[12:32]
	if crc := binary.LittleEndian.Uint32(header[16:]); crc != crc32.ChecksumIEEE(header[:16]) {
		t.Errorf("block header CRC32 = %#x, want %#x", crc, crc32.ChecksumIEEE(header[:16]))
	}
	// Only the property byte and the CRC differ.
	for i := range got {
		if got[i] != original[i] && i != dictPropOffset && (i < 28 || i > 31) {
			t.Errorf("byte %d changed from %#x to %#x", i, original[i], got[i])
		}
	}
}

func TestFile(t *testing.T) {
	t.Parallel()
	var f fs.File = NewFile("a.zip", []byte("hello world"))

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v", err)
	}
	if info.Name() != "a.zip" || info.Size() != 11 || info.IsDir() || !info.Mode().IsRegular() {
		t.Errorf("Stat() = %q, %d bytes, dir %t, mode %v; want a regular file a.zip of 11 bytes", info.Name(), info.Size(), info.IsDir(), info.Mode())
	}
	if info.ModTime().IsZero() || info.Sys() != nil {
		t.Errorf("Stat() ModTime = %v, Sys = %v; want a set time and nil", info.ModTime(), info.Sys())
	}

	got, err := io.ReadAll(f)
	if err != nil || string(got) != "hello world" {
		t.Errorf("ReadAll() = (%q, %v), want %q", got, err, "hello world")
	}

	seeker, ok := f.(io.ReadSeeker)
	if !ok {
		t.Fatal("File does not implement io.ReadSeeker")
	}
	if _, err := seeker.Seek(6, io.SeekStart); err != nil {
		t.Fatal(err)
	}
	if got, err := io.ReadAll(f); err != nil || string(got) != "world" {
		t.Errorf("ReadAll() after Seek = (%q, %v), want %q", got, err, "world")
	}

	readerAt, ok := f.(io.ReaderAt)
	if !ok {
		t.Fatal("File does not implement io.ReaderAt")
	}
	buf := make([]byte, 5)
	if n, err := readerAt.ReadAt(buf, 0); n != 5 || err != nil || string(buf) != "hello" {
		t.Errorf("ReadAt(0) = (%d, %v) %q, want (5, nil) %q", n, err, buf, "hello")
	}

	if err := f.Close(); err != nil {
		t.Errorf("Close() = %v", err)
	}
}
