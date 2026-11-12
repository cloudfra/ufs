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

// BenchmarkMkdirEmulationNameCheck measures the name check the Mkdir emulation
// performs against the same standin, which is where that path gains its cost.
func BenchmarkMkdirEmulationNameCheck(b *testing.B) {
	testCases := []struct {
		name string
		seed func(*store)
		dir  string
		// wantErr marks a row the emulation is expected to refuse.
		wantErr bool
		// fresh marks a row that must start each iteration with the directory
		// absent, which the mkdir itself would otherwise undo.
		fresh bool
	}{
		{
			name:  "NewDirUnderADirectory",
			seed:  func(s *store) { s.seedDir("d") },
			dir:   "d/e",
			fresh: true,
		},
		{
			name:    "MissingParent",
			dir:     "d/e",
			wantErr: true,
		},
		{
			name:    "BelowARegularFile",
			seed:    func(s *store) { s.seedFile("d/f.txt", "alpha") },
			dir:     "d/f.txt/g",
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
				err := Mkdir(mkdirAllFS{s}, tc.dir, dirPerm)
				if tc.wantErr {
					if err == nil {
						b.Fatalf("Mkdir(%q) = nil, want an error", tc.dir)
					}
					continue
				}
				if err != nil {
					b.Fatal(err)
				}
				if tc.fresh {
					delete(s.dirs, tc.dir)
					delete(s.perms, tc.dir)
				}
			}
		})
	}
}
