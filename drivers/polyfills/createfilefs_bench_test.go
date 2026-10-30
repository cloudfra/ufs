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

package polyfills

import "testing"

// BenchmarkCreateEmulationNameCheck measures the name check an emulated Create
// performs, which is where that path gains its cost. Each row holds the store in
// the state the row describes, so an iteration costs what a create in that state
// costs.
func BenchmarkCreateEmulationNameCheck(b *testing.B) {
	testCases := []struct {
		name string
		seed func(*store)
		file string
		// wantErr marks a row the emulation is expected to refuse.
		wantErr bool
		// fresh marks a row that must start each iteration with the name absent,
		// which the create itself would otherwise undo.
		fresh bool
	}{
		{
			name:  "NewNameUnderADirectory",
			seed:  func(s *store) { s.seedDir("d") },
			file:  "d/b.txt",
			fresh: true,
		},
		{
			name: "ExistingName",
			seed: func(s *store) { s.seedFile("a.txt", "alpha") },
			file: "a.txt",
		},
		{
			name:    "MissingParents",
			file:    "d/e/f/b.txt",
			wantErr: true,
		},
		{
			name:    "BelowARegularFile",
			seed:    func(s *store) { s.seedFile("d/f.txt", "alpha") },
			file:    "d/f.txt/g.txt",
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		b.Run(tc.name, func(b *testing.B) {
			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}

			b.ReportAllocs()
			for b.Loop() {
				file, err := Create(openFileFS{s}, tc.file)
				if tc.wantErr {
					if err == nil {
						if closeErr := file.Close(); closeErr != nil {
							b.Fatal(closeErr)
						}
						b.Fatalf("Create(%q) gave a handle, want an error", tc.file)
					}
					continue
				}
				if err != nil {
					b.Fatal(err)
				}
				if closeErr := file.Close(); closeErr != nil {
					b.Fatal(closeErr)
				}
				if tc.fresh {
					delete(s.files, tc.file)
					delete(s.perms, tc.file)
				}
			}
		})
	}
}
