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

// Package httputil downloads files over HTTP(S) with protections against
// server-side request forgery and path traversal.
package httputil

import (
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfra/ufs/internal/osutil"
)

const (
	// URLQueryParamChecksum is the query parameter that carries the expected
	// checksum of a downloaded file as "<algorithm>:<hex digest>", where
	// algorithm is sha256, sha384 or sha512. A bare hex digest is treated as
	// sha256. Case is ignored.
	URLQueryParamChecksum = "ufs.checksum"
	// uriParamPrefix marks query parameters that are addressed to ufs rather
	// than to the server hosting the file.
	uriParamPrefix  = "ufs."
	maxDownloadSize = 4 << 30 // 4 GiB
)

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

// DownloadFile downloads the file at uri into dir and returns its path. The URL
// and any redirects are rejected if they resolve to a private or loopback
// address. See DownloadFileWith for how "ufs." query parameters are handled.
func DownloadFile(ctx context.Context, dir string, uri string) (string, error) {
	return DownloadFileWith(ctx, nil, dir, uri)
}

// DownloadFileWith downloads the file at uri into dir. If client is nil, a
// new SSRF-hardened client is created and the URL is pre-validated against
// private/loopback addresses. When a non-nil client is supplied (tests), the
// pre-flight validation is skipped because the caller owns transport security.
//
// Query parameters prefixed with "ufs." are removed from uri before the
// request is sent. If URLQueryParamChecksum is present, the download fails
// unless the digest of the response body matches it. A checksum that names an
// unsupported algorithm or is not a valid digest for its algorithm is rejected
// before any request is made.
//
// If the body cannot be read in full or the checksum does not match, the
// file is deleted, including any earlier file of the same name it replaced.
func DownloadFileWith(ctx context.Context, client *http.Client, dir string, uri string) (string, error) {
	parsed, params, err := ParseURI(uri)
	if err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}
	sum, err := parseChecksum(params)
	if err != nil {
		return "", err
	}
	if client == nil {
		if err := validateDownloadURL(ctx, parsed); err != nil {
			return "", err
		}
		client = newHTTPClient()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, parsed.String(), nil)
	if err != nil {
		return "", err
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() {
		if err := resp.Body.Close(); err != nil {
			slog.Warn("failed to close response body", "error", err)
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

	f, err := os.Create(filepath.Clean(archiveFilename))
	if err != nil {
		return "", err
	}

	var w io.Writer = f
	if sum != nil {
		w = io.MultiWriter(f, sum.hash)
	}
	_, err = io.Copy(w, io.LimitReader(resp.Body, maxDownloadSize))
	if err == nil && sum != nil {
		err = sum.verify(archiveFilename)
	}
	// Close before deleting; Windows cannot remove an open file.
	if closeErr := f.Close(); closeErr != nil {
		slog.Warn("failed to close downloaded file", "path", archiveFilename, "error", closeErr)
	}
	if err != nil {
		osutil.TryDeleteFile(archiveFilename)
		return "", err
	}

	return archiveFilename, nil
}

// checksum is the expected digest of a download, parsed from
// URLQueryParamChecksum.
type checksum struct {
	algorithm string
	// hash must be fed the file contents before verify is called.
	hash hash.Hash
	want []byte
}

// parseChecksum returns the checksum requested by URLQueryParamChecksum in
// params, or nil if params carries none. The value is "<algorithm>:<hex
// digest>"; a value without an algorithm is a sha256 digest. Case is ignored.
// An error is returned if the algorithm is unsupported or the digest is not
// hex of the algorithm's length.
func parseChecksum(params map[string]string) (*checksum, error) {
	value, ok := params[URLQueryParamChecksum]
	if !ok {
		return nil, nil
	}
	algorithm, digest, ok := strings.Cut(value, ":")
	if !ok {
		algorithm, digest = "sha256", value
	}
	algorithm = strings.ToLower(algorithm)

	var h hash.Hash
	switch algorithm {
	case "sha256":
		h = sha256.New()
	case "sha384":
		h = sha512.New384()
	case "sha512":
		h = sha512.New()
	default:
		return nil, fmt.Errorf("unsupported checksum algorithm %q, want sha256, sha384 or sha512", algorithm)
	}
	want, err := hex.DecodeString(digest)
	if err != nil {
		return nil, fmt.Errorf("invalid %s checksum %q: %w", algorithm, digest, err)
	}
	if len(want) != h.Size() {
		return nil, fmt.Errorf("invalid %s checksum %q: got %d hex characters, want %d", algorithm, digest, len(digest), hex.EncodedLen(h.Size()))
	}
	return &checksum{algorithm: algorithm, hash: h, want: want}, nil
}

// verify compares the digest of the data written to c.hash, the contents of
// the file at path, against the expected digest.
func (c *checksum) verify(path string) error {
	if got := c.hash.Sum(nil); !bytes.Equal(got, c.want) {
		return fmt.Errorf("checksum mismatch for %q: expected %s:%x, got %s:%x", path, c.algorithm, c.want, c.algorithm, got)
	}
	return nil
}

// ParseURI parses uri and extracts the query parameters whose name starts with
// "ufs.". It returns the URL with those parameters removed and a map of the
// extracted parameters keyed by their full name, prefix included (for example
// URLQueryParamChecksum). The remaining query parameters keep their original
// order and encoding. If a parameter is repeated, the first value wins. The
// map is nil when uri has no "ufs." parameters. An error is returned if uri is
// invalid or a "ufs." parameter value is not valid percent-encoding.
func ParseURI(uri string) (*url.URL, map[string]string, error) {
	parsed, err := url.Parse(uri)
	if err != nil {
		return nil, nil, fmt.Errorf("invalid URI %q: %w", uri, err)
	}
	if parsed.RawQuery == "" {
		return parsed, nil, nil
	}
	// The query is edited pair by pair rather than through url.Values:
	// Values.Encode sorts and re-encodes every remaining parameter, which
	// breaks URLs signed over their exact query string, and Query silently
	// drops malformed pairs, which would skip a malformed checksum.
	var params map[string]string
	var kept []string
	for pair := range strings.SplitSeq(parsed.RawQuery, "&") {
		rawKey, rawValue, _ := strings.Cut(pair, "=")
		key, err := url.QueryUnescape(rawKey)
		if err != nil || !strings.HasPrefix(key, uriParamPrefix) {
			kept = append(kept, pair)
			continue
		}
		value, err := url.QueryUnescape(rawValue)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid value for query parameter %q: %w", key, err)
		}
		if params == nil {
			params = make(map[string]string)
		}
		if _, ok := params[key]; !ok {
			params[key] = value
		}
	}
	if params != nil {
		parsed.RawQuery = strings.Join(kept, "&")
	}
	return parsed, params, nil
}
