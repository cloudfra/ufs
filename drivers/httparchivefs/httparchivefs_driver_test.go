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

package httparchivefs_test

import (
	"archive/zip"
	"bytes"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/httparchivefs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/internal/osutil"
)

func TestHttpArchiveFS(t *testing.T) {
	t.Parallel()
	server := newHTTPArchiveServer(t)
	client := server.Client()
	archiveURL := server.URL + "/testassets.zip"

	ufsdriversTesting.WriteFS(t, func(t *testing.T) ufs.WriteFS {
		fsys, err := httparchivefs.NewWithClient(t.Context(), client, archiveURL)
		if err != nil {
			t.Fatalf("cannot create httparchivefs %q, %s", archiveURL, err)
		}
		return fsys
	})
}

func newHTTPArchiveServer(t *testing.T) *httptest.Server {
	t.Helper()
	data := zipArchiveData(t, filepath.Join("..", "..", "testing", "testassets", "files"))
	mux := http.NewServeMux()
	mux.HandleFunc("/testassets.zip", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/zip")
		if _, err := w.Write(data); err != nil {
			t.Errorf("failed to write zip response: %v", err)
		}
	})
	server := httptest.NewServer(mux)
	t.Cleanup(server.Close)
	return server
}

func zipArchiveData(tb testing.TB, dir string) []byte {
	tb.Helper()
	src := osutil.DirFS(dir)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)

	err := fs.WalkDir(src, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || p == "." {
			return err
		}
		w, err := zw.Create(p)
		if err != nil {
			return err
		}
		f, err := src.Open(p)
		if err != nil {
			return err
		}
		defer func() {
			if closeErr := f.Close(); closeErr != nil && err == nil {
				err = closeErr
			}
		}()
		_, err = io.Copy(w, f)
		return err
	})
	if err != nil {
		tb.Fatalf("create archive from %q: %v", dir, err)
	}
	if err := zw.Close(); err != nil {
		tb.Fatalf("close zip writer: %v", err)
	}
	return buf.Bytes()
}
