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
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// privateIPRanges lists address ranges that must never be fetched to prevent
// SSRF attacks against cloud metadata services and internal networks.
var privateIPRanges = func() []net.IPNet {
	cidrs := []string{
		"127.0.0.0/8",    // loopback
		"10.0.0.0/8",     // RFC-1918
		"172.16.0.0/12",  // RFC-1918
		"192.168.0.0/16", // RFC-1918
		"169.254.0.0/16", // link-local / cloud metadata (AWS, GCP, Azure IMDS)
		"::1/128",        // IPv6 loopback
		"fe80::/10",      // IPv6 link-local
	}
	ranges := make([]net.IPNet, 0, len(cidrs))
	for _, cidr := range cidrs {
		_, network, _ := net.ParseCIDR(cidr)
		ranges = append(ranges, *network)
	}
	return ranges
}()

func validateRemoteURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil {
		return fmt.Errorf("invalid URL %q: %w", rawURL, err)
	}
	host := u.Hostname()
	addrs, err := net.LookupHost(host)
	if err != nil {
		return fmt.Errorf("cannot resolve host %q: %w", host, err)
	}
	for _, addr := range addrs {
		ip := net.ParseIP(addr)
		if ip == nil {
			continue
		}
		for _, r := range privateIPRanges {
			if r.Contains(ip) {
				return fmt.Errorf("host %q resolves to a disallowed private address %s", host, addr)
			}
		}
	}
	return nil
}

func downloadFile(dir string, uri string) (string, error) {
	if err := validateRemoteURL(uri); err != nil {
		return "", err
	}
	resp, err := http.Get(uri) //nolint:gosec // URL is validated by validateRemoteURL above
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	parts := strings.Split(resp.Request.URL.Path, "/")
	filename := parts[len(parts)-1]
	archiveFilename := filepath.Join(dir, filename)

	f, err := os.Create(archiveFilename)
	if err != nil {
		return archiveFilename, err
	}
	defer f.Close()

	if _, err := io.Copy(f, resp.Body); err != nil {
		return archiveFilename, err
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
	if !osExists(path) {
		return nil
	}

	if err := os.RemoveAll(path); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("cannot delete directory %q, %s", path, err)
	}
	return nil
}

func tryOSDeleteDirectory(path string) {
	if err := osDeleteDirectory(path); err != nil {
		log.Printf("WARNING: %s", err)
	}
}

func osDeleteFile(path string) error {
	if !osExists(path) {
		return nil
	}

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
