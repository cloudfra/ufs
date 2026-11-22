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

package polyfills

import (
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
)

// TestURI checks the polyfill a wrapper now goes through instead of inventing an
// answer of its own: the URI a file system reports for itself first, then one
// derived from a standard library type whose source a type assertion reveals,
// then an error naming the type. Never a nil URL with no error.
func TestURI(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		fsys    fs.FS
		wantURI string
		wantErr error
	}{
		{
			name:    "the file system reports its own URI",
			fsys:    uriFS{path: "mem://one"},
			wantURI: "mem://one",
		},
		{
			// A nil answer is a method declining to answer, not evidence that no
			// source exists, so the lookup continues and ends in the unsupported
			// error naming the type.
			name:    "a nil answer falls through to the type check",
			fsys:    nilURIFS{},
			wantErr: ErrUnsupportedURI,
		},
		{
			name:    "an embed.FS is embedded data",
			fsys:    embed.FS{},
			wantURI: "embed:",
		},
		{
			// fs.Sub records nothing about the file system it was cut from, so no
			// URI can name it. An error is the honest answer.
			name:    "a subordinate file system is unsupported",
			fsys:    mustSub(t, embed.FS{}, "dir"),
			wantErr: ErrUnsupportedURI,
		},
		{
			name:    "an unknown type is named in the error",
			fsys:    unknownFS{},
			wantErr: ErrUnsupportedURI,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, err := URI(tt.fsys)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("URI(%T) error = %v, want %v", tt.fsys, err, tt.wantErr)
				}
				if u != nil {
					t.Errorf("URI(%T) = %v, want no URL with the error", tt.fsys, u)
				}
				// The error is only useful if it names what could not be placed,
				// which is what a caller sees when a wrapper refuses.
				if named := fmt.Sprintf("%T", tt.fsys); !strings.Contains(err.Error(), named) {
					t.Errorf("URI(%T) error = %q, want it to name the type %q", tt.fsys, err, named)
				}
				return
			}
			if err != nil {
				t.Fatalf("URI(%T) = %v, want %q", tt.fsys, err, tt.wantURI)
			}
			if u == nil {
				t.Fatalf("URI(%T) = nil, want %q", tt.fsys, tt.wantURI)
			}
			if got := u.String(); got != tt.wantURI {
				t.Errorf("URI(%T) = %q, want %q", tt.fsys, got, tt.wantURI)
			}
			// The URI must survive being written out and read back, which is what
			// a caller storing it as configuration does with it.
			reparsed, err := url.Parse(tt.wantURI)
			if err != nil {
				t.Fatalf("url.Parse(%q) = %v, want the reported URI to parse", tt.wantURI, err)
			}
			if got := reparsed.String(); got != tt.wantURI {
				t.Errorf("url.Parse(%q).String() = %q, want the URI to survive being written and read back", tt.wantURI, got)
			}
		})
	}
}

// TestURIOSRoot covers the one value URI accepts that is not an [io/fs.FS]: the
// standard library's directory root. It is not an [io/fs.FS] because its Open
// returns a [*os.File], yet it names the directory it was opened on, so the URI
// it resolves to is the file:// URI ufs.New takes back.
func TestURIOSRoot(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	root, err := os.OpenRoot(dir)
	if err != nil {
		t.Fatalf("os.OpenRoot(%q) = %v, want a root over the test directory", dir, err)
	}
	t.Cleanup(func() {
		if err := root.Close(); err != nil {
			t.Errorf("root.Close() = %v, want no error", err)
		}
	})

	// The expected spelling is the one drivers/core reports for a local file
	// system, so a root and a localFS over the same directory agree.
	wantURL := &url.URL{Scheme: "file", Path: pathutil.CoerceUnix(dir)}

	tests := []struct {
		name    string
		fsys    any
		wantURL *url.URL
		wantErr error
	}{
		{
			name:    "a root names its directory",
			fsys:    root,
			wantURL: wantURL,
		},
		{
			// The io/fs.FS view of a root keeps the directory name to itself, so
			// asking for the URI of the root rather than of that view is the
			// difference between a file:// URI and no answer at all.
			name:    "the FS view of a root reveals no source",
			fsys:    root.FS(),
			wantErr: ErrUnsupportedURI,
		},
		{
			// A typed nil has no receiver to read a name from, so it is reported
			// rather than dereferenced.
			name:    "a nil root has no name",
			fsys:    (*os.Root)(nil),
			wantErr: ErrUnsupportedURI,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			u, err := URI(tt.fsys)
			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("URI(%T) error = %v, want %v", tt.fsys, err, tt.wantErr)
				}
				if u != nil {
					t.Errorf("URI(%T) = %v, want no URL with the error", tt.fsys, u)
				}
				if named := fmt.Sprintf("%T", tt.fsys); !strings.Contains(err.Error(), named) {
					t.Errorf("URI(%T) error = %q, want it to name the type %q", tt.fsys, err, named)
				}
				return
			}
			if err != nil {
				t.Fatalf("URI(%T) = %v, want %q", tt.fsys, err, tt.wantURL)
			}
			if u == nil {
				t.Fatalf("URI(%T) = nil, want %q", tt.fsys, tt.wantURL)
			}
			if got := u.String(); got != tt.wantURL.String() {
				t.Errorf("URI(%T) = %q, want %q", tt.fsys, got, tt.wantURL)
			}
			if u.Scheme != "file" {
				t.Errorf("URI(%T) scheme = %q, want %q", tt.fsys, u.Scheme, "file")
			}
			if u.Path != pathutil.CoerceUnix(dir) {
				t.Errorf("URI(%T) path = %q, want the directory it was opened on, %q", tt.fsys, u.Path, pathutil.CoerceUnix(dir))
			}
			if !u.IsAbs() {
				t.Errorf("URI(%T) = %q, want an absolute URL a reopen can find", tt.fsys, u)
			}
		})
	}
}

// TestURIReturnsACopy covers the URL a caller gets back: a driver that builds
// its URI once keeps that URL for itself, so a wrapper editing what it was
// handed must not change what the driver reports to anyone else.
func TestURIReturnsACopy(t *testing.T) {
	t.Parallel()

	fsys := pinnedURIFS{u: &url.URL{Scheme: "mem", Host: "one", RawQuery: "a=1"}}
	got, err := URI(fsys)
	if err != nil {
		t.Fatalf("URI(%T) = %v, want a URL", fsys, err)
	}
	got.Host = "two"
	got.RawQuery = "a=2"

	again, err := URI(fsys)
	if err != nil {
		t.Fatalf("URI(%T) = %v, want a URL", fsys, err)
	}
	if want := "mem://one?a=1"; again.String() != want {
		t.Errorf("URI(%T) = %q after the caller edited an earlier result, want %q", fsys, again, want)
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

// unknownFS is a file system of no type URI can place.
type unknownFS struct{}

func (unknownFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

// uriFS is a file system that reports a fixed URI, the way a ufs driver does.
type uriFS struct {
	path string
}

func (uriFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (u uriFS) URI() (*url.URL, error) {
	return url.Parse(u.path)
}

// nilURIFS is a file system whose URI method declines to answer.
type nilURIFS struct{}

func (nilURIFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (nilURIFS) URI() (*url.URL, error) {
	return nil, nil
}

// pinnedURIFS reports one stored URL every time it is asked, the way a driver
// that builds its URI at construction does.
type pinnedURIFS struct {
	u *url.URL
}

func (pinnedURIFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func (fsys pinnedURIFS) URI() (*url.URL, error) {
	return fsys.u, nil
}
