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

// Package archivetest builds archives in memory for tests, so that they do
// not have to write to disk.
package archivetest

import (
	"archive/zip"
	"bytes"
	"encoding/binary"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// LZMA2 dictionary size property bytes for XzWithDict, see section 5.3.1 of
// https://tukaani.org/xz/xz-file-format.txt.
const (
	XzDict96MiB  = 29
	XzDict256MiB = 32
	XzDict384MiB = 33
)

// Entry is one entry of an archive built by Zip. A Name ending in "/" is a
// directory entry and its Data is ignored.
type Entry struct {
	Name string
	Data []byte
}

// Zip returns a zip archive holding entries, in the order given.
func Zip(tb testing.TB, entries ...Entry) []byte {
	tb.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, entry := range entries {
		w, err := zw.Create(entry.Name)
		if err != nil {
			tb.Fatalf("zip Create(%q) = %v, want nil", entry.Name, err)
		}
		if strings.HasSuffix(entry.Name, "/") {
			continue
		}
		if _, err := w.Write(entry.Data); err != nil {
			tb.Fatalf("zip Write(%q) = %v, want nil", entry.Name, err)
		}
	}
	if err := zw.Close(); err != nil {
		tb.Fatalf("zip Close() = %v, want nil", err)
	}
	return buf.Bytes()
}

// ZipDir returns a zip archive of every file under the local directory dir,
// named by its slash-separated path relative to dir.
func ZipDir(tb testing.TB, dir string) []byte {
	tb.Helper()
	src := os.DirFS(filepath.Clean(dir))

	var entries []Entry
	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := fs.ReadFile(src, p)
		if err != nil {
			return err
		}
		entries = append(entries, Entry{Name: p, Data: data})
		return nil
	})
	if err != nil {
		tb.Fatalf("cannot read %q, %v", dir, err)
	}
	return Zip(tb, entries...)
}

// XzWithDict returns a copy of the xz stream xzData with its first block
// header rewritten to declare the LZMA2 dictionary size encoded by dictProp,
// one of the XzDict constants. Declaring a larger dictionary than the encoder
// used is valid: the data still decodes, but the decoder must allow that size.
func XzWithDict(tb testing.TB, xzData []byte, dictProp byte) []byte {
	tb.Helper()
	data := bytes.Clone(xzData)

	// The block header follows the 12 byte stream header. Its first byte
	// encodes its size and its last 4 bytes are a CRC32 of the rest.
	const streamHeaderSize = 12
	if len(data) < streamHeaderSize+1 {
		tb.Fatal("data is too short to be an xz stream")
	}
	headerSize := (int(data[streamHeaderSize]) + 1) * 4
	if len(data) < streamHeaderSize+headerSize {
		tb.Fatal("xz stream has a truncated block header")
	}
	header := data[streamHeaderSize : streamHeaderSize+headerSize]
	body := header[:headerSize-4]

	// LZMA2 filter: ID 0x21, 1 byte of properties holding the dictionary size.
	i := bytes.Index(body, []byte{0x21, 0x01})
	if i < 0 || i+2 >= len(body) {
		tb.Fatal("xz stream has no LZMA2 filter in its first block header")
	}
	body[i+2] = dictProp
	binary.LittleEndian.PutUint32(header[headerSize-4:], crc32.ChecksumIEEE(body))
	return data
}

// File is an in-memory fs.File that also supports Seek and ReadAt, which
// mounting an archive from an open file requires.
type File struct {
	*bytes.Reader
	name    string
	modTime time.Time
}

// NewFile returns a File named name that holds data.
func NewFile(name string, data []byte) *File {
	return &File{Reader: bytes.NewReader(data), name: name, modTime: time.Now()}
}

// Stat returns the name and size the file was created with.
func (f *File) Stat() (fs.FileInfo, error) {
	return fileInfo{name: f.name, size: f.Size(), modTime: f.modTime}, nil
}

// Close does nothing; the file holds no resources.
func (*File) Close() error { return nil }

type fileInfo struct {
	name    string
	size    int64
	modTime time.Time
}

func (fi fileInfo) Name() string       { return fi.name }
func (fi fileInfo) Size() int64        { return fi.size }
func (fi fileInfo) Mode() fs.FileMode  { return 0o444 }
func (fi fileInfo) ModTime() time.Time { return fi.modTime }
func (fi fileInfo) IsDir() bool        { return false }
func (fi fileInfo) Sys() any           { return nil }
