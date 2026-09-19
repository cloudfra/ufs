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

// Package testing provides helpers for testing ufs.
package testing

import (
	"embed"
)

//go:embed testassets/files
var embedTestFiles embed.FS

// TestAssetsFS returns an [embed.FS] with all the test assets of ufs.
func TestAssetsFS() embed.FS {
	return embedTestFiles
}
