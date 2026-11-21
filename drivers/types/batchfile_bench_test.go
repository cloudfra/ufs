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
	"io/fs"
	"strconv"
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
)

func benchBatch(n int) []BatchFile {
	files := make([]BatchFile, n)
	for i := range files {
		files[i] = BatchFile{
			Name: "dir/level/sub/file" + strconv.Itoa(i) + ".txt",
			Data: []byte("content"),
			Perm: 0o644,
		}
	}
	return files
}

// validateBatchMapOnly is the all-sizes map form, kept to show where the
// linear scan stops paying for itself.
func validateBatchMapOnly(op string, files []BatchFile) error {
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		if err := pathutil.ValidateFileName(op, file.Name); err != nil {
			return err
		}
		if _, ok := seen[file.Name]; ok {
			return fs.ErrInvalid
		}
		seen[file.Name] = struct{}{}
	}
	return nil
}

func BenchmarkValidateBatchFiles(b *testing.B) {
	for _, n := range []int{1, 8, 16, 32, 64, 256} {
		b.Run(strconv.Itoa(n)+"_files", func(b *testing.B) {
			files := benchBatch(n)
			b.ReportAllocs()
			for b.Loop() {
				if err := ValidateBatchFiles("batchCreate", files); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

func BenchmarkValidateBatchFilesMapOnly(b *testing.B) {
	for _, n := range []int{1, 8, 16, 32, 64, 256} {
		b.Run(strconv.Itoa(n)+"_files", func(b *testing.B) {
			files := benchBatch(n)
			b.ReportAllocs()
			for b.Loop() {
				if err := validateBatchMapOnly("batchCreate", files); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
