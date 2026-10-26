// Copyright 2026 Cloudfra
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

//go:build !unix

package httputil

// unsyncableDir stands in for a directory whose entries cannot be synced. NTFS
// journals metadata, so the entry is durable once the rename returns, and
// leaving a directory handle open would block later removals of the file.
type unsyncableDir struct{}

func (unsyncableDir) Sync() error { return nil }

func (unsyncableDir) Close() error { return nil }

// openDir returns a handle that accepts the durability calls without doing
// anything, so the download path stays the same on every platform.
func openDir(_ string) (dirSyncer, error) {
	return unsyncableDir{}, nil
}
