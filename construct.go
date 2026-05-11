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
				nFS.addMount(mountPath, makeNestFS(mountFS))
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
	if strings.HasPrefix(name, "memory://") {
		return newMemFS(name)
	}
	if strings.HasPrefix(name, "angry://") {
		return newAngryFS(name)
	}
	if strings.HasPrefix(name, "null://") {
		return newNullFS(name)
	}
	if strings.HasPrefix(name, "file://") {
		return newLocalFS(name)
	}
	if strings.HasPrefix(name, "gs://") {
		return newGCSFS(name)
	}

	stat, err := os.Stat(name)
	if err == nil && stat != nil {
		return newLocalFS(name)
	}
	return nil, pathError("mount", name, fmt.Errorf("%q is not a valid mount path for %s, %w", name, runtime.GOOS, err))
}
