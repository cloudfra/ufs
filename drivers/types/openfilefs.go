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

import "io/fs"

// OpenFileFS is an optional interface for opening a file with flags, as
// [os.OpenFile] does. Every file system that ufs.New returns implements it, as
// does every driver of this module. Call ufs.OpenFile rather than asserting
// for it, so that a file system that does not implement it still works.
type OpenFileFS interface {
	fs.FS

	// OpenFile is the generalized open call; most users will use Open or
	// Create instead. It opens the file at name with flag, a combination of
	// the O_* constants of package os, and uses perm for a file that it
	// creates.
	//
	// Not every file system can honor every flag. One that can only read a
	// file and replace it uses the drivers/polyfill package, and fails the
	// combinations it cannot honor with an error that wraps
	// [errors.ErrUnsupported].
	OpenFile(name string, flag int, perm fs.FileMode) (File, error)
}
