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
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCreateOSTempDirectory(t *testing.T) {
	dir, cleanup, err := createOSTempDirectory()
	if err != nil {
		t.Error(err)
	}
	if !osExists(dir) {
		t.Errorf("'%s' does not exist when it should", dir)
	}

	if !strings.Contains(dir, "goapp") {
		t.Errorf("'%s' does not contain 'goapp'", dir)
	}
	cleanup()
	if osExists(dir) {
		t.Errorf("'%s' exists when it should not", dir)
	}
}

func TestOSDeleteFile(t *testing.T) {
	t.Run("nonexistent", func(t *testing.T) {
		err := osDeleteFile("/nonexistent/path/that/cannot/exist-" + t.Name() + ".txt")
		if err != nil {
			t.Errorf("osDeleteFile(nonexistent) = %v, want nil", err)
		}
	})

	t.Run("existing", func(t *testing.T) {
		f, err := os.CreateTemp("", "ufs-osutil-test-*.txt")
		if err != nil {
			t.Fatal(err)
		}
		p := f.Name()
		f.Close()

		if err := osDeleteFile(p); err != nil {
			t.Errorf("osDeleteFile(existing) = %v, want nil", err)
		}
		if osExists(p) {
			t.Errorf("%q still exists after osDeleteFile", p)
		}
	})
}

func TestTryOSDeleteFile(t *testing.T) {
	f, err := os.CreateTemp("", "ufs-try-delete-*.txt")
	if err != nil {
		t.Fatal(err)
	}
	p := f.Name()
	f.Close()

	tryOSDeleteFile(p)
	if osExists(p) {
		t.Errorf("%q still exists after tryOSDeleteFile", p)
	}
}

func TestOSDeleteDirectoryExists(t *testing.T) {
	dir, err := os.MkdirTemp("", "ufs-del-dir-*")
	if err != nil {
		t.Fatal(err)
	}
	if err := osDeleteDirectory(dir); err != nil {
		t.Errorf("osDeleteDirectory(existing) = %v, want nil", err)
	}
	if osExists(dir) {
		t.Errorf("%q still exists after osDeleteDirectory", dir)
	}
}

func TestTryOSDeleteDirectory(t *testing.T) {
	dir, err := os.MkdirTemp("", "ufs-try-del-dir-*")
	if err != nil {
		t.Fatal(err)
	}
	tryOSDeleteDirectory(dir)
	if osExists(dir) {
		t.Errorf("%q still exists after tryOSDeleteDirectory", dir)
	}
}

func TestNewRemoteArchive(t *testing.T) {
	fsys, err := New(t.Context(), "https://github.com/mholt/archives/archive/refs/heads/main.zip")
	if err != nil {
		t.Error(err)
	}
	defer fsys.Close()

	if files, err := fsys.ReadDir(cwdPath); files != nil {
		t.Logf("files: %v, err: %s", files, err)
	}
	if files, err := fsys.ReadDir("archives-main"); files != nil {
		t.Logf("files: %v, err: %s", files, err)
	}
}

func TestIsBlockedIP(t *testing.T) {
	t.Parallel()
	tests := []struct {
		ip   string
		want bool
	}{
		{"127.0.0.1", true},
		{"127.0.0.2", true},
		{"10.0.0.1", true},
		{"10.255.255.255", true},
		{"172.16.0.1", true},
		{"172.31.255.255", true},
		{"192.168.0.1", true},
		{"192.168.1.100", true},
		{"169.254.169.254", true},
		{"0.0.0.0", true},
		{"::1", true},
		{"fe80::1", true},
		{"fc00::1", true},
		{"fd00::1", true},

		{"8.8.8.8", false},
		{"1.1.1.1", false},
		{"172.15.0.1", false},
		{"172.32.0.1", false},
		{"192.169.0.1", false},
		{"11.0.0.1", false},
		{"2001:db8::1", false},
	}
	for _, tc := range tests {
		t.Run(tc.ip, func(t *testing.T) {
			t.Parallel()
			ip := net.ParseIP(tc.ip)
			if ip == nil {
				t.Fatalf("net.ParseIP(%q) = nil", tc.ip)
			}
			if got := isBlockedIP(ip); got != tc.want {
				t.Errorf("isBlockedIP(%s) = %v, want %v", tc.ip, got, tc.want)
			}
		})
	}
}

