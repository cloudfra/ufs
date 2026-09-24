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

package httputil

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
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
		{"::ffff:127.0.0.1", true},
		{"::ffff:10.0.0.1", true},
		{"::ffff:192.168.1.1", true},
		{"::", true},
		{"224.0.0.1", true},
		{"ff02::1", true},
		{"fe80::abcd:1234", true},
		{"2001:4860:4860::8888", false},
		{"93.184.216.34", false},
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
		{"public ipv4 literal", "http://93.184.216.34/file.zip", false},
		{"public ipv6 literal", "https://[2001:4860:4860::8888]/file.zip", false},
		{"public ipv4 literal with port", "http://93.184.216.34:8080/file.zip", false},
		{"ipv4-mapped loopback", "http://[::ffff:127.0.0.1]/file.zip", true},
		{"ipv6 unique local", "http://[fd00::1]/file.zip", true},
		{"ipv6 link-local", "http://[fe80::1]/file.zip", true},
		{"private with port", "http://10.0.0.1:8080/file.zip", true},
		{"localhost resolves to loopback", "http://localhost/file.zip", true},
		{"unresolvable host", "http://nonexistent.invalid/file.zip", true},
		{"uppercase scheme", "HTTP://127.0.0.1/file.zip", true},
		{"empty scheme", "//example.com/file.zip", true},
		{"javascript scheme", "javascript:alert(1)", true},
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
		{"whitespace only", "/path/  ", "", true},
		{"surrounding whitespace", "/path/ file.zip ", "file.zip", false},
		{"traversal", "/../../etc/passwd", "passwd", false},
		{"literal percent", "/..%2F..%2Fetc%2Fpasswd", "..%2F..%2Fetc%2Fpasswd", false},
		{"dotfile", "/path/.hidden", ".hidden", false},
		{"triple dot", "/path/...", "...", false},
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
		{"ipv6 public", "[2001:4860:4860::8888]:443", false},
		{"ipv4-mapped loopback", "[::ffff:127.0.0.1]:80", true},
		{"unspecified", "0.0.0.0:80", true},
		{"missing port", "93.184.216.34", true},
		{"empty", "", true},
		{"hostname", "localhost:80", true},
		{"public hostname", "example.com:443", true},
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

var testPayload = bytes.Repeat([]byte("ufs httputil test payload\n"), 1024)

