// Copyright 2026 Cloudfra
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

package readwrapfs

import (
	"embed"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"testing"
	"testing/fstest"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

var testMapFS = fstest.MapFS{
	"hello.txt":       {Data: []byte("hello world")},
	"dir/nested.txt":  {Data: []byte("nested content")},
	"dir/another.txt": {Data: []byte("another file")},
}

func makeTestStdFS(t *testing.T) types.ReadFS {
	t.Helper()
	fsys := FromFS(testMapFS)
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return fsys
}

func TestFromFSString(t *testing.T) {
	t.Parallel()
	fsys := FromFS(testMapFS)
	defer ufsTesting.ValidateClose(t, fsys)()

	want := `{"readWrapFS":"fstest.MapFS"}`
	if got := fsys.(interface{ String() string }).String(); got != want {
		t.Errorf("String() = %q, want %q", got, want)
	}
}

func TestFromFSOpen(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	f, err := fsys.Open("hello.txt")
	if err != nil {
		t.Fatalf("Open() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, f)()

	data, err := io.ReadAll(f)
	if err != nil {
		t.Fatalf("ReadAll() = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("Open() content = %q, want %q", data, "hello world")
	}
}

func TestFromFSOpenDir(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	f, err := fsys.Open("dir")
	if err != nil {
		t.Fatalf("Open(dir) = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, f)()

	info, err := f.Stat()
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Error("Open(dir).Stat().IsDir() = false, want true")
	}
}

func TestFromFSReadFile(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	data, err := fsys.ReadFile("hello.txt")
	if err != nil {
		t.Fatalf("ReadFile() = %v, want nil", err)
	}
	if string(data) != "hello world" {
		t.Errorf("ReadFile() = %q, want %q", data, "hello world")
	}
}

func TestFromFSReadDir(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	entries, err := fsys.ReadDir("dir")
	if err != nil {
		t.Fatalf("ReadDir() = %v, want nil", err)
	}
	if len(entries) != 2 {
		t.Errorf("ReadDir() returned %d entries, want 2", len(entries))
	}
}

func TestFromFSStat(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	info, err := fsys.Stat("hello.txt")
	if err != nil {
		t.Fatalf("Stat() = %v, want nil", err)
	}
	if info.IsDir() {
		t.Error("Stat(file).IsDir() = true, want false")
	}
	if info.Size() != int64(len("hello world")) {
		t.Errorf("Stat(file).Size() = %d, want %d", info.Size(), len("hello world"))
	}
}

func TestFromFSLstat(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	info, err := fsys.Lstat("hello.txt")
	if err != nil {
		t.Fatalf("Lstat() = %v, want nil", err)
	}
	if info.IsDir() {
		t.Error("Lstat(file).IsDir() = true, want false")
	}
}

func TestFromFSReadLink(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	_, err := fsys.ReadLink("hello.txt")
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("ReadLink() = %v, want fs.ErrInvalid", err)
	}
}

func TestFromFSCloseNoop(t *testing.T) {
	t.Parallel()
	fsys := FromFS(testMapFS)
	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v, want nil", err)
	}
}

type closableMapFS struct {
	fstest.MapFS
	closed bool
}

func (c *closableMapFS) Close() error {
	c.closed = true
	return nil
}

func TestFromFSCloseDelegates(t *testing.T) {
	t.Parallel()
	inner := &closableMapFS{MapFS: testMapFS}
	fsys := FromFS(inner)

	if err := fsys.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	if !inner.closed {
		t.Error("Close() did not delegate to underlying io.Closer")
	}
}

func TestFromFSForEachFilename(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	var files []string
	err := ops.ForEachFilename(fsys, ".", func(name string) error {
		files = append(files, name)
		return nil
	})
	if err != nil {
		t.Fatalf("ForEachFilename() = %v, want nil", err)
	}
	if len(files) != 3 {
		t.Errorf("ForEachFilename() found %d files, want 3: %v", len(files), files)
	}
}

func TestFromFSTestFS(t *testing.T) {
	t.Parallel()
	fsys := makeTestStdFS(t)

	if err := fstest.TestFS(fsys, "hello.txt", "dir/nested.txt"); err != nil {
		t.Errorf("fstest.TestFS: %v", err)
	}
}

func TestFromFSEmbedFS(t *testing.T) {
	t.Parallel()
	fsys := FromFS(ufsTesting.TestAssetsFS())
	defer ufsTesting.ValidateClose(t, fsys)()

	data, err := fsys.ReadFile("testassets/files/index.html")
	if err != nil {
		t.Fatalf("ReadFile() = %v, want nil", err)
	}
	if len(data) == 0 {
		t.Error("ReadFile() returned empty data, want non-empty")
	}
}

// TestFromFSIsSame checks that a readWrapFS reports the data of the file system
// it wraps, which the wrapped file system cannot do itself because it does not
// recognize the wrapper.
func TestFromFSIsSame(t *testing.T) {
	t.Parallel()

	inner := &closableMapFS{MapFS: testMapFS}
	wrapped := FromFS(inner)

	if !types.SameFS(wrapped, inner) {
		t.Error("a readWrapFS and the file system it wraps should report the same data")
	}
	if !types.SameFS(wrapped, FromFS(inner)) {
		t.Error("two readWrapFS over one file system should report the same data")
	}

	separate := &closableMapFS{MapFS: testMapFS}
	if types.SameFS(wrapped, FromFS(separate)) {
		t.Error("readWrapFS over different file systems should not report the same data")
	}
}

// errFS is a [fs.FS] whose URI reports an error.
type errFS struct {
	fstest.MapFS
	err error
}

func (fsys errFS) URI() (*url.URL, error) {
	return nil, fsys.err
}

// errInnerURI is what the failing file system in TestFromFSURI reports.
var errInnerURI = errors.New("inner file system cannot report a URI")

// TestFromFSURI checks that the read-only ufs.ro parameter reaches the URI of
// the wrapped file system and only that: a wrapper adds its own parameter to
// what [polyfills.URI] reports for the file system it wraps, so a wrapped [fs.FS]
// that cannot report a URI gets one derived from its type, and a wrapped type
// with no locatable source is an error rather than a nil URL.
func TestFromFSURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		inner   fs.FS
		wantURI string
		wantErr error
	}{
		{
			// A wrapped fs.FS that reports no URI of its own is not left without
			// one: polyfills.URI names the standard library type it can recognize.
			name:    "embed.FS derives embed:",
			inner:   embed.FS{},
			wantURI: "embed:?ufs.ro=true",
		},
		{
			// A wrapped types.URIGet that answers nil, nil is not an answer either:
			// the nil falls through to the type check, which cannot name this
			// fixture, so the read that used to dereference nil (#373) now ends in
			// an error naming the type.
			name:    "inner URI is nil",
			inner:   &uriFS{},
			wantErr: polyfills.ErrUnsupportedURI,
		},
		{
			// A fs.Sub result keeps no record of the file system it was cut from,
			// so no URI can name it.
			name:    "subordinate file system is unsupported",
			inner:   mustSub(t, testMapFS, "dir"),
			wantErr: polyfills.ErrUnsupportedURI,
		},
		{
			name:    "inner scheme is kept",
			inner:   &uriFS{URIValue: &url.URL{Scheme: "mem", Host: "x"}},
			wantURI: "mem://x?ufs.ro=true",
		},
		{
			// The wrapped parameters keep the order they were written in, not the
			// sorted order url.Values.Encode would produce, because a URI can be
			// signed over its exact query string.
			name:    "existing parameters are kept",
			inner:   &uriFS{URIValue: &url.URL{Scheme: "mem", RawQuery: "b=2&a=1"}},
			wantURI: "mem:?b=2&a=1&ufs.ro=true",
		},
		{
			name:    "ufs.ro is set once",
			inner:   &uriFS{URIValue: &url.URL{Scheme: "mem", RawQuery: "ufs.ro=false"}},
			wantURI: "mem:?ufs.ro=true",
		},
		{
			name:    "inner URI fails",
			inner:   errFS{err: errInnerURI},
			wantErr: errInnerURI,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			fsys := FromFS(tt.inner)
			defer ufsTesting.ValidateClose(t, fsys)()

			u, err := fsys.URI()
			if !errors.Is(err, tt.wantErr) {
				t.Fatalf("URI() error = %v, want %v", err, tt.wantErr)
			}
			if tt.wantErr != nil {
				if u != nil {
					t.Errorf("URI() = %v, want no URL with the error", u)
				}
			} else {
				if u == nil {
					t.Fatalf("URI() = nil, want %q", tt.wantURI)
				}
				if got := u.String(); got != tt.wantURI {
					t.Errorf("URI() = %q, want %q", got, tt.wantURI)
				}
				// The URI must survive being written out and read back, which is
				// what ufs.New does with it.
				reparsed, err := url.Parse(tt.wantURI)
				if err != nil {
					t.Fatalf("url.Parse(%q) = %v, want the reported URI to parse", tt.wantURI, err)
				}
				if got := reparsed.String(); got != tt.wantURI {
					t.Errorf("url.Parse(%q).String() = %q, want the URI to survive being written and read back", tt.wantURI, got)
				}
			}
		})
	}
}

