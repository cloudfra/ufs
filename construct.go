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
	"net/url"
	"os"
	"runtime"
	"strings"
)

// CreateURI creates a URI that specifies the configuration for how to construct the file system with nested mount points.
func CreateURI(name string, nested map[string]string) (string, error) {
	u, err := nameToURI(name)
	if err != nil {
		return "", err
	}
	vals := u.Query()
	for mountPoint, uri := range nested {
		mu, err := nameToURI(uri)
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
	if err != nil {
		origErr := err
		// Assume the name is a local file path.
		u, err = url.Parse("file://" + name)
		if err != nil {
			return nil, fmt.Errorf("%q is not a uri for file system, %w", name, origErr)
		}
	}
	return u, nil
}

// New creates a new nested file system. Use CreateURI to construct the string.
func New(name string) (FS, error) {
	u, err := url.Parse(name)
	if err == nil {
		bFS, err := newBaseFS(u.Scheme + "://" + u.Path)
		if err == nil {
			nFS := makeNestFS(bFS)
			vals := u.Query()
			for mountPath, mountURI := range vals {
				mountFS, err := newBaseFS(mountURI[0])
				if err != nil {
					return nil, err
				}
				if err := nFS.addMount(mountPath, makeNestFS(mountFS)); err != nil {
					return nil, err
				}
			}
			return nFS, nil
		}
	}

	fsys, err := newBaseFS(name)
	if err != nil {
		return nil, err
	}
	return makeNestFS(fsys), nil
}

func newBaseFS(name string) (FS, error) {
	if strings.HasPrefix(name, "memory:") {
		return newMemFS(name)
	}
	if strings.HasPrefix(name, "angry:") {
		return newAngryFS(name)
	}
	if strings.HasPrefix(name, "null:") {
		return newNullFS(name)
	}
	if strings.HasPrefix(name, "file:") {
		return newLocalFS(name)
	}
	if strings.HasPrefix(name, "gs:") {
		return newGCSFS(name)
	}
	if strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://") {
		return newTempMountRemoteArchiveFS(name)
	}

	stat, err := os.Stat(name)
	if err == nil && stat != nil {
		return newLocalFS(name)
	}
	return nil, pathError("mount", name, fmt.Errorf("%q is not a valid mount path for %s, %w", name, runtime.GOOS, err))
}
