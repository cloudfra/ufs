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

// Package httputil downloads files over HTTP(S) with protections against
// server-side request forgery and path traversal.
package httputil

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/cloudfra/ufs/internal/hashutil"
	"github.com/cloudfra/ufs/internal/osutil"
)

const (
	// URLQueryParamChecksum is the query parameter that carries the expected
	// checksum of a downloaded file as "<algorithm>:<hex digest>", where
	// algorithm is sha256, sha384, sha512, sha3-256, sha3-384 or sha3-512. A
	// bare hex digest is treated as sha256. Case is ignored.
	URLQueryParamChecksum = "ufs.checksum"
	// uriParamPrefix marks query parameters that are addressed to ufs rather
	// than to the server hosting the file.
	uriParamPrefix        = "ufs."
	maxDownloadSize int64 = 4 << 30 // 4 GiB
	// partialFileSuffix marks a file whose download has not finished.
	partialFileSuffix = ".partial"
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

// blockedNets are the address ranges that are not on the public internet and
// that the net.IP predicates used by isBlockedIP do not cover.
var blockedNets = mustParseCIDRs(
	"0.0.0.0/8",     // "this network" (RFC 791)
	"100.64.0.0/10", // shared address space for carrier-grade NAT (RFC 6598)
	"192.0.0.0/24",  // IETF protocol assignments (RFC 6890)
	"198.18.0.0/15", // benchmarking (RFC 2544)
	"240.0.0.0/4",   // reserved (RFC 1112) and limited broadcast
)

// nat64Net is the well-known NAT64 prefix (RFC 6052). An address in it
// reaches the IPv4 address held in its last four bytes.
var nat64Net = mustParseCIDRs("64:ff9b::/96")[0]

// sixToFourNet is the 6to4 prefix (RFC 3056). The IPv4 address being reached is
// held in bytes 2..5, so 2002:7f00:1::1 translates to 127.0.0.1.
var sixToFourNet = mustParseCIDRs("2002::/16")[0]

// teredoNet is the Teredo prefix (RFC 4380). The IPv4 address being reached is
// held bitwise complemented in the last four bytes, so the stored bytes have to
// be inverted before they can be judged.
var teredoNet = mustParseCIDRs("2001:0000::/32")[0]

func mustParseCIDRs(cidrs ...string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err != nil {
			panic(err)
		}
		nets = append(nets, ipNet)
	}
	return nets
}

// isBlockedIP reports whether ip is an address that a download must not
// connect to: anything that is not a public unicast address.
func isBlockedIP(ip net.IP) bool {
	if len(ip) == net.IPv6len && ip.To4() == nil {
		// Judge a transition address by the IPv4 address it is translated to, so
		// that public hosts stay reachable on IPv6-only networks while an
		// embedded loopback or private address stays blocked. The byte offsets
		// below are in range because the address is known to be sixteen bytes.
		switch {
		case nat64Net.Contains(ip):
			return isBlockedIP(ip[len(ip)-net.IPv4len:])
		case sixToFourNet.Contains(ip):
			return isBlockedIP(ip[2 : 2+net.IPv4len])
		case teredoNet.Contains(ip):
			return isBlockedIP(net.IPv4(^ip[12], ^ip[13], ^ip[14], ^ip[15]))
		}
	}
	if ip.IsLoopback() ||
		ip.IsPrivate() ||
		ip.IsLinkLocalUnicast() ||
		ip.IsMulticast() ||
		ip.IsUnspecified() {
		return true
	}
	for _, blocked := range blockedNets {
		if blocked.Contains(ip) {
			return true
		}
	}
	return false
}

// sanitizeFilename picks the name that rawURL is downloaded as inside the
// download directory.
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
// A file that already exists at the destination is never overwritten: with a
// checksum it is accepted only if the digest matches, without one it is an
// error. A failed download leaves nothing at the destination.
func DownloadFileWith(ctx context.Context, client *http.Client, dir string, uri string) (string, error) {
	return downloadFileWith(ctx, client, dir, uri, osutil.OpenDir)
}