func TestValidateDownloadURL(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rawURL  string
		wantErr bool
	}{
		{"https valid", "https://example.com/file.zip", false},
		{"http valid", "http://example.com/file.zip", false},
		{"ftp rejected", "ftp://example.com/file.zip", true},
		{"file rejected", "file:///etc/passwd", true},
		{"empty host", "http:///path", true},
		{"loopback v4", "http://127.0.0.1/file.zip", true},
		{"loopback v6", "http://[::1]/file.zip", true},
		{"private 10", "http://10.0.0.1/file.zip", true},
		{"private 172.16", "http://172.16.0.1/file.zip", true},
		{"private 192.168", "http://192.168.1.1/file.zip", true},
		{"link-local", "http://169.254.169.254/latest/meta-data/", true},
		{"unspecified", "http://0.0.0.0/file.zip", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u, err := url.Parse(tc.rawURL)
			if err != nil {
				t.Fatalf("url.Parse(%q) = %v", tc.rawURL, err)
			}
			err = validateDownloadURL(t.Context(), u)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateDownloadURL(%q) error = %v, wantErr = %v", tc.rawURL, err, tc.wantErr)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		path    string
		want    string
		wantErr bool
	}{
		{"simple", "/archive/file.zip", "file.zip", false},
		{"nested", "/a/b/c/data.tar.gz", "data.tar.gz", false},
		{"single component", "/file.zip", "file.zip", false},
		{"empty last component", "/path/to/", "", true},
		{"dot", "/path/.", "", true},
		{"dotdot", "/path/..", "", true},
		{"root only", "/", "", true},
		{"empty path", "", "", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			u := &url.URL{Scheme: "https", Host: "example.com", Path: tc.path}
			got, err := sanitizeFilename(u)
			if (err != nil) != tc.wantErr {
				t.Errorf("sanitizeFilename(%q) error = %v, wantErr = %v", tc.path, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.path, got, tc.want)
			}
		})
	}
}

func TestDialControl(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		address string
		wantErr bool
	}{
		{"public", "93.184.216.34:443", false},
		{"loopback", "127.0.0.1:80", true},
		{"private", "10.0.0.1:80", true},
		{"link-local", "169.254.169.254:80", true},
		{"ipv6 loopback", "[::1]:80", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := dialControl("tcp", tc.address, nil)
			if (err != nil) != tc.wantErr {
				t.Errorf("dialControl(tcp, %q) error = %v, wantErr = %v", tc.address, err, tc.wantErr)
			}
		})
	}
}

