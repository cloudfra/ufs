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

import (
	"errors"
	"io"
	"io/fs"
	"maps"
	"os"
	"reflect"
	"slices"
	"testing"

	"github.com/cloudfra/ufs/internal/pathutil"
)

// openWritableFS implements fs.FS with an Open that hands back a file that can
// be written to, the way a local file handle can whatever the flags were. A
// read-only open must not pass that ability on to the caller.
type openWritableFS struct{ *store }

func (f openWritableFS) Open(name string) (fs.File, error) {
	if _, ok := f.files[name]; !ok {
		return nil, &fs.PathError{Op: "open", Path: name, Err: fs.ErrNotExist}
	}
	return &fakeHandle{store: f.store, name: name, writable: true}, nil
}

// listFS implements fs.FS with directories that can be listed, so that the
// handle of a read-only open of one keeps its ReadDir.
type listFS struct{ *store }

func (f listFS) Open(name string) (fs.File, error) {
	if f.dirs[name] {
		return &listableDir{fakeDir: fakeDir{name: name}}, nil
	}
	return f.open(name)
}

// listableDir is a fakeDir that can be listed.
type listableDir struct{ fakeDir }

func (d *listableDir) ReadDir(int) ([]fs.DirEntry, error) {
	return []fs.DirEntry{listableEntry{name: "a.txt"}}, nil
}

// listableEntry is the fs.DirEntry a listed directory reports.
type listableEntry struct{ name string }

func (e listableEntry) Name() string      { return e.name }
func (e listableEntry) IsDir() bool       { return false }
func (e listableEntry) Type() fs.FileMode { return 0 }

func (e listableEntry) Info() (fs.FileInfo, error) {
	return fakeInfo{name: e.name, size: int64(len("alpha")), mode: filePerm}, nil
}

