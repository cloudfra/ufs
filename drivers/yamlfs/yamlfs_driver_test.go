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

package yamlfs

import (
	"errors"
	"io/fs"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// TestYamlFSDriver runs the shared conformance suites over a read-only
// document tree.
func TestYamlFSDriver(t *testing.T) {
	ufsdriversTesting.FS(t, ufsdriversTesting.Args{ReadOnly: true}, func(t *testing.T) types.SimpleFS {
		return newTestFS(t, "document.yaml", []byte(document))
	})
}

// writeDocument writes document into dir under name and returns the host path
// of the file it wrote.
func writeDocument(t *testing.T, dir, name string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := osutil.WriteFile(path, []byte(document)); err != nil {
		t.Fatalf("WriteFile(%q) = %v, want nil", path, err)
	}
	return path
}

// documentURI returns the yaml: URI that names the document at the host path
// path.
func documentURI(path string) string {
	return yamlFSPrefix + "//" + path
}

// tree lists every entry of fsys, a file with the content it holds and a
// directory with a trailing separator, so that two file systems can be compared
// for holding one document.
func tree(t *testing.T, fsys fs.FS) []string {
	t.Helper()
	var got []string
	err := fs.WalkDir(fsys, pathutil.CwdPath, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			got = append(got, name+"/")
			return nil
		}
		content, err := fs.ReadFile(fsys, name)
		if err != nil {
			return err
		}
		got = append(got, name+"="+string(content))
		return nil
	})
	if err != nil {
		t.Fatalf("fs.WalkDir() = %v, want nil", err)
	}
	return got
}

func TestIsYamlFSURI(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		uri  string
		want bool
	}{
		{uri: "yaml:", want: true},
		{uri: "yaml://", want: true},
		{uri: "yaml:///srv/config.yaml", want: true},
		{uri: "yaml:///config.yaml?dir=memory%3A", want: true},
		{uri: "yaml", want: false},
		{uri: "yamlx:///config.yaml", want: false},
		{uri: "YAML:///config.yaml", want: false},
		{uri: "/srv/config.yaml", want: false},
		{uri: "archive:///srv/config.yaml", want: false},
		{uri: "memory:", want: false},
	} {
		if got := isYamlFSURI(tc.uri); got != tc.want {
			t.Errorf("isYamlFSURI(%q) = %t, want %t", tc.uri, got, tc.want)
		}
	}
}

// TestDriverRegistered opens a document the way a binary that only imports this
// package does, through [ufs.New], so it fails if the driver stops being
// registered under the yaml: scheme.
func TestDriverRegistered(t *testing.T) {
	t.Parallel()
	path := writeDocument(t, t.TempDir(), "config.yaml")
	uri := documentURI(path)

	fsys, err := ufs.New(t.Context(), uri)
	if err != nil {
		t.Fatalf("ufs.New(%q) = %v, want nil", uri, err)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}()

	got, err := fs.ReadFile(fsys, "server/host")
	if err != nil {
		t.Errorf("ReadFile(server/host) = %v, want nil", err)
	} else if string(got) != "localhost" {
		t.Errorf("ReadFile(server/host) = %q, want %q", got, "localhost")
	}
	if _, err := fsys.Create("name"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Create(name) = %v, want fs.ErrPermission", err)
	}
}

