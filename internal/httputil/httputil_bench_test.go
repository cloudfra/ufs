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

package httputil

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"syscall"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
)

// benchSizes brackets the archive sizes the download path handles.
var benchSizes = []struct {
	name string
	size int
}{
	{"1MiB", 1 << 20},
	{"32MiB", 32 << 20},
	{"256MiB", 256 << 20},
}

// benchPayload returns size bytes of data that has no structure, so nothing
// in the storage stack can take a shortcut on it.
func benchPayload(size int) []byte {
	payload := make([]byte, (size/8+1)*8)
	state := uint64(0x9E3779B97F4A7C15)
	for i := 0; i < len(payload); i += 8 {
		state ^= state << 13
		state ^= state >> 7
		state ^= state << 17
		binary.LittleEndian.PutUint64(payload[i:], state)
	}
	return payload[:size]
}

// benchServer serves payload at /archive.zip over loopback. A failed write is
// not reported here: a client that already holds the wanted file closes the
// body unread, which breaks this write.
func benchServer(b *testing.B, payload []byte) *httptest.Server {
	b.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/archive.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		if _, err := w.Write(payload); err != nil && !errors.Is(err, syscall.EPIPE) && !errors.Is(err, syscall.ECONNRESET) {
			b.Errorf("failed to write to response: %v", err)
		}
	})
	ts := httptest.NewServer(mux)
	b.Cleanup(ts.Close)
	return ts
}

// closeOnly releases a written file the way the download path did before the
// Sync was added.
func closeOnly(f syncCloser, _ string) error {
	return f.Close()
}

// BenchmarkFlushAndClose writes payload the way DownloadFileWith does, then
// releases the file with Close alone or Sync followed by Close. The difference
// is the cost of the added Sync.
func BenchmarkFlushAndClose(b *testing.B) {
	modes := []struct {
		name  string
		flush func(f syncCloser, name string) error
	}{
		{"close_only", closeOnly},
		{"sync_then_close", flushAndClose},
	}
	for _, size := range benchSizes {
		payload := benchPayload(size.size)
		b.Run(size.name, func(b *testing.B) {
			dir := b.TempDir()
			name := filepath.Join(dir, "archive.zip")
			b.SetBytes(int64(len(payload)))
			for _, mode := range modes {
				b.Run(mode.name, func(b *testing.B) {
					for b.Loop() {
						f, err := os.Create(filepath.Clean(name))
						if err != nil {
							b.Fatal(err)
						}
						if _, err := f.Write(payload); err != nil {
							b.Fatal(err)
						}
						if err := mode.flush(f, name); err != nil {
							b.Fatal(err)
						}
					}
				})
			}
		})
	}
}

// BenchmarkDownloadFileWith measures the whole download path, which is the
// work the added Sync is charged against.
func BenchmarkDownloadFileWith(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(size.name, func(b *testing.B) {
			payload := benchPayload(size.size)
			ts := benchServer(b, payload)
			client := ts.Client()
			dir := b.TempDir()
			name := filepath.Join(dir, "archive.zip")
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				// The download path refuses to replace an existing file, so each
				// iteration starts from an empty destination.
				osutil.TryDeleteFile(name)
				if _, err := DownloadFileWith(b.Context(), client, dir, ts.URL+"/archive.zip"); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDownloadExistingFile measures a destination that already holds the
// wanted file: it is hashed and compared, and the body is discarded.
func BenchmarkDownloadExistingFile(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(size.name, func(b *testing.B) {
			payload := benchPayload(size.size)
			ts := benchServer(b, payload)
			client := ts.Client()
			dir := b.TempDir()
			if _, err := DownloadFileWith(b.Context(), client, dir, ts.URL+"/archive.zip"); err != nil {
				b.Fatal(err)
			}
			digest := sha256.Sum256(payload)
			uri := fmt.Sprintf("%s/archive.zip?%s=sha256:%s", ts.URL, URLQueryParamChecksum, hex.EncodeToString(digest[:]))
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				if _, err := DownloadFileWith(b.Context(), client, dir, uri); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkDownloadWithoutSync measures the download path as it was before the
// Sync was added, so it can be read against BenchmarkDownloadFileWith.
func BenchmarkDownloadWithoutSync(b *testing.B) {
	for _, size := range benchSizes {
		b.Run(size.name, func(b *testing.B) {
			payload := benchPayload(size.size)
			ts := benchServer(b, payload)
			client := ts.Client()
			dir := b.TempDir()
			name := filepath.Join(dir, "archive.zip")
			b.SetBytes(int64(len(payload)))
			for b.Loop() {
				osutil.TryDeleteFile(name)
				if err := copyWithoutSync(b.Context(), client, ts.URL+"/archive.zip", name); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkPublishDownload isolates the rename that publishes a finished
// download, with and without the directory sync.
func BenchmarkPublishDownload(b *testing.B) {
	noDirSync := func(string) (dirSyncer, error) { return &dirSyncerFake{}, nil }
	modes := []struct {
		name    string
		openDir openDirFunc
	}{
		{name: "rename_only", openDir: noDirSync},
		{name: "rename_and_dir_sync", openDir: openDir},
	}
	for _, size := range benchSizes {
		b.Run(size.name, func(b *testing.B) {
			payload := benchPayload(size.size)
			for _, mode := range modes {
				b.Run(mode.name, func(b *testing.B) {
					dir := b.TempDir()
					final := filepath.Join(dir, "archive.zip")
					partial := final + partialFileSuffix
					b.SetBytes(int64(len(payload)))
					for b.Loop() {
						if err := osutil.WriteFile(partial, payload); err != nil {
							b.Fatal(err)
						}
						if err := publishDownload(mode.openDir, dir, final, partial); err != nil {
							b.Fatal(err)
						}
						osutil.TryDeleteFile(final)
					}
				})
			}
		})
	}
}

// copyWithoutSync stores the body of uri at name without syncing it. It is a
// benchmarking baseline, not a second download implementation.
func copyWithoutSync(ctx context.Context, client *http.Client, uri string, name string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, uri, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		closeErr := resp.Body.Close()
		return fmt.Errorf("download %q failed with status %d: %w", uri, resp.StatusCode, closeErr)
	}
	f, err := os.Create(filepath.Clean(name))
	if err != nil {
		closeErr := resp.Body.Close()
		return fmt.Errorf("%w: %w", err, closeErr)
	}
	copyErr := copyLimited(f, resp.Body, maxDownloadSize)
	closeFileErr := f.Close()
	closeBodyErr := resp.Body.Close()
	if copyErr != nil {
		return copyErr
	}
	if closeFileErr != nil {
		return closeFileErr
	}
	return closeBodyErr
}
