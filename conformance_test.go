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

package ufs

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/google/go-cmp/cmp"
)

// Conformance tests run against the hard coded list of file systems in
// conformance_helper_test.go. Tests here should hold for any FS implementation.

// TestFileSystem runs the shared CRUD and fstest.TestFS harness against each
// backend that needs its own constructor and fixture.
func TestFileSystem(t *testing.T) {
	tests := []struct {
		name string
		run  func(t *testing.T)
	}{
		{"localFS", func(t *testing.T) {
			testFileSystem(t, newLocalFS, mustTemp(t))
		}},
		{"memFS", func(t *testing.T) {
			testFileSystem(t, newMemFS, "memory://test")
		}},
		{"nestFS", func(t *testing.T) {
			testFileSystem(t, newNestFS, "memory://")
		}},
		{"tempMountFS", func(t *testing.T) {
			testFileSystem(t, func(ctx context.Context, name string) (FS, error) {
				return newTempMountFS(ctx, name, func(string) error { return nil })
			}, "temp://")
		}},
		{"gcsFS", func(t *testing.T) {
			client := createStorage(t)
			testFileSystem(t, func(ctx context.Context, name string) (FS, error) {
				return makeGCSFSWithClient(ctx, client, name)
			}, "gs://first")
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, tc.run)
	}
}

// TestFSImplementsReadFS verifies that every file system in the hard coded
// list (read-write, read-only, and permission-denied) is a ReadFS.
func TestFSImplementsReadFS(t *testing.T) {
	t.Parallel()

	for _, tc := range append(append(readWriteFSTestCaseList, readOnlyFSTestCaseList...), permDeniedFSTestCaseList...) {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer validateClose(t, fsys)()
			verifyReadOnlyFS(t, fsys)
		})
	}
}

func TestInvalidPath(t *testing.T) {
	invalidPaths := []string{
		"/absolute/path",
		"../relative/path",
		"invalid/../path",
		"",
	}

	for _, fsysTC := range getAllTestCaseList() {
		for _, path := range invalidPaths {
			t.Run(fmt.Sprintf("Open/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				_, err := fsys.Open(path)
				assertInvalidPathError(t, path, err, "open")
			})

			t.Run(fmt.Sprintf("ReadDir/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				_, err := fsys.ReadDir(path)
				assertInvalidPathError(t, path, err, "readdir")
			})

			t.Run(fmt.Sprintf("Create/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				_, err := fsys.Create(path)
				assertInvalidPathError(t, path, err, "create")
			})

			t.Run(fmt.Sprintf("MkdirAll/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				err := fsys.MkdirAll(path, fs.ModeDir)
				assertInvalidPathError(t, path, err, "mkdir")
			})

			t.Run(fmt.Sprintf("ReadFileFS/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if rf, ok := fsys.(fs.ReadFileFS); ok {
					_, err := rf.ReadFile(path)
					assertInvalidPathError(t, path, err, "readfile")
				}
			})

			t.Run(fmt.Sprintf("ReadLink/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if rf, ok := fsys.(fs.ReadLinkFS); ok {
					_, err := rf.ReadLink(path)
					assertInvalidPathError(t, path, err, "readlink")
				}
			})

			t.Run(fmt.Sprintf("Lstat/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if rf, ok := fsys.(fs.ReadLinkFS); ok {
					_, err := rf.Lstat(path)
					assertInvalidPathError(t, path, err, "lstat")
				}
			})

			t.Run(fmt.Sprintf("Remove/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if r, ok := fsys.(RemoveFileFS); ok {
					err := r.Remove(path)
					assertInvalidPathError(t, path, err, "remove")
				}
			})

			t.Run(fmt.Sprintf("RemoveAll/%s/%s", fsysTC.name, path), func(t *testing.T) {
				t.Parallel()
				fsys := fsysTC.createFS(t)
				if r, ok := fsys.(RemoveFileFS); ok {
					err := r.RemoveAll(path)
					assertInvalidPathError(t, path, err, "removeall")
				}
			})
		}
	}
}

func TestFSConventions(t *testing.T) {
	srcFS, err := newLocalFS(t.Context(), testLocalFSName)
	if err != nil {
		t.Fatalf("cannot mount localFS(%q), %s", testLocalFSName, err)
	}
	t.Cleanup(validateClose(t, srcFS))
	for _, fsysTC := range getReadWriteTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			if err := Rsync(srcFS, fsys, CwdPath); err != nil {
				t.Errorf("rsync failed with error, %s", err)
			}

			allFilenames, err := List(srcFS, CwdPath)
			if err != nil {
				t.Fatal(err)
			}
			if len(allFilenames) == 0 {
				t.Fatal("expected at least 1 file name")
			}
			if err := fstest.TestFS(fsys, allFilenames...); err != nil {
				t.Error(err)
			}
		})
	}
}

