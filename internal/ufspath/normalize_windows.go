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

//go:build windows

package ufspath

import (
	"path/filepath"
	"strings"
)

// NormalizeFileURI strips a "file://" or "file:" URI prefix from name and
// converts a "/C:/path" form (left after stripping "file://" from
// "file:///C:/path") to the Windows-native "C:\path" form required by
// filepath.Abs.
func NormalizeFileURI(name string) string {
	if after, ok := strings.CutPrefix(name, "file://"); ok {
		name = after
	} else {
		name = strings.TrimPrefix(name, "file:")
	}
	// "/C:/path/..." → "C:\path\..." (strip leading slash, convert separators)
	if len(name) >= 3 && name[0] == '/' && name[2] == ':' {
		name = filepath.FromSlash(name[1:])
	}
	return name
}