func testArchiveServer(t *testing.T) *httptest.Server {
	t.Helper()
	zipPath := createZipFromDir(t, testAssetsFilesDir)
	zipData, err := os.ReadFile(zipPath)
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

func TestDownloadFile(t *testing.T) {
	t.Parallel()
	ts := testArchiveServer(t)
	client := ts.Client()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := downloadFileWith(t.Context(), client, dir, ts.URL+"/testassets.zip")
		if err != nil {
			t.Fatalf("downloadFileWith() = %v", err)
		}
		if filepath.Base(path) != "testassets.zip" {
			t.Errorf("filename = %q, want %q", filepath.Base(path), "testassets.zip")
		}
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile() = %v", err)
		}
		if len(data) == 0 {
			t.Error("downloaded file is empty")
		}
	})

	t.Run("404 status", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := downloadFileWith(t.Context(), client, dir, ts.URL+"/404.zip")
		if err == nil {
			t.Fatal("downloadFileWith() should fail for 404")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("error = %v, want mention of 404", err)
		}
	})

	t.Run("500 status", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := downloadFileWith(t.Context(), client, dir, ts.URL+"/500.zip")
		if err == nil {
			t.Fatal("downloadFileWith() should fail for 500")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("error = %v, want mention of 500", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := downloadFileWith(t.Context(), client, dir, ts.URL+"/redirect-to-archive")
		if err != nil {
			t.Fatalf("downloadFileWith() = %v", err)
		}
		if filepath.Base(path) != "testassets.zip" {
			t.Errorf("filename after redirect = %q, want %q", filepath.Base(path), "testassets.zip")
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := downloadFileWith(t.Context(), client, dir, "://bad-url")
		if err == nil {
			t.Fatal("downloadFileWith() should fail for invalid URL")
		}
	})

	t.Run("private IP blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := downloadFile(t.Context(), dir, "http://127.0.0.1:9999/file.zip")
		if err == nil {
			t.Fatal("downloadFile() should reject loopback address")
		}
	})

	t.Run("metadata endpoint blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := downloadFile(t.Context(), dir, "http://169.254.169.254/latest/meta-data/")
		if err == nil {
			t.Fatal("downloadFile() should reject link-local address")
		}
	})

	t.Run("ftp scheme blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := downloadFile(t.Context(), dir, "ftp://example.com/file.zip")
		if err == nil {
			t.Fatal("downloadFile() should reject ftp scheme")
		}
	})

	t.Run("bad filename from trailing slash", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := downloadFileWith(t.Context(), client, dir, ts.URL+"/trailing-slash/")
		if err == nil {
			t.Fatal("downloadFileWith() should reject empty filename from trailing slash")
		}
	})

	t.Run("path stays inside download dir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := downloadFileWith(t.Context(), client, dir, ts.URL+"/testassets.zip")
		if err != nil {
			t.Fatalf("downloadFileWith() = %v", err)
		}
		absDir, _ := filepath.Abs(dir)
		absPath, _ := filepath.Abs(path)
		rel, err := filepath.Rel(absDir, absPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Errorf("downloaded file %q escapes download dir %q (rel=%q)", absPath, absDir, rel)
		}
	})

	t.Run("redirect to traversal path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := downloadFileWith(t.Context(), client, dir, ts.URL+"/redirect-to-traversal")
		if err != nil {
			return
		}
		absDir, _ := filepath.Abs(dir)
		absPath, _ := filepath.Abs(path)
		rel, _ := filepath.Rel(absDir, absPath)
		if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Errorf("traversal redirect produced path %q outside dir %q", absPath, absDir)
		}
	})

	t.Run("file content matches", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := downloadFileWith(t.Context(), client, dir, ts.URL+"/testassets.zip")
		if err != nil {
			t.Fatalf("downloadFileWith() = %v", err)
		}
		got, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		zipPath := createZipFromDir(t, testAssetsFilesDir)
		want, err := os.ReadFile(zipPath)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, want) {
			t.Errorf("downloaded file size = %d, want %d", len(got), len(want))
		}
	})
}

func testDownloadAndMount(t *testing.T, ts *httptest.Server, urlPath string) FS {
	t.Helper()
	ctx := t.Context()
	client := ts.Client()
	dir := t.TempDir()

	archivePath, err := downloadFileWith(ctx, client, dir, ts.URL+urlPath)
	if err != nil {
		t.Fatalf("downloadFileWith(%q) = %v", urlPath, err)
	}
	fsys, err := newArchiveFSFromLocalFS(ctx, archivePath)
	if err != nil {
		t.Fatalf("newArchiveFSFromLocalFS() = %v", err)
	}
	t.Cleanup(func() { fsys.Close() })
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
	entries, err := fsys.ReadDir(cwdPath)
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

func TestDownloadFilePathContainment(t *testing.T) {
	t.Parallel()

	body := []byte("test content")
	mux := http.NewServeMux()
	mux.HandleFunc("/safe.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	})
	mux.HandleFunc("/..%2F..%2Fetc%2Fpasswd", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	})
	mux.HandleFunc("/redirect-dotdot", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/../../../tmp/pwned.txt", http.StatusFound)
	})
	mux.HandleFunc("/../../../tmp/pwned.txt", func(w http.ResponseWriter, _ *http.Request) {
		w.Write(body)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := ts.Client()

	tests := []struct {
		name    string
		urlPath string
	}{
		{"safe filename", "/safe.txt"},
		{"encoded traversal", "/..%2F..%2Fetc%2Fpasswd"},
		{"redirect to dotdot", "/redirect-dotdot"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			path, err := downloadFileWith(t.Context(), client, dir, ts.URL+tc.urlPath)
			if err != nil {
				t.Logf("correctly rejected: %v", err)
				return
			}
			absDir, _ := filepath.Abs(dir)
			absPath, _ := filepath.Abs(path)
			if !strings.HasPrefix(absPath, absDir+string(os.PathSeparator)) {
				t.Errorf("downloaded path %q is outside target dir %q", absPath, absDir)
			}
		})
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
