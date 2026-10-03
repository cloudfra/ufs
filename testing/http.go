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

package testing

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func CreateHTTPServer(t *testing.T) *httptest.Server {
	t.Helper()
	zipData, err := TestAssetsArchivesFS().ReadFile("testassets/archives/single-testassets.zip")
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