func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/testassets.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		if _, err := w.Write(testPayload); err != nil {
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
	ts := testServer(t)
	client := ts.Client()

	t.Run("success", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/testassets.zip")
		if err != nil {
			t.Fatalf("DownloadFileWith() = %v", err)
		}
		if filepath.Base(path) != "testassets.zip" {
			t.Errorf("filename = %q, want %q", filepath.Base(path), "testassets.zip")
		}
		data, err := osutil.ReadFile(path)
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
		_, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/404.zip")
		if err == nil {
			t.Fatal("DownloadFileWith() should fail for 404")
		}
		if !strings.Contains(err.Error(), "404") {
			t.Errorf("error = %v, want mention of 404", err)
		}
	})

	t.Run("500 status", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/500.zip")
		if err == nil {
			t.Fatal("DownloadFileWith() should fail for 500")
		}
		if !strings.Contains(err.Error(), "500") {
			t.Errorf("error = %v, want mention of 500", err)
		}
	})

	t.Run("redirect", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/redirect-to-archive")
		if err != nil {
			t.Fatalf("DownloadFileWith() = %v", err)
		}
		if filepath.Base(path) != "testassets.zip" {
			t.Errorf("filename after redirect = %q, want %q", filepath.Base(path), "testassets.zip")
		}
	})

	t.Run("invalid URL", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := DownloadFileWith(t.Context(), client, dir, "://bad-url")
		if err == nil {
			t.Fatal("DownloadFileWith() should fail for invalid URL")
		}
	})

	t.Run("private IP blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := DownloadFile(t.Context(), dir, "http://127.0.0.1:9999/file.zip")
		if err == nil {
			t.Fatal("DownloadFile() should reject loopback address")
		}
	})

	t.Run("metadata endpoint blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := DownloadFile(t.Context(), dir, "http://169.254.169.254/latest/meta-data/")
		if err == nil {
			t.Fatal("DownloadFile() should reject link-local address")
		}
	})

	t.Run("ftp scheme blocked", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := DownloadFile(t.Context(), dir, "ftp://example.com/file.zip")
		if err == nil {
			t.Fatal("DownloadFile() should reject ftp scheme")
		}
	})

	t.Run("bad filename from trailing slash", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		_, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/trailing-slash/")
		if err == nil {
			t.Fatal("DownloadFileWith() should reject empty filename from trailing slash")
		}
	})

	t.Run("path stays inside download dir", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/testassets.zip")
		if err != nil {
			t.Fatalf("DownloadFileWith() = %v", err)
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			t.Errorf("filepath.Abs(%q) returned an error, %s", dir, err)
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			t.Errorf("filepath.Abs(%q) returned an error, %s", path, err)
		}
		rel, err := filepath.Rel(absDir, absPath)
		if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Errorf("downloaded file %q escapes download dir %q (rel=%q)", absPath, absDir, rel)
		}
	})

	t.Run("redirect to traversal path", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/redirect-to-traversal")
		if err != nil {
			return
		}
		absDir, err := filepath.Abs(dir)
		if err != nil {
			t.Errorf("filepath.Abs(%q) returned an error, %s", dir, err)
		}
		absPath, err := filepath.Abs(path)
		if err != nil {
			t.Errorf("filepath.Abs(%q) returned an error, %s", path, err)
		}
		rel, err := filepath.Rel(absDir, absPath)
		if err != nil {
			t.Errorf("filepath.Rel returned an error, %s", err)
		}
		if rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
			t.Errorf("traversal redirect produced path %q outside dir %q", absPath, absDir)
		}
	})

	t.Run("file content matches", func(t *testing.T) {
		t.Parallel()
		dir := t.TempDir()
		path, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/testassets.zip")
		if err != nil {
			t.Fatalf("DownloadFileWith() = %v", err)
		}
		got, err := osutil.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, testPayload) {
			t.Errorf("downloaded file size = %d, want %d", len(got), len(testPayload))
		}
	})
}

func TestDownloadFilePathContainment(t *testing.T) {
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
			path, err := DownloadFileWith(t.Context(), client, dir, ts.URL+tc.urlPath)
			if err != nil {
				t.Logf("correctly rejected: %v", err)
				return
			}
			absDir, err := filepath.Abs(dir)
			if err != nil {
				t.Errorf("filepath.Abs(%q) returned an error, %s", dir, err)
			}
			absPath, err := filepath.Abs(path)
			if err != nil {
				t.Errorf("filepath.Abs(%q) returned an error, %s", path, err)
			}
			if !strings.HasPrefix(absPath, absDir+string(os.PathSeparator)) {
				t.Errorf("downloaded path %q is outside target dir %q", absPath, absDir)
			}
		})
	}
}

// testNetIP is in TEST-NET-3 (RFC 5737): it passes the private/loopback checks
// but is never routable, so tests must not expect a connection to succeed.
const testNetIP = "203.0.113.1"

func TestSanitizeFilenameBackslash(t *testing.T) {
	t.Parallel()
	u := &url.URL{Scheme: "https", Host: "example.com", Path: `/dir/a\b.zip`}
	got, err := sanitizeFilename(u)
	if runtime.GOOS == "windows" {
		if err != nil || got != "b.zip" {
			t.Errorf("sanitizeFilename(%q) = %q, %v, want %q, nil", u.Path, got, err, "b.zip")
		}
		return
	}
	if err == nil {
		t.Errorf("sanitizeFilename(%q) = %q, want error for backslash", u.Path, got)
	}
}

func TestSanitizeFilenameIgnoresQueryAndFragment(t *testing.T) {
	t.Parallel()
	u, err := url.Parse("https://example.com/dir/file.zip?name=../../evil#frag")
	if err != nil {
		t.Fatal(err)
	}
	got, err := sanitizeFilename(u)
	if err != nil {
		t.Fatalf("sanitizeFilename(%q) = %v", u, err)
	}
	if got != "file.zip" {
		t.Errorf("sanitizeFilename(%q) = %q, want %q", u, got, "file.zip")
	}
}

