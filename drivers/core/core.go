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

// Package core is the home of the file systems that every ufs program is
// expected to use: memory:, null:, angry:, file:// and archive://.
//
// They are being moved here from the base package. Until a file system has
// moved, this package forwards to the base package, so that callers can
// already use the import path it will have.
package core

import (
	"context"

	"github.com/cloudfra/ufs"
)

// MakeMemFS returns an empty in-memory file system named name.
func MakeMemFS(name string) ufs.WriteFS {
	return ufs.MakeMemFS(name)
}

// NewTempMountFS returns a file system for uri backed by a temporary local
// directory; prepare is called with the directory path to populate it.
func NewTempMountFS(ctx context.Context, uri string, prepare func(string) error) (ufs.WriteFS, error) {
	return ufs.NewTempMountFS(ctx, uri, prepare)
}
