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

//go:build linux

package ufs

import (
	"path/filepath"
	"strings"
)

// isPathCoveredBy returns true if pathPrefix is a directory ancestor of or equal to targetPath.
func isPathCoveredBy(targetPath, pathPrefix string) bool {
	target := filepath.ToSlash(filepath.Clean(targetPath))
	prefix := filepath.ToSlash(filepath.Clean(pathPrefix))
	if target == prefix {
		return true
	}
	if strings.HasSuffix(prefix, "/") {
		return strings.HasPrefix(target, prefix)
	}
	return strings.HasPrefix(target, prefix+"/")
}

// isPathUnder returns true if targetPath is strictly under rootPath.
func isPathUnder(targetPath, rootPath string) bool {
	target := filepath.ToSlash(filepath.Clean(targetPath))
	root := filepath.ToSlash(filepath.Clean(rootPath))
	if strings.HasSuffix(root, "/") {
		stripped, _ := strings.CutSuffix(root, "/")
		return strings.HasPrefix(target, root) && target != stripped
	}
	return strings.HasPrefix(target, root+"/")
}

// relPathUnder returns targetPath relative to rootPath.
// Assumes isPathUnder(targetPath, rootPath) is true.
func relPathUnder(targetPath, rootPath string) string {
	target := filepath.ToSlash(filepath.Clean(targetPath))
	root := filepath.ToSlash(filepath.Clean(rootPath))
	if strings.HasSuffix(root, "/") {
		return strings.TrimPrefix(target, root)
	}
	return strings.TrimPrefix(target, root+"/")
}
