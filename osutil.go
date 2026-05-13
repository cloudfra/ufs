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
	"net/http"
	"os"
	"path/filepath"
	"strings"
)

func downloadFile(dir string, uri string) (string, error) {
	resp, err := http.Get(uri)
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
	if err := os.RemoveAll(path); err != nil {
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
