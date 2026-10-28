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

import (
	"fmt"
	"io/fs"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
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

// linearUniqueFiles is the largest batch whose names are checked for repeats
// by comparing each one with the names before it, which needs no allocation.
// BenchmarkValidateBatchFiles and BenchmarkValidateBatchFilesMapOnly in
// batchfile_bench_test.go put the crossover a little past this size.
const linearUniqueFiles = 16

// ValidateBatchFiles validates a batch before any of it is written, which is
// what every [BatchCreate] implementation must do so that a bad entry cannot
// leave the entries before it written. It returns an [*fs.PathError] wrapping
// [fs.ErrInvalid] for the first name that is not a file name per
// [pathutil.ValidateFileName], or that repeats a name earlier in files: a name
// written twice makes the result of the batch depend on the order the file
// system writes its entries in.
func ValidateBatchFiles(op string, files []BatchFile) error {
	if len(files) <= linearUniqueFiles {
		for i, file := range files {
			if err := pathutil.ValidateFileName(op, file.Name); err != nil {
				return err
			}
			for _, before := range files[:i] {
				if file.Name == before.Name {
					return duplicateName(op, file.Name)
				}
			}
		}
		return nil
	}
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if err := pathutil.ValidateFileName(op, file.Name); err != nil {
			return err
		}
		if _, ok := seen[file.Name]; ok {
			return duplicateName(op, file.Name)
		}
		seen[file.Name] = struct{}{}
	}
	return nil
}

// duplicateName reports the error for a name that a batch writes twice.
func duplicateName(op, name string) error {
	return ufserrors.NewPathError(op, name,
		fmt.Errorf("%q is written twice in this batch: %w", name, fs.ErrInvalid))
}