func TestSanitizeFilenameErrorRedactsPassword(t *testing.T) {
	t.Parallel()
	u, err := url.Parse("https://user:secret@example.com/dir/")
	if err != nil {
		t.Fatal(err)
	}
	_, err = sanitizeFilename(u)
	if err == nil {
		t.Fatal("sanitizeFilename() = nil, want error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("sanitizeFilename() error %q leaks the URL password", err)
	}
}

func TestValidateDownloadURLErrorRedactsPassword(t *testing.T) {
	t.Parallel()
	u := &url.URL{Scheme: "http", User: url.UserPassword("user", "secret"), Path: "/file.zip"}
	err := validateDownloadURL(t.Context(), u)
	if err == nil {
		t.Fatal("validateDownloadURL(empty host) = nil, want error")
	}
	if strings.Contains(err.Error(), "secret") {
		t.Errorf("validateDownloadURL() error %q leaks the URL password", err)
	}
}

func TestNewHTTPClient(t *testing.T) {
	t.Parallel()
	client := newHTTPClient()
	if client.Timeout <= 0 {
		t.Errorf("Timeout = %v, want a positive timeout", client.Timeout)
	}
	if client.CheckRedirect == nil {
		t.Error("CheckRedirect = nil, want redirect validation")
	}
	transport, ok := client.Transport.(*http.Transport)
	if !ok {
		t.Fatalf("Transport = %T, want *http.Transport", client.Transport)
	}
	if transport.DialContext == nil {
		t.Error("Transport.DialContext = nil, want the SSRF-checking dialer")
	}
	if transport.Proxy != nil {
		t.Error("Transport.Proxy != nil, a proxy would bypass the dial-time IP check")
	}
	if newHTTPClient() == client {
		t.Error("newHTTPClient() returned a shared client, want a new one per call")
	}
}

func TestNewHTTPClientCheckRedirect(t *testing.T) {
	t.Parallel()
	client := newHTTPClient()
	mustRequest := func(t *testing.T, rawURL string) *http.Request {
		t.Helper()
		req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, rawURL, nil)
		if err != nil {
			t.Fatal(err)
		}
		return req
	}
	via := func(n int) []*http.Request {
		reqs := make([]*http.Request, n)
		for i := range reqs {
			reqs[i] = mustRequest(t, "http://"+testNetIP+"/hop")
		}
		return reqs
	}

	tests := []struct {
		name    string
		target  string
		hops    int
		wantErr string
	}{
		{"public", "http://" + testNetIP + "/file.zip", 1, ""},
		{"nine hops", "http://" + testNetIP + "/file.zip", 9, ""},
		{"ten hops", "http://" + testNetIP + "/file.zip", 10, "too many redirects"},
		{"loopback", "http://127.0.0.1/file.zip", 1, "not allowed"},
		{"metadata", "http://169.254.169.254/latest/meta-data/", 1, "not allowed"},
		{"private", "http://10.0.0.1/file.zip", 1, "not allowed"},
		{"localhost", "http://localhost/file.zip", 1, "loopback"},
		{"ftp scheme", "ftp://" + testNetIP + "/file.zip", 1, "unsupported scheme"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := client.CheckRedirect(mustRequest(t, tc.target), via(tc.hops))
			if tc.wantErr == "" {
				if err != nil {
					t.Errorf("CheckRedirect(%q, %d hops) = %v, want nil", tc.target, tc.hops, err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("CheckRedirect(%q, %d hops) = %v, want error containing %q", tc.target, tc.hops, err, tc.wantErr)
			}
		})
	}
}

// TestNewHTTPClientBlocksLoopbackDial checks the dial-time guard on its own,
// as it would apply after DNS rebinding: the client is aimed straight at a
// loopback server without going through validateDownloadURL.
func TestNewHTTPClientBlocksLoopbackDial(t *testing.T) {
	t.Parallel()
	var hits atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		hits.Add(1)
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(ts.Close)

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, ts.URL+"/file.zip", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := newHTTPClient().Do(req)
	if err == nil {
		if cerr := resp.Body.Close(); cerr != nil {
			t.Error(cerr)
		}
		t.Fatal("Do(loopback) succeeded, want the dialer to refuse the connection")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("Do(loopback) = %v, want error mentioning 'not allowed'", err)
	}
	if n := hits.Load(); n != 0 {
		t.Errorf("server received %d requests, want 0", n)
	}
}

// TestDownloadFileUsesHardenedClient passes a public IP literal so that the
// pre-flight check succeeds and DownloadFile builds its own client. The
// context is already canceled, so no packets are sent.
func TestDownloadFileUsesHardenedClient(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	dir := t.TempDir()

	_, err := DownloadFile(ctx, dir, "http://"+testNetIP+"/file.zip")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("DownloadFile(canceled) = %v, want context.Canceled", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("download dir has %d entries after a failed download, want 0", len(entries))
	}
}

func TestDownloadFileErrors(t *testing.T) {
	t.Parallel()
	mux := http.NewServeMux()
	mux.HandleFunc("/truncated.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Length", "1000")
		if _, err := w.Write([]byte("short")); err != nil {
			t.Errorf("failed to write to response: %v", err)
		}
	})
	mux.HandleFunc("/file.zip", func(w http.ResponseWriter, _ *http.Request) {
		if _, err := w.Write(testPayload); err != nil {
			t.Errorf("failed to write to response: %v", err)
		}
	})
	mux.HandleFunc("/no-content.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("/redirect.zip", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/file.zip", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/not-modified.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotModified)
	})
	mux.HandleFunc("/forbidden.zip", func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "forbidden", http.StatusForbidden)
	})
	ts := httptest.NewServer(mux)
	t.Cleanup(ts.Close)
	client := ts.Client()

	t.Run("truncated body", func(t *testing.T) {
		t.Parallel()
		_, err := DownloadFileWith(t.Context(), client, t.TempDir(), ts.URL+"/truncated.zip")
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("DownloadFileWith(truncated) = %v, want io.ErrUnexpectedEOF", err)
		}
	})

	t.Run("missing directory", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "missing")
		_, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/file.zip")
		if !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("DownloadFileWith(missing dir) = %v, want fs.ErrNotExist", err)
		}
	})

	t.Run("directory is a file", func(t *testing.T) {
		t.Parallel()
		dir := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(dir, nil, osutil.DefaultFilePermissions); err != nil {
			t.Fatal(err)
		}
		if _, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/file.zip"); err == nil {
			t.Error("DownloadFileWith(dir is a file) = nil, want error")
		}
	})

	t.Run("nil context", func(t *testing.T) {
		t.Parallel()
		var ctx context.Context
		if _, err := DownloadFileWith(ctx, client, t.TempDir(), ts.URL+"/file.zip"); err == nil {
			t.Error("DownloadFileWith(nil ctx) = nil, want error")
		}
	})

	t.Run("canceled context", func(t *testing.T) {
		t.Parallel()
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := DownloadFileWith(ctx, client, t.TempDir(), ts.URL+"/file.zip")
		if !errors.Is(err, context.Canceled) {
			t.Errorf("DownloadFileWith(canceled) = %v, want context.Canceled", err)
		}
	})

	for _, tc := range []struct {
		path   string
		status string
	}{
		{"/not-modified.zip", "304"},
		{"/forbidden.zip", "403"},
		{"/missing.zip", "404"},
	} {
		t.Run("status "+tc.status, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			_, err := DownloadFileWith(t.Context(), client, dir, ts.URL+tc.path)
			if err == nil || !strings.Contains(err.Error(), tc.status) {
				t.Errorf("DownloadFileWith(%s) = %v, want error mentioning %s", tc.path, err, tc.status)
			}
			entries, err := os.ReadDir(dir)
			if err != nil {
				t.Fatal(err)
			}
			if len(entries) != 0 {
				t.Errorf("download dir has %d entries after status %s, want 0", len(entries), tc.status)
			}
		})
	}

	t.Run("status 204 creates empty file", func(t *testing.T) {
		t.Parallel()
		path, err := DownloadFileWith(t.Context(), client, t.TempDir(), ts.URL+"/no-content.zip")
		if err != nil {
			t.Fatalf("DownloadFileWith(204) = %v", err)
		}
		data, err := osutil.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		if len(data) != 0 {
			t.Errorf("downloaded %d bytes for 204, want 0", len(data))
		}
	})

	t.Run("permanent redirect uses final name", func(t *testing.T) {
		t.Parallel()
		path, err := DownloadFileWith(t.Context(), client, t.TempDir(), ts.URL+"/redirect.zip")
		if err != nil {
			t.Fatalf("DownloadFileWith(redirect) = %v", err)
		}
		if got := filepath.Base(path); got != "file.zip" {
			t.Errorf("filename = %q, want %q", got, "file.zip")
		}
	})
}

