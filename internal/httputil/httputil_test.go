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
	"maps"
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

// Hex digests of testPayload.
const (
	testPayloadSHA256     = "781ee6dc098e170a7e0c9f337a02ff52972819035c88d96afcd09a839a67c17d"
	testPayloadSHA384     = "90ca6269d4b969cefb2a8f58f53743a7c8d514f0eaf411b95432e7bbab905066c21c66c69bf929a05488267ad48f28f1"
	testPayloadSHA512     = "3ae85b2baa4d3e8e942d42d83aa79bf4c18dcd84c1da8c7507f20d023e5d232bd8c1e581c37ce6b9c6cc94618563525a04fd505fe5825b6a00905411e8f92454"
	testPayloadSHA3Sum256 = "409e611b3e3753faf2adbddaf9a68e7d3db8ebb96a8fb19133f78a852758aa8c"
	testPayloadSHA3Sum512 = "b6719ec47734601517eddb9cefb327c718f62b8853f1f23e3549bb2f0c64f9ebb60e47f59628d6549ad221a6f82e08ade67bacb8bfbc92be2af5965ea1d752f0"
)

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
		dir := t.TempDir()
		_, err := DownloadFileWith(t.Context(), client, dir, ts.URL+"/truncated.zip")
		if !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Errorf("DownloadFileWith(truncated) = %v, want io.ErrUnexpectedEOF", err)
		}
		assertEmptyDir(t, dir)
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

func TestParseURI(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		uri        string
		wantURI    string
		wantParams map[string]string
	}{
		{
			name:    "no query",
			uri:     "https://example.com/a.zip",
			wantURI: "https://example.com/a.zip",
		},
		{
			name:    "no ufs params leaves query untouched",
			uri:     "https://example.com/a.zip?z=1&a=b%20c&flag",
			wantURI: "https://example.com/a.zip?z=1&a=b%20c&flag",
		},
		{
			name:       "only checksum",
			uri:        "https://example.com/a.zip?ufs.checksum=abc123",
			wantURI:    "https://example.com/a.zip",
			wantParams: map[string]string{URLQueryParamChecksum: "abc123"},
		},
		{
			name:       "checksum first",
			uri:        "https://example.com/a.zip?ufs.checksum=abc123&token=t",
			wantURI:    "https://example.com/a.zip?token=t",
			wantParams: map[string]string{URLQueryParamChecksum: "abc123"},
		},
		{
			name:       "checksum last",
			uri:        "https://example.com/a.zip?token=t&ufs.checksum=abc123",
			wantURI:    "https://example.com/a.zip?token=t",
			wantParams: map[string]string{URLQueryParamChecksum: "abc123"},
		},
		{
			name:       "remaining params keep order and encoding",
			uri:        "https://example.com/a.zip?z=1&ufs.checksum=abc123&a=b%20c&flag",
			wantURI:    "https://example.com/a.zip?z=1&a=b%20c&flag",
			wantParams: map[string]string{URLQueryParamChecksum: "abc123"},
		},
		{
			name:       "multiple ufs params",
			uri:        "https://example.com/a.zip?ufs.checksum=abc123&x=1&ufs.other=v",
			wantURI:    "https://example.com/a.zip?x=1",
			wantParams: map[string]string{URLQueryParamChecksum: "abc123", "ufs.other": "v"},
		},
		{
			name:       "escaped key and value are decoded",
			uri:        "https://example.com/a.zip?ufs%2Eother=a%20b%26c",
			wantURI:    "https://example.com/a.zip",
			wantParams: map[string]string{"ufs.other": "a b&c"},
		},
		{
			name:       "repeated param keeps first value",
			uri:        "https://example.com/a.zip?ufs.checksum=first&ufs.checksum=second",
			wantURI:    "https://example.com/a.zip",
			wantParams: map[string]string{URLQueryParamChecksum: "first"},
		},
		{
			name:       "empty value",
			uri:        "https://example.com/a.zip?ufs.checksum=",
			wantURI:    "https://example.com/a.zip",
			wantParams: map[string]string{URLQueryParamChecksum: ""},
		},
		{
			name:       "missing value",
			uri:        "https://example.com/a.zip?ufs.checksum",
			wantURI:    "https://example.com/a.zip",
			wantParams: map[string]string{URLQueryParamChecksum: ""},
		},
		{
			name:    "prefix must match exactly",
			uri:     "https://example.com/a.zip?ufs=1&ufschecksum=2&UFS.checksum=3&x.ufs.checksum=4",
			wantURI: "https://example.com/a.zip?ufs=1&ufschecksum=2&UFS.checksum=3&x.ufs.checksum=4",
		},
		{
			name:    "ufs prefix in value is not a param",
			uri:     "https://example.com/a.zip?next=ufs.checksum",
			wantURI: "https://example.com/a.zip?next=ufs.checksum",
		},
		{
			name:       "userinfo path and fragment are preserved",
			uri:        "https://user@example.com:8443/dir/a.zip?ufs.checksum=abc123&x=1#frag",
			wantURI:    "https://user@example.com:8443/dir/a.zip?x=1#frag",
			wantParams: map[string]string{URLQueryParamChecksum: "abc123"},
		},
		{
			name:       "non http scheme",
			uri:        "archive:///tmp/a.zip?ufs.checksum=abc123",
			wantURI:    "archive:///tmp/a.zip",
			wantParams: map[string]string{URLQueryParamChecksum: "abc123"},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, params, err := ParseURI(tc.uri)
			if err != nil {
				t.Fatalf("ParseURI(%q) = %v", tc.uri, err)
			}
			if got := parsed.String(); got != tc.wantURI {
				t.Errorf("ParseURI(%q) URL = %q, want %q", tc.uri, got, tc.wantURI)
			}
			if len(params) != len(tc.wantParams) || !maps.Equal(params, tc.wantParams) {
				t.Errorf("ParseURI(%q) params = %v, want %v", tc.uri, params, tc.wantParams)
			}
		})
	}
}