func TestOpenFile(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name string
		wrap func(*store) fs.FS
		seed func(*store)
		file string
		flag int
		perm fs.FileMode
		// write is content written through the handle that was handed back, and
		// read is what the same handle is then expected to give up.
		write string
		read  string
		// wantCloses counts the handles the polyfill closed itself. Querying a
		// name that is already there costs one open and one close, which is the
		// price of deciding what the flags mean before anything is written to
		// it. The native method pays none of it.
		wantCloses   int
		wantErr      string
		wantErrIs    error
		wantWriteErr string
		wantReadErr  string
		wantFiles    map[string]string
		wantPerms    map[string]fs.FileMode
		wantDirs     []string
		wantOpens    int
	}{
		{
			// A file system that implements OpenFileFS knows which flags it can
			// carry out and what mode a new file gets, so its own method must win
			// even where an emulation is available too. The mode the new file
			// takes is the perm the caller passed, which the emulation could never
			// offer since Create takes no permission bits.
			name: "native OpenFileFS wins over the emulation",
			wrap: func(s *store) fs.FS {
				return createOpenFS{s}
			},
			file:      "b.txt",
			flag:      os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
			perm:      filePerm,
			write:     "beta",
			wantFiles: map[string]string{"b.txt": "beta"},
			wantPerms: map[string]fs.FileMode{"b.txt": filePerm},
			wantOpens: 1,
		},
		{
			// Whatever the file system's own method reports for a flag combination
			// the standin would also refuse is what the caller gets, and no stat
			// is done first.
			name:       "a native file system refuses its own exclusive create",
			wrap:       openFileFSSet,
			seed:       seedAlpha,
			file:       "a.txt",
			flag:       os.O_WRONLY | os.O_CREATE | os.O_EXCL | os.O_TRUNC,
			wantErr:    "file already exists",
			wantErrIs:  fs.ErrExist,
			wantFiles:  map[string]string{"a.txt": "alpha"},
			wantOpens:  0,
			wantCloses: 0,
		},
		{
			// A native read-only open costs one open and nothing else: the file
			// system enforces the access mode itself.
			name:         "a native read-only open",
			wrap:         openFileFSSet,
			seed:         seedAlpha,
			file:         "a.txt",
			flag:         os.O_RDONLY,
			read:         "alpha",
			wantWriteErr: "permission denied",
			wantFiles:    map[string]string{"a.txt": "alpha"},
			wantOpens:    1,
		},
		{
			// The read-only half needs no write interface at all, so a plain
			// fs.FS is enough.
			name:         "read-only from a file system that can only read",
			wrap:         plainFSSet,
			seed:         seedAlpha,
			file:         "a.txt",
			flag:         os.O_RDONLY,
			read:         "alpha",
			wantWriteErr: "permission denied",
			wantFiles:    map[string]string{"a.txt": "alpha"},
			wantCloses:   1,
		},
		{
			// Open may hand back a file that can be written to whatever the flags
			// asked for, so the read-only handle must block writes itself rather
			// than trust the file underneath it.
			name:         "a read-only open blocks a handle that can be written to",
			wrap:         openWritableFSSet,
			seed:         seedAlpha,
			file:         "a.txt",
			flag:         os.O_RDONLY,
			read:         "alpha",
			write:        "beta",
			wantWriteErr: "permission denied",
			wantFiles:    map[string]string{"a.txt": "alpha"},
			wantCloses:   1,
		},
		{
			// O_CREATE with a read-only open means the file is created empty and
			// then opened, which needs Create. The mode it gets is whatever the
			// file system gives a new file, since Create takes no permission bits.
			name:         "an empty file is created for a read-only open with O_CREATE",
			wrap:         createFSSet,
			file:         "b.txt",
			flag:         os.O_RDONLY | os.O_CREATE,
			wantFiles:    map[string]string{"b.txt": ""},
			wantPerms:    map[string]fs.FileMode{"b.txt": 0},
			wantWriteErr: "permission denied",
			wantOpens:    1,
			wantCloses:   1,
		},
		{
			// Creating the empty file needs a write interface, and a read-only
			// file system has none.
			name:      "O_CREATE has nothing to create the file with",
			wrap:      plainFSSet,
			file:      "b.txt",
			flag:      os.O_RDONLY | os.O_CREATE,
			wantErr:   `does not support "open"`,
			wantFiles: map[string]string{},
		},
		{
			// Without O_CREATE a missing name is a missing name, even for a file
			// system that could create it.
			name:      "a missing file without O_CREATE",
			wrap:      createFSSet,
			file:      "b.txt",
			flag:      os.O_WRONLY,
			wantErr:   "file does not exist",
			wantErrIs: fs.ErrNotExist,
			wantFiles: map[string]string{},
		},
		{
			name:       "O_EXCL refuses an existing file",
			wrap:       createFSSet,
			seed:       seedAlpha,
			file:       "a.txt",
			flag:       os.O_WRONLY | os.O_CREATE | os.O_EXCL | os.O_TRUNC,
			wantErr:    "file already exists",
			wantErrIs:  fs.ErrExist,
			wantFiles:  map[string]string{"a.txt": "alpha"},
			wantCloses: 1,
		},
		{
			// perm is ignored here: Create is all there is to write a new file
			// with, and it takes no permission bits.
			name:        "a new file opened for writing",
			wrap:        createFSSet,
			file:        "b.txt",
			flag:        os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
			write:       "beta",
			wantReadErr: "permission denied",
			wantFiles:   map[string]string{"b.txt": "beta"},
			wantPerms:   map[string]fs.FileMode{"b.txt": 0},
			wantOpens:   1,
		},
		{
			// Truncating means the old content is gone, which is what Create does
			// to a name that is already there. The mode the file had is left
			// alone, since new content says nothing about who may read it.
			name:        "a write truncates an existing file",
			wrap:        createFSSet,
			seed:        seedAlpha,
			file:        "a.txt",
			flag:        os.O_WRONLY | os.O_TRUNC,
			write:       "beta",
			wantReadErr: "permission denied",
			wantFiles:   map[string]string{"a.txt": "beta"},
			wantPerms:   map[string]fs.FileMode{"a.txt": filePerm},
			wantOpens:   1,
			wantCloses:  1,
		},
		{
			// An in-place write needs the old content kept, and Create replaces a
			// file instead. Keeping a copy somewhere to write back on Close is a
			// copy the rest of the file system never sees, so this is refused
			// rather than quietly doing something else.
			name:       "an in-place write is refused",
			wrap:       createFSSet,
			seed:       seedAlpha,
			file:       "a.txt",
			flag:       os.O_WRONLY | os.O_CREATE,
			wantErr:    "unsupported",
			wantErrIs:  errors.ErrUnsupported,
			wantFiles:  map[string]string{"a.txt": "alpha"},
			wantCloses: 1,
		},
		{
			// A read-only open cannot change the file at all, so the truncate flag
			// says nothing, the way it does for a read-only [os.OpenFile].
			name:       "a read-only open ignores the truncate flag",
			wrap:       plainFSSet,
			seed:       seedAlpha,
			file:       "a.txt",
			flag:       os.O_RDONLY | os.O_TRUNC,
			read:       "alpha",
			wantFiles:  map[string]string{"a.txt": "alpha"},
			wantCloses: 1,
		},
		{
			// O_RDWR is the only access mode whose handle can do both.
			name:       "O_RDWR reads and writes",
			wrap:       createFSSet,
			seed:       seedAlpha,
			file:       "a.txt",
			flag:       os.O_RDWR | os.O_TRUNC,
			write:      "beta",
			read:       "beta",
			wantFiles:  map[string]string{"a.txt": "beta"},
			wantOpens:  1,
			wantCloses: 1,
		},
		{
			// A file written through this polyfill starts out empty, so there is
			// nothing for O_APPEND to go past.
			name:      "O_APPEND starts from an empty file",
			wrap:      createFSSet,
			file:      "b.txt",
			flag:      os.O_WRONLY | os.O_CREATE | os.O_APPEND,
			write:     "beta",
			wantFiles: map[string]string{"b.txt": "beta"},
			wantPerms: map[string]fs.FileMode{"b.txt": 0},
			wantOpens: 1,
		},
		{
			// A directory cannot hold what the caller is about to write into it,
			// so it is refused rather than handed back as a handle that fails on
			// the first write.
			name:       "a directory cannot be opened for writing",
			wrap:       createFSSet,
			seed:       seedDirD,
			file:       "d",
			flag:       os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
			wantErr:    "is a directory",
			wantErrIs:  fs.ErrInvalid,
			wantFiles:  map[string]string{},
			wantDirs:   []string{"d"},
			wantCloses: 1,
		},
		{
			// OpenFile is documented not to make parents, so it must not reach for
			// MkdirAll either: the file system reports the missing directory
			// itself and nothing is created for it.
			name:      "a missing parent is not created",
			wrap:      createFSSet,
			file:      "d/b.txt",
			flag:      os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
			wantErr:   "file does not exist",
			wantErrIs: fs.ErrNotExist,
			wantFiles: map[string]string{},
		},
		{
			name:      "the root is refused",
			wrap:      createFSSet,
			seed:      seedAlpha,
			file:      pathutil.CwdPath,
			flag:      os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
			wantErr:   "the root is a directory",
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			name:      "an invalid path is refused",
			wrap:      createFSSet,
			seed:      seedAlpha,
			file:      "../escape",
			flag:      os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
			wantErr:   "is not a valid path",
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			// A file system with its own OpenFileFS is asked even for a name the
			// emulation would refuse, because it knows its own tree better than a
			// standin does.
			name:      "the root is left to the native method",
			wrap:      openFileFSSet,
			file:      pathutil.CwdPath,
			flag:      os.O_WRONLY,
			wantErr:   "invalid argument",
			wantFiles: map[string]string{},
		},
		{
			name:      "no way to hand back a writable handle",
			wrap:      plainFSSet,
			seed:      seedAlpha,
			file:      "b.txt",
			flag:      os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
			wantErr:   `does not support "open"`,
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			// A batch interface writes whole files at once, so it cannot hand back
			// a handle to write through and is no use here.
			name:      "BatchCreateFS alone is not enough",
			wrap:      batchFSSet,
			seed:      seedAlpha,
			file:      "b.txt",
			flag:      os.O_WRONLY | os.O_CREATE | os.O_TRUNC,
			wantErr:   `does not support "open"`,
			wantFiles: map[string]string{"a.txt": "alpha"},
		},
		{
			// A stat that fails for a reason other than a missing name leaves the
			// name unknown, which is not a reason to invent an answer: whatever
			// the file system reports is what the caller gets.
			name:       "a name that cannot be queried is reported as it is",
			wrap:       brokenFSSet,
			file:       "b.txt",
			flag:       os.O_RDONLY,
			wantErr:    "permission denied",
			wantErrIs:  fs.ErrPermission,
			wantFiles:  map[string]string{},
			wantCloses: 0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := newStore()
			if tc.seed != nil {
				tc.seed(s)
			}
			f, err := OpenFile(tc.wrap(s), tc.file, tc.flag, tc.perm)
			checkWantErr(t, "OpenFile", err, tc.wantErr)
			if tc.wantErrIs != nil {
				if err == nil {
					t.Errorf("OpenFile(%q) = %v, want it to be %v", tc.file, err, tc.wantErrIs)
				} else if !errors.Is(err, tc.wantErrIs) {
					t.Errorf("OpenFile(%q) = %v, want it to be %v", tc.file, err, tc.wantErrIs)
				}
			}
			if err == nil && f == nil {
				t.Fatalf("OpenFile(%q) = a nil handle and no error, want a handle", tc.file)
			}
			if f != nil {
				// The handle the caller was handed must still be open, so the
				// only close allowed is the one querying the name needed for
				// itself.
				if s.closeCalls != tc.wantCloses {
					t.Errorf("OpenFile(%q) closed %d handles, want %d", tc.file, s.closeCalls, tc.wantCloses)
				}
				if tc.write != "" {
					_, werr := f.WriteString(tc.write)
					checkWantErr(t, "WriteString", werr, tc.wantWriteErr)
				}
				if tc.wantReadErr != "" {
					_, rerr := f.Read(make([]byte, len("alpha")))
					checkWantErr(t, "Read", rerr, tc.wantReadErr)
				} else if tc.read != "" {
					data, rerr := io.ReadAll(f)
					if rerr != nil {
						t.Fatalf("Read() = %v, want %q", rerr, tc.read)
					}
					if got := string(data); got != tc.read {
						t.Errorf("Read() = %q, want %q", got, tc.read)
					}
				}
				if err := f.Close(); err != nil {
					t.Errorf("Close() = %v, want nil", err)
				}
				if s.closeCalls != tc.wantCloses+1 {
					t.Errorf("OpenFile(%q) closed %d handles after Close, want %d",
						tc.file, s.closeCalls, tc.wantCloses+1)
				}
			}

			if len(s.files) != len(tc.wantFiles) {
				t.Errorf("OpenFile(%q) left %d files (%v), want %d (%v)", tc.file,
					len(s.files), slices.Sorted(maps.Keys(s.files)),
					len(tc.wantFiles), slices.Sorted(maps.Keys(tc.wantFiles)))
			}
			for name, want := range tc.wantFiles {
				if got := string(s.files[name]); got != want {
					t.Errorf("OpenFile(%q) left %q in %q, want %q", tc.file, got, name, want)
				}
			}
			for name, want := range tc.wantPerms {
				if got := s.perms[name]; got != want {
					t.Errorf("OpenFile(%q) perm for %q = %v, want %v", tc.file, name, got, want)
				}
			}
			if dirs := createdDirs(s); !reflect.DeepEqual(dirs, tc.wantDirs) {
				t.Errorf("OpenFile(%q) left directories %v, want %v", tc.file, dirs, tc.wantDirs)
			}
			if s.openCalls != tc.wantOpens {
				t.Errorf("OpenFile(%q) opened %d handles, want %d", tc.file, s.openCalls, tc.wantOpens)
			}
			// OpenFile writes one file through a handle, so it never makes a
			// parent directory and never writes a batch of whole files.
			if s.mkdirAllCalls != nil {
				t.Errorf("OpenFile(%q) called MkdirAll %v, want no call", tc.file, s.mkdirAllCalls)
			}
			if s.batches != nil {
				t.Errorf("OpenFile(%q) called BatchCreate %v, want no call", tc.file, s.batches)
			}
		})
	}
}