func TestFSClose(t *testing.T) {
	for _, fsysTC := range getAllRegularTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)
			for i := range 10 {
				if err := fsys.Close(); err != nil {
					t.Errorf("Close() [%d] failed with error, %s", i, err)
				}
			}

			t.Run("Open", func(t *testing.T) {
				f, err := fsys.Open(".")
				if f != nil {
					buf := make([]byte, 64)
					if bytesWritten, err := f.Read(buf); bytesWritten != 0 && err != fs.ErrClosed {
						t.Errorf("Open('.') worked for a closed file system, read %d (want: 0) bytes with error= %s (want: fs.ErrClosed), file: %+v", bytesWritten, err, f)
					}
				}
				if !errors.Is(err, fs.ErrClosed) {
					t.Errorf("Open('.') did not return fs.ErrClosed, got: %s", err)
				}
			})

			t.Run("Create", func(t *testing.T) {
				f, err := fsys.Create("file.txt")
				if f != nil {
					buf := make([]byte, 64)
					if bytesWritten, err := f.Write(buf); bytesWritten != 0 && err != fs.ErrClosed {
						t.Errorf("Create('file.txt') worked for a closed file system, read %d (want: 0) bytes with error= %s (want: fs.ErrClosed), file: %+v", bytesWritten, err, f)
					}
				}
				if !errors.Is(err, fs.ErrClosed) {
					t.Errorf("Create('file.txt') did not return fs.ErrClosed, got: %s", err)
				}
			})

			t.Run("MkdirAll", func(t *testing.T) {
				if err := fsys.MkdirAll("a/b/c", fs.ModePerm); !errors.Is(err, fs.ErrClosed) {
					t.Errorf("MkdirAll('a/b/c') did not return fs.ErrClosed, got: %s", err)
				}
			})

			t.Run("Remove", func(t *testing.T) {
				if err := fsys.Remove("file.txt"); !errors.Is(err, fs.ErrClosed) {
					t.Errorf("Remove('file.txt') did not return fs.ErrClosed, got: %s", err)
				}
			})

			t.Run("RemoveAll", func(t *testing.T) {
				if err := fsys.RemoveAll("dir"); !errors.Is(err, fs.ErrClosed) {
					t.Errorf("RemoveAll('dir') did not return fs.ErrClosed, got: %s", err)
				}
			})

			t.Run("ReadDir", func(t *testing.T) {
				entries, err := fs.ReadDir(fsys, ".")
				if len(entries) != 0 {
					t.Errorf("ReadDir('.') returned results for a closed file system, got: %v", entries)
				}
				if !errors.Is(err, fs.ErrClosed) {
					t.Errorf("ReadDir('.') did not return fs.ErrClosed, got: %s", err)
				}
			})
		})
	}
}

func TestFSString(t *testing.T) {
	for _, tc := range getAllTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			if got := fsys.String(); !strings.Contains(got, tc.wantString) {
				t.Errorf("%s.String() should contain %q: got: %q", fsys, tc.wantString, got)
			}
		})
	}
}

func TestFSReadDir(t *testing.T) {
	for _, fsysTC := range getAllRegularTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)

			dirs := []string{"a", "b", "b/a/c", "b/b", "b/c", "c", "d/e/f/g", "d/e/g", "a/b/c/d/e/f/g"}
			lsMap := map[string][]string{
				".":             {"a", "b", "c", "d"},
				"a":             {"b"},
				"b":             {"a", "b", "c"},
				"b/a":           {"c"},
				"b/a/c":         {},
				"b/b":           {},
				"b/c":           {},
				"c":             {},
				"d":             {"e"},
				"d/e":           {"f", "g"},
				"d/e/f":         {"g"},
				"d/e/f/g":       {},
				"d/e/g":         {},
				"a/b":           {"c"},
				"a/b/c":         {"d"},
				"a/b/c/d":       {"e"},
				"a/b/c/d/e":     {"f"},
				"a/b/c/d/e/f":   {"g"},
				"a/b/c/d/e/f/g": {},
			}

			for _, dir := range dirs {
				t.Run(fmt.Sprintf("MkdirAll/%s", dir), func(t *testing.T) {
					if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
						t.Errorf("cannot Mkdir(%q), %s", dir, err)
					}
				})
			}

			for input, want := range lsMap {
				t.Run(fmt.Sprintf("ReadDir/%s", input), func(t *testing.T) {
					gotEntries, err := fsys.ReadDir(input)
					if err != nil {
						t.Errorf("cannot ReadDir(%q), got error: %s", input, err)
					}
					gotNames := dirEntryListToNames(gotEntries)
					if diff := cmp.Diff(want, gotNames); diff != "" {
						t.Errorf("got %s, want %s diff(-want,+got):\n %v", gotNames, want, diff)
					}
				})

				t.Run(fmt.Sprintf("Open/%s", input), func(t *testing.T) {
					f, err := fsys.Open(input)
					if err != nil {
						t.Fatalf("cannot ReadDir(%q), got error: %s", input, err)
					}
					defer validateClose(t, f)()
					if rdf, ok := f.(fs.ReadDirFile); ok {
						entries, err := rdf.ReadDir(-1)
						if err != nil {
							t.Errorf("ReadDir(-1) failed with error, %s", err)
						}
						gotNames := dirEntryListToNames(entries)
						sort.Strings(gotNames)
						sort.Strings(want)
						if diff := cmp.Diff(want, gotNames); diff != "" {
							t.Errorf("ReadDir(-1) got %s, want %s diff(-want,+got):\n %v", gotNames, want, diff)
						}
					}
				})
			}
		})
	}
}