func TestParseURIErrors(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name string
		uri  string
	}{
		{name: "control character", uri: "https://example.com/\x7f"},
		{name: "missing scheme before colon", uri: "://example.com/a.zip"},
		{name: "malformed ufs value", uri: "https://example.com/a.zip?ufs.checksum=%zz"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			parsed, params, err := ParseURI(tc.uri)
			if err == nil {
				t.Fatalf("ParseURI(%q) = (%v, %v, nil), want error", tc.uri, parsed, params)
			}
			if parsed != nil || params != nil {
				t.Errorf("ParseURI(%q) = (%v, %v), want nil results on error", tc.uri, parsed, params)
			}
		})
	}
}

func TestDownloadFileChecksum(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name      string
		query     string
		wantQuery string
		wantErr   string
		// wantNoRequest is set when the checksum must be rejected before the
		// server is contacted.
		wantNoRequest bool
	}{
		{name: "no checksum", query: "", wantQuery: ""},
		{name: "bare checksum is sha256", query: "?ufs.checksum=" + testPayloadSHA256, wantQuery: ""},
		{name: "bare uppercase checksum", query: "?ufs.checksum=" + strings.ToUpper(testPayloadSHA256), wantQuery: ""},
		{name: "sha256", query: "?ufs.checksum=sha256:" + testPayloadSHA256, wantQuery: ""},
		{name: "sha384", query: "?ufs.checksum=sha384:" + testPayloadSHA384, wantQuery: ""},
		{name: "sha512", query: "?ufs.checksum=sha512:" + testPayloadSHA512, wantQuery: ""},
		{name: "sha3-256", query: "?ufs.checksum=sha3-256:" + testPayloadSHA3Sum256, wantQuery: ""},
		{name: "sha3-512", query: "?ufs.checksum=sha3-512:" + testPayloadSHA3Sum512, wantQuery: ""},
		{name: "mismatched sha3-256", query: "?ufs.checksum=sha3-256:" + testPayloadSHA256, wantQuery: "", wantErr: "checksum mismatch"},
		{name: "uppercase algorithm and digest", query: "?ufs.checksum=SHA512:" + strings.ToUpper(testPayloadSHA512), wantQuery: ""},
		{name: "escaped colon", query: "?ufs.checksum=sha512%3A" + testPayloadSHA512, wantQuery: ""},
		{name: "checksum with other params", query: "?z=1&ufs.checksum=sha512:" + testPayloadSHA512 + "&a=b%20c", wantQuery: "z=1&a=b%20c"},
		{name: "mismatched bare checksum", query: "?ufs.checksum=" + strings.Repeat("0", len(testPayloadSHA256)), wantQuery: "", wantErr: "checksum mismatch"},
		{name: "mismatched sha256", query: "?ufs.checksum=sha256:" + strings.Repeat("0", len(testPayloadSHA256)), wantQuery: "", wantErr: "checksum mismatch"},
		{name: "mismatched sha384", query: "?ufs.checksum=sha384:" + strings.Repeat("0", len(testPayloadSHA384)), wantQuery: "", wantErr: "checksum mismatch"},
		{name: "mismatched sha512", query: "?ufs.checksum=sha512:" + strings.Repeat("F", len(testPayloadSHA512)), wantQuery: "", wantErr: "checksum mismatch"},
		{name: "mismatched with other params", query: "?token=t&ufs.checksum=sha512:" + strings.Repeat("0", len(testPayloadSHA512)), wantQuery: "token=t", wantErr: "checksum mismatch"},
		{name: "sha256 digest labeled sha512", query: "?ufs.checksum=sha512:" + testPayloadSHA256, wantErr: "invalid sha512 checksum", wantNoRequest: true},
		{name: "sha512 digest without prefix", query: "?ufs.checksum=" + testPayloadSHA512, wantErr: "invalid sha256 checksum", wantNoRequest: true},
		{name: "truncated digest", query: "?ufs.checksum=sha256:" + testPayloadSHA256[:16], wantErr: "invalid sha256 checksum", wantNoRequest: true},
		{name: "non hex digest", query: "?ufs.checksum=sha256:" + strings.Repeat("z", len(testPayloadSHA256)), wantErr: "invalid sha256 checksum", wantNoRequest: true},
		{name: "empty checksum", query: "?ufs.checksum=", wantErr: "invalid sha256 checksum", wantNoRequest: true},
		{name: "algorithm without digest", query: "?ufs.checksum=sha512:", wantErr: "invalid sha512 checksum", wantNoRequest: true},
		{name: "unsupported algorithm", query: "?ufs.checksum=md5:d41d8cd98f00b204e9800998ecf8427e", wantErr: `unsupported checksum algorithm "md5"`, wantNoRequest: true},
		{name: "empty algorithm", query: "?ufs.checksum=:" + testPayloadSHA256, wantErr: `unsupported checksum algorithm ""`, wantNoRequest: true},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var gotQuery atomic.Pointer[string]
			ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotQuery.Store(&r.URL.RawQuery)
				if _, err := w.Write(testPayload); err != nil {
					t.Errorf("failed to write to response: %v", err)
				}
			}))
			t.Cleanup(ts.Close)

			dir := t.TempDir()
			path, err := DownloadFileWith(t.Context(), ts.Client(), dir, ts.URL+"/testassets.zip"+tc.query)
			switch q := gotQuery.Load(); {
			case tc.wantNoRequest && q != nil:
				t.Errorf("server received a request with query %q, want none", *q)
			case tc.wantNoRequest:
			case q == nil:
				t.Error("server did not receive a request")
			case *q != tc.wantQuery:
				t.Errorf("server received query %q, want %q", *q, tc.wantQuery)
			}
			if tc.wantErr != "" {
				if err == nil {
					t.Fatalf("DownloadFileWith() = %q, want error containing %q", path, tc.wantErr)
				}
				if !strings.Contains(err.Error(), tc.wantErr) {
					t.Errorf("error = %v, want mention of %q", err, tc.wantErr)
				}
				assertEmptyDir(t, dir)
				return
			}
			if err != nil {
				t.Fatalf("DownloadFileWith() = %v", err)
			}
			if want := filepath.Join(dir, "testassets.zip"); path != want {
				t.Errorf("path = %q, want %q", path, want)
			}
			got, err := osutil.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, testPayload) {
				t.Errorf("downloaded %d bytes, want %d", len(got), len(testPayload))
			}
		})
	}
}