// downloadFileWith is DownloadFileWith with the handle for the download
// directory supplied by the caller, so tests can make it misbehave.
func downloadFileWith(ctx context.Context, client *http.Client, dir string, uri string, openDirHandle openDirFunc) (string, error) {
	parsed, params, err := ParseURI(uri)
	if err != nil {
		return "", fmt.Errorf("invalid download URL: %w", err)
	}
	var fileChecksum *hashutil.Checksum
	if value, ok := params[URLQueryParamChecksum]; ok {
		fileChecksum, err = hashutil.ParseChecksum(value)
		if err != nil {
			return "", fmt.Errorf("invalid %s parameter: %w", URLQueryParamChecksum, err)
		}
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
	archiveFilename := filepath.Join(baseDir, filename)

	// Every file operation on the destination runs through this root, so an
	// entry planted in the download directory cannot point the work outside it.
	root, err := osutil.OpenRoot(baseDir)
	if err != nil {
		return "", fmt.Errorf("cannot open download directory %q: %w", baseDir, err)
	}
	err = storeDownload(root, baseDir, filename, fileChecksum, resp.Body, openDirHandle)
	if closeErr := root.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("cannot close download directory %q: %w", baseDir, closeErr))
	}
	if err != nil {
		return "", err
	}
	return archiveFilename, nil
}

// storeDownload puts body where uri is meant to be downloaded. A failure
// leaves nothing at the destination.
func storeDownload(root *os.Root, dir, name string, fileChecksum *hashutil.Checksum, body io.Reader, openDirHandle openDirFunc) error {
	finalPath := filepath.Join(dir, name)
	// The destination is only known after the redirects settle, so an existing
	// file is inspected here rather than before the request.
	info, statErr := root.Stat(name)
	if statErr == nil {
		return validateExistingFile(root, finalPath, name, info, fileChecksum)
	}
	if !errors.Is(statErr, fs.ErrNotExist) {
		return fmt.Errorf("cannot check for an existing file at %q: %w", finalPath, statErr)
	}

	partialName, f, err := createPartialFile(root, name)
	if err != nil {
		return err
	}
	var w io.Writer = f
	if fileChecksum != nil {
		w = io.MultiWriter(f, fileChecksum)
	}
	err = copyLimited(w, body, maxDownloadSize)
	if err == nil && fileChecksum != nil {
		if verifyErr := fileChecksum.Verify(); verifyErr != nil {
			err = fmt.Errorf("downloaded file %q: %w", finalPath, verifyErr)
		}
	}
	if flushErr := osutil.FlushAndClose(f, finalPath); flushErr != nil && err == nil {
		err = flushErr
	}
	if err != nil {
		return errors.Join(err, discard(root, dir, partialName))
	}
	return publishDownload(root, openDirHandle, dir, name, partialName)
}

// partialCreateAttempts bounds the names storeDownload tries before giving up.
const partialCreateAttempts = 8

// createPartialFile opens a new file for a download of name, taking a name no
// other download already holds. Concurrent downloads of one destination never
// share a file.
func createPartialFile(root *os.Root, name string) (string, *os.File, error) {
	partialName := name + partialFileSuffix
	for attempt := range partialCreateAttempts {
		if attempt > 0 {
			partialName = fmt.Sprintf("%s-%d-%d", name+partialFileSuffix, os.Getpid(), attempt)
		}
		f, err := root.OpenFile(partialName, os.O_WRONLY|os.O_CREATE|os.O_EXCL, osutil.DefaultFilePermissions)
		switch {
		case err == nil:
			return partialName, f, nil
		case !errors.Is(err, fs.ErrExist):
			return "", nil, fmt.Errorf("cannot create partial file for %q: %w", name, err)
		}
	}
	return "", nil, fmt.Errorf("cannot create partial file for %q: %d names are already in use", name, partialCreateAttempts)
}