// TestOpenFileHandle checks that the handle OpenFile hands back is a whole
// [types.File] that refuses the methods its access mode does not allow, since
// that is what makes it mean the same thing as an [os.File] opened with the same
// flags. A file from a file system without [types.OpenFileFS] carries no mode of
// its own.
func TestOpenFileHandle(t *testing.T) {
	t.Parallel()

	t.Run("read only", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		s.seedFile("a.txt", "alpha")
		f, err := OpenFile(plainFSSet(s), "a.txt", os.O_RDONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile() = %v, want nil", err)
		}
		if _, err := f.Write([]byte("beta")); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Write() = %v, want permission denied", err)
		}
		if _, err := f.WriteString("beta"); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("WriteString() = %v, want permission denied", err)
		}
		head := make([]byte, 3)
		if n, err := f.ReadAt(head, 0); err != nil {
			t.Errorf("ReadAt() = %d, %v, want the content", n, err)
		}
		if string(head) != "alp" {
			t.Errorf("ReadAt() read %q, want %q", string(head), "alp")
		}
		if pos, err := f.Seek(0, io.SeekStart); err != nil || pos != 0 {
			t.Errorf("Seek() = %d, %v, want 0 and nil", pos, err)
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat() = %v, want nil", err)
		}
		if info.Name() != "a.txt" || info.Size() != int64(len("alpha")) {
			t.Errorf("Stat() = %q, %d, want %q and %d", info.Name(), info.Size(), "a.txt", len("alpha"))
		}
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("Read() = %v, want nil", err)
		}
		if got := string(data); got != "alpha" {
			t.Errorf("Read() = %q, want %q", got, "alpha")
		}
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if got := string(s.files["a.txt"]); got != "alpha" {
			t.Errorf("a read-only open left %q in a.txt, want %q", got, "alpha")
		}
	})

	t.Run("write only", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		s.seedFile("a.txt", "alpha")
		f, err := OpenFile(createFSSet(s), "a.txt", os.O_WRONLY|os.O_TRUNC, 0)
		if err != nil {
			t.Fatalf("OpenFile() = %v, want nil", err)
		}
		// The fake underneath can read whatever it holds, so a read here can
		// only be blocked by the access mode.
		if _, err := f.Read(make([]byte, 3)); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Read() = %v, want permission denied", err)
		}
		if _, err := f.ReadAt(make([]byte, 3), 0); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("ReadAt() = %v, want permission denied", err)
		}
		if _, err := f.Write([]byte("beta")); err != nil {
			t.Fatalf("Write() = %v, want nil", err)
		}
		if _, err := f.WriteString("!"); err != nil {
			t.Fatalf("WriteString() = %v, want nil", err)
		}
		if pos, err := f.Seek(0, io.SeekStart); err != nil || pos != 0 {
			t.Errorf("Seek() = %d, %v, want 0 and nil", pos, err)
		}
		info, err := f.Stat()
		if err != nil {
			t.Fatalf("Stat() = %v, want nil", err)
		}
		if info.Size() != int64(len("beta!")) {
			t.Errorf("Stat().Size() = %d, want %d", info.Size(), len("beta!"))
		}
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if got := string(s.files["a.txt"]); got != "beta!" {
			t.Errorf("OpenFile() left %q in a.txt, want %q", got, "beta!")
		}
	})

	t.Run("read and write", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		f, err := OpenFile(createFSSet(s), "b.txt", os.O_RDWR|os.O_CREATE, 0)
		if err != nil {
			t.Fatalf("OpenFile() = %v, want nil", err)
		}
		if _, err := f.WriteString("beta"); err != nil {
			t.Fatalf("WriteString() = %v, want nil", err)
		}
		head := make([]byte, 4)
		if n, err := f.ReadAt(head, 0); err != nil {
			t.Errorf("ReadAt() = %d, %v, want the content that was written", n, err)
		}
		if string(head) != "beta" {
			t.Errorf("ReadAt() read %q, want %q", string(head), "beta")
		}
		data, err := io.ReadAll(f)
		if err != nil {
			t.Fatalf("Read() = %v, want nil", err)
		}
		if got := string(data); got != "beta" {
			t.Errorf("Read() = %q, want %q", got, "beta")
		}
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
		if got := string(s.files["b.txt"]); got != "beta" {
			t.Errorf("OpenFile() left %q in b.txt, want %q", got, "beta")
		}
	})

	// Listing is not a method the access mode limits, so a read-only open of a
	// directory keeps it.
	t.Run("directory", func(t *testing.T) {
		t.Parallel()

		s := newStore()
		s.seedDir("d")
		s.seedFile("d/a.txt", "alpha")
		f, err := OpenFile(listFSSet(s), "d", os.O_RDONLY, 0)
		if err != nil {
			t.Fatalf("OpenFile() = %v, want nil", err)
		}
		rd, ok := f.(fs.ReadDirFile)
		if !ok {
			t.Fatalf("OpenFile() = a handle without ReadDir, want a directory that can be listed")
		}
		entries, err := rd.ReadDir(-1)
		if err != nil {
			t.Fatalf("ReadDir() = %v, want nil", err)
		}
		if len(entries) != 1 || entries[0].Name() != "a.txt" {
			t.Errorf("ReadDir() = %v, want a.txt", entries)
		}
		if _, err := f.Write([]byte("beta")); !errors.Is(err, fs.ErrPermission) {
			t.Errorf("Write() = %v, want permission denied", err)
		}
		if err := f.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})
}

func openFileFSSet(s *store) fs.FS     { return openFileFS{s} }
func plainFSSet(s *store) fs.FS        { return plainFS{s} }
func createFSSet(s *store) fs.FS       { return createFS{s} }
func batchFSSet(s *store) fs.FS        { return batchFS{s} }
func brokenFSSet(s *store) fs.FS       { return brokenFS{s} }
func openWritableFSSet(s *store) fs.FS { return openWritableFS{s} }
func listFSSet(s *store) fs.FS         { return listFS{s} }