func TestFSCreate(t *testing.T) {
	for _, fsysTC := range getAllRegularTestCaseList() {
		t.Run(fsysTC.name, func(t *testing.T) {
			t.Parallel()
			fsys := fsysTC.createFS(t)

			filenames := []string{"b/a/c", "b/b", "b/c", "c", "d/e/f/g", "d/e/g", "a/b/c/d/e/f/g"}

			for _, filename := range filenames {
				t.Run(fmt.Sprintf("Create/%s", filename), func(t *testing.T) {
					dir := path.Dir(filename)
					if err := fsys.MkdirAll(dir, fs.ModePerm); err != nil {
						t.Errorf("cannot Mkdir(%q), %s", dir, err)
					}

					t.Run("Create", func(t *testing.T) {
						f, err := fsys.Create(filename)
						if err != nil {
							t.Errorf("Open(%q) failed, %s", filename, err)
						}
						bytesWritten, err := f.WriteString(filename)
						if err != nil {
							t.Errorf("WriteString(%q) failed to write, %s", filename, err)
						}
						if bytesWritten != len(filename) {
							t.Errorf("WriteString(%q) bytesWritten mismatch, want: %d, got: %d", filename, len(filename), bytesWritten)
						}
						if err := f.Close(); err != nil {
							t.Errorf("Close() got error, %s", err)
						}
					})

					t.Run("ReadFile", func(t *testing.T) {
						data, err := fsys.ReadFile(filename)
						if err != nil {
							t.Errorf("ReadFile(%q) got error, %s", filename, err)
						}
						gotData := string(data)
						if gotData != filename {
							t.Errorf("ReadFile(%q) mismatch, got: %q, want: %q", filename, gotData, filename)
						}
					})

					t.Run("Open", func(t *testing.T) {
						rf, err := fsys.Open(filename)
						if err != nil {
							t.Errorf("Open(%q) got error, %s", filename, err)
						}
						data, err := io.ReadAll(rf)
						if err != nil {
							t.Errorf("io.ReadAll(%q) got error, %s", filename, err)
						}
						if err := rf.Close(); err != nil {
							t.Errorf("Close() got error, %s", err)
						}
						gotData := string(data)
						if gotData != filename {
							t.Errorf("io.ReadAll(%q) mismatch, got: %q, want: %q", filename, gotData, filename)
						}
					})

					t.Run("ReadLink", func(t *testing.T) {
						lfilename, err := fsys.ReadLink(filename)
						if err == nil {
							t.Errorf("ReadLink(%q) error was nil", filename)
						} else if _, ok := err.(*fs.PathError); !ok {
							t.Errorf("ReadLink(%q) error was not of type *fs.PathError, %s", filename, err)
						}
						if lfilename != "" {
							t.Errorf("ReadLink(%q) mismatch, got: %q, want: ''", filename, lfilename)
						}
					})

					t.Run("Lstat", func(t *testing.T) {
						stat, err := fsys.Lstat(filename)
						if err != nil {
							t.Errorf("Lstat(%q) got error, %s", filename, err)
						}
						if stat == nil {
							t.Errorf("Lstat(%q) returned nil FileInfo", filename)
						}
					})
				})
			}
		})
	}
}

func TestFSMkdirAll(t *testing.T) {
	t.Parallel()
	for _, tc := range getAllExceptAngryTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.createFS(t)
			defer validateClose(t, fsys)()
			if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
				t.Errorf("MkdirAll() = %v, want nil", err)
			}
		})
	}
}

func TestFSReadFile(t *testing.T) {
	t.Parallel()
	for _, tc := range getReadWriteTestCaseList() {
		t.Run(tc.name, func(t *testing.T) {
			wantData := randomString(100)
			t.Parallel()
			fsys := tc.createFS(t)
			defer validateClose(t, fsys)()
			f, err := fsys.Create("readfile_test.txt")
			if err != nil {
				t.Fatalf("Create failed: %v", err)
			}
			if _, err := io.WriteString(f, wantData); err != nil {
				t.Fatalf("WriteString failed: %v", err)
			}
			if err := f.Close(); err != nil {
				t.Fatalf("Close failed: %v", err)
			}

			rfs, ok := fsys.(fs.ReadFileFS)
			if !ok {
				t.Skip("does not implement fs.ReadFileFS")
			}
			got, err := rfs.ReadFile("readfile_test.txt")
			if err != nil {
				t.Fatalf("ReadFile failed: %v", err)
			}
			if diff := cmp.Diff(wantData, string(got)); diff != "" {
				t.Errorf("ReadFile mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