// discard takes name out of the download directory, counting an absent file as
// success.
func discard(root *os.Root, dir, name string) error {
	if err := root.Remove(name); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("cannot remove %q: %w", filepath.Join(dir, name), err)
	}
	return nil
}

// validateExistingFile reports whether the file already at name can stand in
// for the download; fileChecksum is nil when the URL named no digest. The file
// on disk is left as it is either way.
func validateExistingFile(root *os.Root, finalPath, name string, info fs.FileInfo, fileChecksum *hashutil.Checksum) error {
	if !info.Mode().IsRegular() {
		return fmt.Errorf("cannot download to %q: an existing entry that is not a regular file is in the way", finalPath)
	}
	if fileChecksum == nil {
		return fmt.Errorf("cannot download to %q: an existing file is present but cannot be validated without a %s parameter", finalPath, URLQueryParamChecksum)
	}
	// Rejected from the stat so an oversized file is not hashed first.
	if info.Size() > maxDownloadSize {
		return fmt.Errorf("existing file %q: %w of %d bytes", finalPath, errTooLarge, maxDownloadSize)
	}
	f, err := root.Open(name)
	if err != nil {
		return fmt.Errorf("cannot checksum existing file %q: %w", finalPath, err)
	}
	readErr := copyLimited(fileChecksum, f, maxDownloadSize)
	closeErr := f.Close()
	if readErr != nil {
		return fmt.Errorf("cannot checksum existing file %q: %w", finalPath, errors.Join(readErr, closeErr))
	}
	if closeErr != nil {
		return fmt.Errorf("cannot close existing file %q: %w", finalPath, closeErr)
	}
	if verifyErr := fileChecksum.Verify(); verifyErr != nil {
		return fmt.Errorf("existing file %q: %w", finalPath, verifyErr)
	}
	return nil
}

// openDirFunc opens a download directory so the file just stored in it can be
// made durable. Tests inject a failure through it.
type openDirFunc func(dir string) (osutil.DirSyncer, error)

// publishDownload makes the downloaded file available under name, leaving
// nothing behind if it cannot.
func publishDownload(root *os.Root, openDirHandle openDirFunc, dir, name, partialName string) error {
	finalPath := filepath.Join(dir, name)
	d, err := openDirHandle(dir)
	if err != nil {
		return fmt.Errorf("cannot publish downloaded file to %q: %w", finalPath, errors.Join(err, discard(root, dir, partialName)))
	}
	if err := root.Rename(partialName, name); err != nil {
		return fmt.Errorf("cannot publish downloaded file to %q: %w", finalPath, errors.Join(err, d.Close(), discard(root, dir, partialName)))
	}
	syncErr := d.Sync()
	closeErr := d.Close()
	if syncErr != nil {
		return fmt.Errorf("cannot sync directory %q after publishing %q: %w", dir, finalPath,
			errors.Join(syncErr, closeErr, discard(root, dir, name)))
	}
	if closeErr != nil {
		return fmt.Errorf("cannot close directory %q after publishing %q: %w", dir, finalPath,
			errors.Join(closeErr, discard(root, dir, name)))
	}
	return nil
}

// errTooLarge is returned for a download that is larger than maxDownloadSize.
var errTooLarge = errors.New("download is larger than the size limit")

// copyLimited copies src to dst. It fails with errTooLarge if src holds more
// than limit bytes, instead of silently storing only the first limit bytes.
func copyLimited(dst io.Writer, src io.Reader, limit int64) error {
	// Read one byte past the limit to tell a source of exactly limit bytes
	// from a longer one.
	n, err := io.Copy(dst, io.LimitReader(src, limit+1))
	if err != nil {
		return err
	}
	if n > limit {
		return fmt.Errorf("%w of %d bytes", errTooLarge, limit)
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