// mustSub cuts name out of fsys, stopping the test if the standard library
// cannot, and returns the subordinate file system.
func mustSub(t *testing.T, fsys fs.FS, name string) fs.FS {
	t.Helper()
	sub, err := fs.Sub(fsys, name)
	if err != nil {
		t.Fatalf("fs.Sub(%q) = %v, want a subordinate file system", name, err)
	}
	return sub
}

// TestFromFSURIDoesNotEditInner checks that the wrapper adds its parameter to a
// copy: RawQuery is a plain field of the URL the wrapped file system returned,
// so writing it through would rewrite the URI that file system reports.
func TestFromFSURIDoesNotEditInner(t *testing.T) {
	t.Parallel()

	inner := &uriFS{URIValue: &url.URL{Scheme: "mem", Host: "x", RawQuery: "a=1"}}
	fsys := FromFS(inner)
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.URI(); err != nil {
		t.Fatalf("URI() = %v, want nil", err)
	}
	if got, want := inner.URIValue.String(), "mem://x?a=1"; got != want {
		t.Errorf("the wrapped file system reports %q after URI(), want %q: the wrapper must edit a copy", got, want)
	}
}

// uriFS is a [fs.FS] that reports a fixed URI, the way a ufs driver does.
type uriFS struct {
	fstest.MapFS
	URIValue *url.URL
}

func (fsys *uriFS) URI() (*url.URL, error) {
	return fsys.URIValue, nil
}
