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
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// CreateURI constructs a URI understood by [New] that layers additional file
// systems at specific mount paths inside the base file system. The nested map
// maps mount-point paths (e.g. "cache", "data/scratch") to the URI of the file
// system to mount there. Paths follow [fs.ValidPath] conventions.
//
// Pass the returned URI directly to [New]:
//
//	 ctx := context.Background()
//		uri, _ := ufs.CreateURI("file:///srv/data", map[string]string{
//		    "tmp": "memory://",
//		})
//		fsys, _ := ufs.New(ctx, uri)
//
// Returns an error if name or any nested URI cannot be parsed.
func CreateURI(name string, nested map[string]string) (string, error) {
	u, err := nameToURI(name)
	if err != nil {
		return "", err
	}
	vals := u.Query()
	mountPoints := make([]string, 0, len(nested))
	for mountPoint := range nested {
		mountPoints = append(mountPoints, mountPoint)
	}
	sort.Strings(mountPoints)
	for _, mountPoint := range mountPoints {
		mu, err := nameToURI(nested[mountPoint])
		if err != nil {
			return "", err
		}
		vals.Set(mountPoint, mu.String())
	}
	u.RawQuery = vals.Encode()
	return u.String(), nil
}

func nameToURI(name string) (*url.URL, error) {
	u, err := url.Parse(name)
	if err == nil {
		return u, nil
	}
	origErr := err
	if after, ok := strings.CutPrefix(name, "file://"); ok {
		// On Windows, "file://C:\path" fails url.Parse because the drive-letter
		// colon is misread as a host:port separator with "\path" as an invalid
		// port number. Recover by stripping "file://" and re-interpreting the
		// remainder as a local path, normalizing to forward slashes.
		localPath := filepath.ToSlash(after)
		if !strings.HasPrefix(localPath, "/") {
			localPath = "/" + localPath
		}
		return &url.URL{Scheme: "file", Path: localPath}, nil
	}
	if strings.Contains(name, "://") {
		return nil, fmt.Errorf("%q is not a uri for file system, %w", name, origErr)
	}
	// Assume the name is a local file path.
	u, err = url.Parse("file://" + name)
	if err != nil {
		return nil, fmt.Errorf("%q is not a uri for file system, %w", name, origErr)
	}
	return u, nil
}

// New opens a file system identified by name. The returned [FS] wraps the
// backend in a nestFS layer that automatically mounts archives found inside the
// tree (see below). Always call Close on the returned FS when done.
//
// # URI schemes
//
//   - memory://   — volatile in-memory file system; all data is lost when the
//     FS is closed or the process exits. Safe for concurrent use.
//   - null://     — /dev/null semantics: Create and MkdirAll always succeed,
//     writes are accepted but discarded, reads return empty content, Stat
//     reports everything as a directory. Useful in tests.
//   - angry://    — always returns [fs.ErrInvalid]; used to exercise
//     error-handling paths in tests.
//   - file://path  or a bare path — local directory, mounted read-write via
//     [os.OpenRoot] (Go 1.24+). Access outside the mount root is rejected by
//     the OS. On Windows, directory Stat always reports size 0 (unlike the raw
//     os package which may report 4096).
//   - gs://bucket/prefix — Google Cloud Storage bucket, optionally scoped to a
//     prefix. Credentials are resolved via ADC; unauthenticated access is tried
//     as a fallback.
//   - https:// or http:// URL ending in a recognised archive extension — the
//     archive is downloaded to a temporary directory, mounted read-only, and the
//     temporary directory is removed when Close is called.
//   - A path ending in .git — the repository is shallow-cloned into a temporary
//     directory (not available on AIX).
//   - A local path pointing to a recognised archive (.zip, .tar, .tar.gz, etc.)
//     is mounted read-only through the archive's contents.
//
// # Nested mounts and archive auto-mounting
//
// The returned FS wraps all backends in a nestFS layer. When a directory entry
// named foo.zip (or any recognised archive extension) exists, the virtual path
// foo.zip.d is automatically exposed as a read-only mount of that archive's
// contents. No explicit configuration is required.
//
// Use [CreateURI] to pre-configure additional mount points before calling New.
func New(ctx context.Context, name string) (FS, error) {
	u, err := url.Parse(name)
	if err == nil {
		bFS, err := newBaseFS(ctx, u.Scheme+"://"+u.Host+u.Path)
		if err == nil {
			nFS := makeNestFS(ctx, bFS)
			vals := u.Query()
			for mountPath, mountURI := range vals {
				mountFS, err := newBaseFS(ctx, mountURI[0])
				if err != nil {
					return nil, err
				}
				if err := nFS.addMount(mountPath, makeNestFS(ctx, mountFS)); err != nil {
					return nil, err
				}
			}
			return nFS, nil
		}
	}

	fsys, err := newBaseFS(ctx, name)
	if err != nil {
		return nil, err
	}
	return makeNestFS(ctx, fsys), nil
}

func newBaseFS(ctx context.Context, name string) (FS, error) {
	if isMemFSUri(name) {
		return newMemFS(name)
	}
	if isAngryFSUri(name) {
		return newAngryFS(name)
	}
	if isNullFSUri(name) {
		return newNullFS(name)
	}
	if isGitFSUri(name) {
		return newGitFS(name)
	}
	if isLocalFSUri(name) {
		if isMountableArchivePath(name) {
			return newArchiveFSFromLocalFS(ctx, name)
		}
		return newLocalFS(name)
	}
	if isGCSFSUri(name) {
		return newGCSFS(ctx, name)
	}
	if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") {
		return newTempMountRemoteArchiveFS(ctx, name)
	}

	stat, err := os.Stat(name)
	if err == nil && stat != nil {
		return newLocalFS(name)
	}
	return nil, pathError("mount", name, fmt.Errorf("%q is not a valid mount path for %s, %w", name, runtime.GOOS, err))
}