func TestDownloadFileOverwritesExisting(t *testing.T) {
	t.Parallel()
	ts := testServer(t)
	dir := t.TempDir()
	existing := filepath.Join(dir, "testassets.zip")
	stale := bytes.Repeat([]byte("stale"), len(testPayload))
	if err := os.WriteFile(existing, stale, osutil.DefaultFilePermissions); err != nil {
		t.Fatal(err)
	}

	path, err := DownloadFileWith(t.Context(), ts.Client(), dir, ts.URL+"/testassets.zip")
	if err != nil {
		t.Fatalf("DownloadFileWith() = %v", err)
	}
	if path != existing {
		t.Errorf("path = %q, want %q", path, existing)
	}
	got, err := osutil.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, testPayload) {
		t.Errorf("downloaded %d bytes, want %d (existing file not truncated)", len(got), len(testPayload))
	}
}

func TestDownloadFileReturnsAbsolutePath(t *testing.T) {
	ts := testServer(t)
	dir := t.TempDir()
	t.Chdir(dir)

	path, err := DownloadFileWith(t.Context(), ts.Client(), ".", ts.URL+"/testassets.zip")
	if err != nil {
		t.Fatalf("DownloadFileWith() = %v", err)
	}
	if !filepath.IsAbs(path) {
		t.Errorf("path = %q, want an absolute path", path)
	}
	if _, err := os.Stat(filepath.Join(dir, "testassets.zip")); err != nil {
		t.Errorf("file not written into the working directory: %v", err)
	}
}

