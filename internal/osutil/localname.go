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

package osutil

import (
	"path/filepath"
	"runtime"
	"strings"
)

// LocalDriverPriority is the registration priority of the driver that serves
// local names. It is behind every driver that owns a scheme. A driver for a
// kind of local file, such as an archive, registers one ahead of it.
const LocalDriverPriority = 10000

// IsLocalName reports whether name names a file or directory of the local
// disk: a file: URI, a plain path, or any name that exists on disk.
//
// A name that starts with another scheme, such as "memory:" or "s3://bucket",
// is local only when it exists on disk, so that a scheme whose driver is not
// registered is reported as unknown instead of being opened as a path. A
// single letter before the colon is a Windows drive, not a scheme.
func IsLocalName(name string) bool {
	if strings.HasPrefix(name, "file:") || !hasScheme(name) {
		return true
	}
	stat, err := Stat(name)
	return err == nil && stat != nil
}

// hasScheme reports whether name starts with a URI scheme of two or more
// characters followed by a colon.
func hasScheme(name string) bool {
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case 'a' <= c && c <= 'z' || 'A' <= c && c <= 'Z':
		case i > 0 && ('0' <= c && c <= '9' || c == '+' || c == '-' || c == '.'):
		case c == ':':
			return i >= 2
		default:
			return false
		}
	}
	return false
}

// LocalPath returns the host path named by name, which is a path or a file:
// URI. On Windows it also converts the "/C:/path" form, left after stripping
// "file://" from "file:///C:/path", to the native "C:\path" form required by
// filepath.Abs.
func LocalPath(name string) string {
	if after, ok := strings.CutPrefix(name, "file://"); ok {
		name = after
	} else {
		name = strings.TrimPrefix(name, "file:")
	}
	if runtime.GOOS == "windows" && len(name) >= 3 && name[0] == '/' && name[2] == ':' {
		name = filepath.FromSlash(name[1:])
	}
	return name
}
