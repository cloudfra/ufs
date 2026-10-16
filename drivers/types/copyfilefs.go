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

package types

// CopyFileFS is an optional interface that a file system may implement to
// support native file copying. It is not embedded in [WriteFS], so a driver
// need not implement it; the copy helpers in the ufs and ops packages fall
// back to a read-and-write copy when a backend does not implement it.
type CopyFileFS interface {
	// CopyFile copies a file from srcPath to dstPath. It returns an error
	// wrapping [fs.ErrNotExist] if srcPath does not exist, or an error if dstPath
	// already exists. Copying the root (".") returns [fs.ErrPermission].
	//
	// CopyFile is not guaranteed to be atomic; some backends may implement it as
	// a read-and-write operation. Callers should not assume that the file at
	// dstPath is created if CopyFile returns an error.
	CopyFile(srcPath, dstPath string) error
}
