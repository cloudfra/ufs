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

package download

import (
	"bytes"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
)

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

func testFileServer(t *testing.T) (*httptest.Server, []byte) {
	t.Helper()
	body := []byte("the quick brown fox jumps over the lazy dog")
	mux := http.NewServeMux()
	mux.HandleFunc("/testfile.zip", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Errorf("failed to write to response: %v", err)
		}
	})
	mux.HandleFunc("/404.zip", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "not found", http.StatusNotFound)
	})
	mux.HandleFunc("/500.zip", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "server error", http.StatusInternalServerError)
	})
	mux.HandleFunc("/redirect-to-file", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/testfile.zip", http.StatusFound)
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
	return ts, body
}

func TestFile(t *testing.T) {
	t.Parallel()
	ts, body := testFileServer(t)
	client := ts.Client()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := FileWith(t.Context(), client, dir, ts.URL+"/testfile.zip")
		if err != nil {
			t.Fatalf("FileWith() = %v", err)
		}
		if filepath.Base(path) != "testfile.zip" {
			t.Errorf("filename = %q, want %q", filepath.Base(path), "testfile.zip")
		}
		got, err := osutil.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile() = %v", err)
		}
		if !bytes.Equal(got, body) {
			t.Errorf("downloaded content = %q, want %q", got, body)
		}
	})

	t.Run("404 status", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := FileWith(t.Context(), client, dir, ts.URL+"/404.zip")
		if err == nil {
			t.Fatal("FileWith() should fail for 404")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("error = %v, want mention of 404", err)
		}
	})

	t.Run("500 status", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := FileWith(t.Context(), client, dir, ts.URL+"/500.zip")
		if err == nil {
			t.Fatal("FileWith() should fail for 500")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("error = %v, want mention of 500", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := FileWith(t.Context(), client, dir, ts.URL+"/redirect-to-file")
		if err != nil {
			t.Fatalf("FileWith() = %v", err)
		}
		if filepath.Base(path) != "testfile.zip" {
			t.Errorf("filename after redirect = %q, want %q", filepath.Base(path), "testfile.zip")
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := FileWith(t.Context(), client, dir, "://bad-url")
		if err == nil {
			t.Fatal("FileWith() should fail for invalid URL")
		}
	})

	t.Run("private IP blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := File(t.Context(), dir, "http://127.0.0.1:9999/file.zip")
		if err == nil {
			t.Fatal("File() should reject loopback address")
		}
	})

	t.Run("metadata endpoint blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := File(t.Context(), dir, "http://169.254.169.254/latest/meta-data/")
		if err == nil {
			t.Fatal("File() should reject link-local address")
		}
	})

	t.Run("ftp scheme blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := File(t.Context(), dir, "ftp://example.com/file.zip")
		if err == nil {
			t.Fatal("File() should reject ftp scheme")
		}
	})

	t.Run("bad filename from trailing slash", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := FileWith(t.Context(), client, dir, ts.URL+"/trailing-slash/")
		if err == nil {
			t.Fatal("FileWith() should reject empty filename from trailing slash")
		}
	})

	t.Run("path stays inside download dir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := FileWith(t.Context(), client, dir, ts.URL+"/testfile.zip")
		if err != nil {
			t.Fatalf("FileWith() = %v", err)
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
		path, err := FileWith(t.Context(), client, dir, ts.URL+"/redirect-to-traversal")
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
}

func TestFilePathContainment(t *testing.T) {
	t.Parallel()

	body := []byte("test content")
	mux := http.NewServeMux()
	mux.HandleFunc("/safe.txt", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Logf("failed to write response: %v", err)
		}
	})
	mux.HandleFunc("/..%2F..%2Fetc%2Fpasswd", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Logf("failed to write response: %v", err)
		}
	})
	mux.HandleFunc("/redirect-dotdot", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/../../../tmp/pwned.txt", http.StatusFound)
	})
	mux.HandleFunc("/../../../tmp/pwned.txt", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(body); err != nil {
			t.Logf("failed to write response: %v", err)
		}
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
			path, err := FileWith(t.Context(), client, dir, ts.URL+tc.urlPath)
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
