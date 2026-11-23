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

package types

// FS is the file system a driver implements in full: a read-write file system
// ([WriteFS]) that opens files with the generalized flags and mode
// ([OpenFileFS]), moves a file or directory ([RenameFS]), and lets go of
// the resources it holds without closing the mount ([ReleaseFS]). It is
// embedded in the file-system type returned by the ufs constructors.
type FS interface {
	WriteFS
	OpenFileFS
	RenameFS
	ReleaseFS

	// TODO: Add all optional types.
	// BatchCreateFS
	// ChmodFS
	// fs.GlobFS
	// etc...

	// Copy copies the file at srcPath to dstPath.
	// Copy(srcPath, dstPath string) error
}
