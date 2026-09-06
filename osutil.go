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
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"math"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const maxDownloadSize = 4 << 30 // 4 GiB

func newHTTPClient() *http.Client {
	return &http.Client{
		Timeout: 10 * time.Minute,
		Transport: &http.Transport{
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
				Control:   dialControl,
			}).DialContext,
		},
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			if len(via) >= 10 {
				return fmt.Errorf("too many redirects")
			}
			return validateDownloadURL(req.Context(), req.URL)
		},
	}
}

// dialControl is called after DNS resolution but before the TCP connection is
// established. It rejects connections to private/loopback IPs, defeating DNS
// rebinding attacks where a hostname resolves to a public IP during
// pre-validation but to a private IP at actual connect time.
func dialControl(_ string, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("invalid dial address %q: %w", address, err)
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("invalid IP in dial address %q", address)
	}
	if isBlockedIP(ip) {
		return fmt.Errorf("connection to private/loopback address %s is not allowed", ip)
	}
	return nil
}

func validateDownloadURL(ctx context.Context, u *url.URL) error {
	if u.Scheme != "https" && u.Scheme != "http" {
		return fmt.Errorf("unsupported scheme %q, only http and https are allowed", u.Scheme)
	}
	host := u.Hostname()
	if host == "" {
		return fmt.Errorf("empty hostname in URL %q", u.Redacted())
	}
	if ip := net.ParseIP(host); ip != nil {
		if isBlockedIP(ip) {
			return fmt.Errorf("download from private/loopback address %s is not allowed", ip)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	for _, addr := range ips {
		if isBlockedIP(addr.IP) {
			return fmt.Errorf("host %q resolves to private/loopback address %s", host, addr.IP)
		}
	}
	return nil
}

func isBlockedIP(ip net.IP) bool {
	return ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsLinkLocalMulticast() ||
		ip.IsUnspecified()
}

func sanitizeFilename(rawURL *url.URL) (string, error) {
	p := rawURL.Path
	parts := strings.Split(p, "/")
	filename := parts[len(parts)-1]
	filename = strings.TrimSpace(filename)
	if filename == "" || filename == "." || filename == ".." {
		return "", fmt.Errorf("invalid filename %q derived from URL %q", filename, rawURL.Redacted())
	}
	filename = filepath.Base(filename)
	if filename == "" || filename == "." || filename == ".." || strings.ContainsAny(filename, `/\`) {
		return "", fmt.Errorf("invalid filename %q derived from URL %q", filename, rawURL.Redacted())
	}
	return filename, nil
}

func downloadFile(ctx context.Context, dir string, uri string) (string, error) {
	return downloadFileWith(ctx, nil, dir, uri)
}

// downloadFileWith downloads the file at uri into dir. If client is nil, a
// new SSRF-hardened client is created and the URL is pre-validated against
// private/loopback addresses. When a non-nil client is supplied (tests), the
// pre-flight validation is skipped because the caller owns transport security.
func downloadFileWith(ctx context.Context, client *http.Client, dir string, uri string) (string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}
	if client == nil {
		if err := validateDownloadURL(ctx, parsed); err != nil {
			return "", err
		}
		client = newHTTPClient()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			log.Printf("WARNING: failed to close response body: %v", err)
		}
	}()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return "", fmt.Errorf("download %q failed with status %d", uri, resp.StatusCode)
	}

	filename, err := sanitizeFilename(resp.Request.URL)
	if err != nil {
		return "", err
	}
	baseDir, err := filepath.Abs(dir)
	if err != nil {
		return "", fmt.Errorf("invalid download directory %q: %w", dir, err)
	}
	archiveFilename, err := filepath.Abs(filepath.Join(baseDir, filename))
	if err != nil {
		return "", fmt.Errorf("invalid archive filename %q: %w", filename, err)
	}
	rel, err := filepath.Rel(baseDir, archiveFilename)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
		return "", fmt.Errorf("resolved path %q escapes download directory %q", archiveFilename, baseDir)
	}

	f, err := os.Create(archiveFilename)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("failed to close downloaded file", "path", archiveFilename, "error", err)
		}
	}()

	if _, err := io.Copy(f, io.LimitReader(resp.Body, maxDownloadSize)); err != nil {
		return "", err
	}

	if err := f.Sync(); err != nil {
		return "", err
	}

	return archiveFilename, nil
}

func createOSTempDirectory() (string, func() error, error) {
	tmpDir, err := os.MkdirTemp(os.TempDir(), "goapp")
	if err != nil {
		return "", func() error { return nil }, fmt.Errorf("cannot create temp directory, %w", err)
	}
	return tmpDir, func() error {
		return osDeleteDirectory(tmpDir)
	}, nil
}

func osExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func osDeleteDirectory(path string) error {
	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot delete directory %q, %w", path, err)
	}
	return nil
}

func tryOSDeleteDirectory(path string) {
	if err := osDeleteDirectory(path); err != nil {
		log.Printf("WARNING: %s", err)
	}
}

func osDeleteFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot delete file %q, %w", path, err)
	}
	return nil
}

func tryOSDeleteFile(path string) {
	if err := osDeleteFile(path); err != nil {
		log.Printf("WARNING: %s", err)
	}
}

// clampToUint32 converts n to uint32, clamping negative values to 0 and
// values above math.MaxUint32 to math.MaxUint32.
func clampToUint32(n int) uint32 {
	if n < 0 {
		return 0
	}
	if uint64(n) > math.MaxUint32 {
		return math.MaxUint32
	}
	return uint32(n)
}

// clampToUint64 converts n to uint64, clamping negative values to 0.
func clampToUint64(n int64) uint64 {
	if n < 0 {
		return 0
	}
	return uint64(n)
}
