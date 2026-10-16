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

import (
	"io/fs"
)

// BatchFile is one file that [BatchCreate] writes.
type BatchFile struct {
	// Name is the path of the file. It must satisfy [fs.ValidPath] and must
	// not be the root (".").
	Name string

	// Data is the whole content of the file. The file system does not keep a
	// reference to it after BatchCreate returns, and the caller must not
	// change it before then.
	Data []byte

	// Perm holds the permission bits of the file, on file systems that store
	// them. Zero means the permissions that Create uses.
	Perm fs.FileMode
}

// BatchCreateFS is an optional interface for a file system that can write
// several whole files as one atomic operation, such as a database that writes
// them in one transaction. It is not embedded in [WriteFS]; call
// [BatchCreate] to write a batch to any [WriteFS].
type BatchCreateFS interface {
	// BatchCreate writes every file in files, in order. Missing parent
	// directories are created as MkdirAll creates them, an existing file is
	// replaced, and a later entry for a name replaces an earlier one. An
	// empty batch does nothing.
	//
	// It returns an [*fs.PathError] for the first file that fails: one
	// wrapping [fs.ErrInvalid] if its name is invalid or the root, one
	// wrapping [fs.ErrClosed] if the file system is closed, and an error if
	// its name or one of its parent paths is an existing directory or file of
	// the wrong kind, as Create and MkdirAll would.
	//
	// BatchCreate must be atomic: if it returns an error none of the files
	// was written, and a reader sees either none or all of them. A file system
	// that cannot guarantee this must not implement BatchCreateFS, so that
	// [BatchCreate] falls back to Create. It must be safe to call from several
	// goroutines, and an implementation may commit concurrent batches together.
	//
	// A [Watcher] is notified as Create, MkdirAll and Close would notify it:
	// [NotifyCreate] for each new directory and file and [NotifyWrite] for each
	// replaced file, once the batch is committed.
	BatchCreate(files []BatchFile) error
}