func TestDownloadFileConcurrent(t *testing.T) {
	t.Parallel()
	ts := testServer(t)
	client := ts.Client()

	const n = 8
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Go(func() {
			path, err := DownloadFileWith(t.Context(), client, t.TempDir(), ts.URL+"/testassets.zip")
			if err != nil {
				errs[i] = err
				return
			}
			got, err := osutil.ReadFile(path)
			if err != nil {
				errs[i] = err
				return
			}
			if !bytes.Equal(got, testPayload) {
				errs[i] = fmt.Errorf("download %d: got %d bytes, want %d", i, len(got), len(testPayload))
			}
		})
	}
	wg.Wait()
	if err := errors.Join(errs...); err != nil {
		t.Error(err)
	}
}

// TestDownloadFileDeletedWorkingDirectory makes filepath.Abs fail for a
// relative download dir by removing the working directory. It changes the
// working directory and cannot run in parallel.
func TestDownloadFileDeletedWorkingDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("windows does not allow removing the working directory")
	}
	ts := testServer(t)
	dir := filepath.Join(t.TempDir(), "cwd")
	if err := os.Mkdir(dir, osutil.DefaultDirectoryPermissions); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	if err := os.Remove(dir); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Getwd(); err == nil {
		t.Skip("os.Getwd() still succeeds after removing the working directory")
	}

	_, err := DownloadFileWith(t.Context(), ts.Client(), ".", ts.URL+"/testassets.zip")
	if err == nil || !strings.Contains(err.Error(), "invalid download directory") {
		t.Errorf("DownloadFileWith(deleted cwd) = %v, want 'invalid download directory' error", err)
	}
}