// TestNewYamlFSFromURI checks that the file system the URI constructor builds
// holds the same document as [New] builds from its bytes, and that the address
// it reports names that document back.
func TestNewYamlFSFromURI(t *testing.T) {
	t.Parallel()
	path := writeDocument(t, t.TempDir(), "config.yaml")
	uri := documentURI(path)

	fsys, err := newYamlFS(t.Context(), uri)
	if err != nil {
		t.Fatalf("newYamlFS(%q) = %v, want nil", uri, err)
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}()

	fromBytes := newTestFS(t, filepath.Base(path), []byte(document))
	if got, want := tree(t, fsys), tree(t, fromBytes); !slices.Equal(got, want) {
		t.Errorf("newYamlFS(%q) holds %q, want the tree New holds: %q", uri, got, want)
	}

	// The address of this file system is the URI that named it with ufs's
	// read-only marker added. Opening that address gives a file system that
	// reports the same address, which is what keeps the URI from drifting
	// between one open and the next.
	u, err := fsys.URI()
	if err != nil {
		t.Fatalf("URI() = %v, want nil", err)
	}
	wantURI := uri + "?ufs.ro=true"
	if got := u.String(); got != wantURI {
		t.Fatalf("URI() = %q, want %q", got, wantURI)
	}
	again, err := newYamlFS(t.Context(), wantURI)
	if err != nil {
		t.Fatalf("newYamlFS(%q) = %v, want nil", wantURI, err)
	}
	defer func() {
		if err := again.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	}()
	if u2, err := again.URI(); err != nil {
		t.Errorf("URI() of the reopened file system = %v, want nil", err)
	} else if got := u2.String(); got != wantURI {
		t.Errorf("URI() of the reopened file system = %q, want %q", got, wantURI)
	}
	if got, want := tree(t, again), tree(t, fsys); !slices.Equal(got, want) {
		t.Errorf("the reopened file system holds %q, want %q", got, want)
	}
}

// TestDocumentPath checks the host path a yaml: URI names, and the addresses it
// refuses.
func TestDocumentPath(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		uri     string
		want    string
		wantErr string
	}{
		{uri: "yaml:///srv/config.yaml", want: "/srv/config.yaml"},
		{uri: "yaml:///srv/config.yaml?dir=memory%3A", want: "/srv/config.yaml"},
		{uri: "yaml:///srv/config.yaml?ufs.ro=true", want: "/srv/config.yaml"},
		{uri: "yaml:", wantErr: "does not name a document path"},
		{uri: "yaml://", wantErr: "does not name a document path"},
		{uri: "yaml:config.yaml", wantErr: "does not name a document path"},
		{uri: "yaml://host/srv/config.yaml", wantErr: `names the host "host"`},
		{uri: "archive:///srv/config.yaml", wantErr: "does not use the yaml: scheme"},
	} {
		got, err := documentPath(tc.uri)
		if tc.wantErr != "" {
			if err == nil {
				t.Errorf("documentPath(%q) = %q, want an error containing %q", tc.uri, got, tc.wantErr)
				continue
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("documentPath(%q) = %v, want an error containing %q", tc.uri, err, tc.wantErr)
			}
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Errorf("documentPath(%q) = %v, want a *fs.PathError", tc.uri, err)
			}
			continue
		}
		if err != nil {
			t.Errorf("documentPath(%q) = %v, want %q", tc.uri, err, tc.want)
			continue
		}
		if got != tc.want {
			t.Errorf("documentPath(%q) = %q, want %q", tc.uri, got, tc.want)
		}
	}
}

// TestNewYamlFSErrors checks the addresses the driver refuses through [ufs.New],
// which also shows that the yaml: driver is the one that answers them: the
// local driver matches a name with no authority separator too, and it is this
// driver's higher priority that decides.
func TestNewYamlFSErrors(t *testing.T) {
	t.Parallel()
	missing := filepath.Join(t.TempDir(), "missing.yaml")
	for _, tc := range []struct {
		uri     string
		wantErr string
		not     error
	}{
		{uri: "yaml:", wantErr: "does not name a document path"},
		{uri: "yaml:config.yaml", wantErr: "does not name a document path"},
		{uri: "yaml://host/srv/config.yaml", wantErr: `names the host "host"`},
		{uri: documentURI(missing), wantErr: missing, not: fs.ErrNotExist},
	} {
		fsys, err := ufs.New(t.Context(), tc.uri)
		if err == nil {
			t.Errorf("ufs.New(%q) = nil, want an error", tc.uri)
			if err := fsys.Close(); err != nil {
				t.Errorf("Close() = %v", err)
			}
			continue
		}
		if want := `with driver "yaml"`; !strings.Contains(err.Error(), want) {
			t.Errorf("ufs.New(%q) = %v, want an error containing %q", tc.uri, err, want)
		}
		if !strings.Contains(err.Error(), tc.wantErr) {
			t.Errorf("ufs.New(%q) = %v, want an error containing %q", tc.uri, err, tc.wantErr)
		}
		if tc.not != nil && !errors.Is(err, tc.not) {
			t.Errorf("ufs.New(%q) = %v, want it to match %v", tc.uri, err, tc.not)
		}
	}
}
