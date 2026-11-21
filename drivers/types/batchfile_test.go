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
	"errors"
	"io/fs"
	"strconv"
	"strings"
	"testing"
)

func uniqueNames(n int) []BatchFile {
	files := make([]BatchFile, n)
	for i := range files {
		files[i].Name = "dir/file" + strconv.Itoa(i) + ".txt"
	}
	return files
}

func TestValidateBatchFiles(t *testing.T) {
	t.Run("empty", func(t *testing.T) {
		for _, files := range [][]BatchFile{nil, {}} {
			if err := ValidateBatchFiles("batchCreate", files); err != nil {
				t.Errorf("ValidateBatchFiles(nil batch) = %v, want nil", err)
			}
		}
	})
	t.Run("unique", func(t *testing.T) {
		// Sizes on both sides of linearUniqueFiles, which both must accept.
		for _, n := range []int{1, 2, linearUniqueFiles, linearUniqueFiles + 1, 100} {
			if err := ValidateBatchFiles("batchCreate", uniqueNames(n)); err != nil {
				t.Errorf("ValidateBatchFiles(%d unique files) = %v, want nil", n, err)
			}
		}
	})
	t.Run("duplicate", func(t *testing.T) {
		tests := []struct {
			name  string
			size  int
			first int
			last  int
		}{
			{"adjacent", 3, 0, 1},
			{"apart", 5, 1, 4},
			{"at the linear limit", linearUniqueFiles, 0, linearUniqueFiles - 1},
			{"past the linear limit", linearUniqueFiles + 1, 0, linearUniqueFiles},
			{"far apart in a large batch", 100, 7, 99},
		}
		for _, tc := range tests {
			t.Run(tc.name, func(t *testing.T) {
				files := uniqueNames(tc.size)
				files[tc.last].Name = files[tc.first].Name
				err := ValidateBatchFiles("batchCreate", files)
				if err == nil {
					t.Fatalf("ValidateBatchFiles(%d files, %q twice) = nil, want an error",
						tc.size, files[tc.first].Name)
				}
				var pathErr *fs.PathError
				if !errors.As(err, &pathErr) {
					t.Fatalf("ValidateBatchFiles = %v, want an [*fs.PathError]", err)
				}
				if pathErr.Op != "batchCreate" {
					t.Errorf("error Op = %q, want %q", pathErr.Op, "batchCreate")
				}
				if pathErr.Path != files[tc.first].Name {
					t.Errorf("error Path = %q, want %q", pathErr.Path, files[tc.first].Name)
				}
				if !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("ValidateBatchFiles = %v, want it to wrap %v", err, fs.ErrInvalid)
				}
				if !strings.Contains(err.Error(), strconv.Quote(files[tc.first].Name)) {
					t.Errorf("ValidateBatchFiles = %v, want it to name the repeated file", err)
				}
			})
		}
	})
	t.Run("invalid name", func(t *testing.T) {
		for _, size := range []int{linearUniqueFiles, linearUniqueFiles + 1} {
			for _, bad := range []string{"", ".", "dir/", "../x", "a//b"} {
				files := uniqueNames(size)
				files[size-1].Name = bad
				err := ValidateBatchFiles("batchCreate", files)
				var pathErr *fs.PathError
				if !errors.As(err, &pathErr) {
					t.Fatalf("ValidateBatchFiles(%q) = %v, want an [*fs.PathError]", bad, err)
				}
				if pathErr.Path != bad {
					t.Errorf("ValidateBatchFiles(%q) reported %q", bad, pathErr.Path)
				}
				if !errors.Is(err, fs.ErrInvalid) {
					t.Errorf("ValidateBatchFiles(%q) = %v, want it to wrap %v", bad, err, fs.ErrInvalid)
				}
			}
		}
	})
	t.Run("first failure in order", func(t *testing.T) {
		// Whichever check trips first on the walk through the batch is the
		// one reported, in both directions.
		for _, size := range []int{linearUniqueFiles, linearUniqueFiles + 1} {
			files := uniqueNames(size)
			files[2].Name = "dir/"
			files[5].Name = files[4].Name
			err := ValidateBatchFiles("batchCreate", files)
			var pathErr *fs.PathError
			if !errors.As(err, &pathErr) {
				t.Fatalf("ValidateBatchFiles = %v, want an [*fs.PathError]", err)
			}
			if pathErr.Path != "dir/" {
				t.Errorf("ValidateBatchFiles reported %q, want the earlier bad name %q",
					pathErr.Path, "dir/")
			}

			files = uniqueNames(size)
			files[2].Name = files[1].Name
			files[5].Name = "dir/"
			err = ValidateBatchFiles("batchCreate", files)
			if !errors.As(err, &pathErr) {
				t.Fatalf("ValidateBatchFiles = %v, want an [*fs.PathError]", err)
			}
			if pathErr.Path != files[1].Name {
				t.Errorf("ValidateBatchFiles reported %q, want the earlier bad name %q",
					pathErr.Path, files[1].Name)
			}
		}
	})
}