// assertEmptyDir fails the test if a failed download left anything in dir.
func assertEmptyDir(t *testing.T, dir string) {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		t.Errorf("failed download left %q in %q", entry.Name(), dir)
	}
}

func TestDownloadFileFailureRemovesExistingFile(t *testing.T) {
	t.Parallel()
	ts := testServer(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "testassets.zip"), []byte("stale"), osutil.DefaultFilePermissions); err != nil {
		t.Fatal(err)
	}

	uri := ts.URL + "/testassets.zip?ufs.checksum=" + strings.Repeat("0", len(testPayloadSHA256))
	if _, err := DownloadFileWith(t.Context(), ts.Client(), dir, uri); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Fatalf("DownloadFileWith() = %v, want checksum mismatch", err)
	}
	assertEmptyDir(t, dir)
}

func TestDownloadFileChecksumSurvivesRedirect(t *testing.T) {
	t.Parallel()
	ts := testServer(t)
	uri := ts.URL + "/redirect-to-archive?ufs.checksum=" + strings.Repeat("0", len(testPayloadSHA256))
	if _, err := DownloadFileWith(t.Context(), ts.Client(), t.TempDir(), uri); err == nil || !strings.Contains(err.Error(), "checksum mismatch") {
		t.Errorf("DownloadFileWith() = %v, want checksum mismatch", err)
	}

	uri = ts.URL + "/redirect-to-archive?ufs.checksum=" + testPayloadSHA256
	if _, err := DownloadFileWith(t.Context(), ts.Client(), t.TempDir(), uri); err != nil {
		t.Errorf("DownloadFileWith() = %v", err)
	}
}
