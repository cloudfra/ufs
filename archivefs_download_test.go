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
	"bytes"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/cloudfra/ufs/internal/httputil"
	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

func TestNewRemoteArchive(t *testing.T) {
	fsys, err := New(t.Context(), "https://github.com/mholt/archives/archive/refs/heads/main.zip")
	if err != nil {
		t.Error(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	if files, err := fsys.ReadDir(CwdPath); files != nil {
		t.Logf("files: %v, err: %s", files, err)
	}
	if files, err := fsys.ReadDir("archives-main"); files != nil {
		t.Logf("files: %v, err: %s", files, err)
	}
}

func testArchiveServer(t *testing.T) *httptest.Server {
	t.Helper()
	zipPath := createZipFromDir(t, testAssetsFilesDir)
	zipData, err := osutil.ReadFile(zipPath)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/testassets.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		if _, err := w.Write(zipData); err != nil {
			t.Errorf("failed to write to response: %v", err)
		}
	})
	mux.HandleFunc("/404.zip", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/500.zip", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	})
	mux.HandleFunc("/redirect-to-archive", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/testassets.zip", http.StatusFound)
	})
	mux.HandleFunc("/trailing-slash/", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("bad")); err != nil {
			t.Errorf("failed to write to response: %v", err)
		}
	})
	mux.HandleFunc("/redirect-to-traversal", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/../../etc/passwd", http.StatusFound)
	})
	mux.HandleFunc("/../../etc/passwd", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write([]byte("root:x:0:0")); err != nil {
			t.Errorf("failed to write to response: %v", err)
		}
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	return ts
}

func testDownloadAndMount(t *testing.T, ts *httptest.Server, urlPath string) FS {
	t.Helper()
	ctx := t.Context()
	client := ts.Client()
	dir := t.TempDir()

	archivePath, err := httputil.DownloadFileWith(ctx, client, dir, ts.URL+urlPath)
	if err != nil {
		t.Fatalf("httputil.DownloadFileWith(%q) = %v", urlPath, err)
	}
	fsys, err := newArchiveFSFromLocalFS(ctx, archivePath)
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS() = %v", err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v", err)
		}
	})
	return fsys
}

func TestDownloadFileAndMount(t *testing.T) {
	t.Parallel()
	ts := testArchiveServer(t)
	wantFiles := loadTestAssets(t)

	fsys := testDownloadAndMount(t, ts, "/testassets.zip")
	for filePath, wantData := range wantFiles {
		t.Run(filePath, func(t *testing.T) {
			t.Parallel()
			got, err := fs.ReadFile(fsys, filePath)
			if err != nil {
				t.Fatalf("ReadFile(%q) = %v", filePath, err)
			}
			if !bytes.Equal(got, wantData) {
				t.Errorf("ReadFile(%q): got %d bytes, want %d bytes", filePath, len(got), len(wantData))
			}
		})
	}
}

func TestDownloadFileAndMountRedirect(t *testing.T) {
	t.Parallel()
	ts := testArchiveServer(t)

	fsys := testDownloadAndMount(t, ts, "/redirect-to-archive")
	entries, err := fsys.ReadDir(CwdPath)
	if err != nil {
		t.Fatalf("ReadDir(\".\") = %v", err)
	}
	if len(entries) == 0 {
		t.Error("ReadDir(\".\") returned no entries, want at least one")
	}
}

func TestDownloadFileAndMountReadDir(t *testing.T) {
	t.Parallel()
	ts := testArchiveServer(t)

	fsys := testDownloadAndMount(t, ts, "/testassets.zip")
	entries, err := fsys.ReadDir("assets")
	if err != nil {
		t.Fatalf("ReadDir(\"assets\") = %v", err)
	}
	if len(entries) == 0 {
		t.Error("ReadDir(\"assets\") returned no entries, want at least one")
	}
}

func TestDownloadFileAndMountStat(t *testing.T) {
	t.Parallel()
	ts := testArchiveServer(t)

	fsys := testDownloadAndMount(t, ts, "/testassets.zip")
	info, err := fsys.Stat("index.html")
	if err != nil {
		t.Fatalf("Stat(\"index.html\") = %v", err)
	}
	if info.IsDir() {
		t.Error("Stat(\"index.html\").IsDir() = true, want false")
	}
	if info.Size() == 0 {
		t.Error("Stat(\"index.html\").Size() = 0, want > 0")
	}
}

func TestNewRemoteArchiveSSRFBlocked(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		uri  string
	}{
		{"loopback", "http://127.0.0.1/evil.zip"},
		{"private 10", "http://10.0.0.1/evil.zip"},
		{"metadata", "http://169.254.169.254/latest/meta-data/"},
		{"ipv6 loopback", "http://[::1]/evil.zip"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := New(t.Context(), tc.uri)
			if err == nil {
				t.Fatalf("New(%q) should have been blocked", tc.uri)
			}
		})
	}
}
