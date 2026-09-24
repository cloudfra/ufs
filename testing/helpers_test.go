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

package testing_test

import (
	"errors"
	"fmt"
	"io/fs"
	"runtime"
	"slices"
	"testing"
	"testing/fstest"
	"time"

	utesting "github.com/cloudfra/ufs/testing"
)

// ---------- test fixtures ----------

// fakeCloser implements io.Closer and records whether Close() was called.
type fakeCloser struct {
	closed bool
	err    error
}

func (f *fakeCloser) Close() error {
	f.closed = true
	return f.err
}

// captureTB embeds a *testing.T to satisfy testing.TB, and overrides Errorf
// and Fatalf to record calls without failing the test.
type captureTB struct {
	*testing.T
	errors []string
	fatals []string
}

func (m *captureTB) Errorf(format string, args ...any) {
	m.errors = append(m.errors, fmt.Sprintf(format, args...))
}

func (m *captureTB) Error(args ...any) {
	m.errors = append(m.errors, fmt.Sprint(args...))
}

// Fatalf records the fatal message instead of calling the embedded
// (*testing.T).Fatalf, which would stop the goroutine and mark the enclosing
// test as failed.
func (m *captureTB) Fatalf(format string, args ...any) {
	m.fatals = append(m.fatals, fmt.Sprintf(format, args...))
}

// runFatal runs fn with a captureTB whose Fatalf stops the goroutine, matching
// the real testing.TB contract, so helpers that rely on Fatalf not returning
// can be tested. It returns the captured TB after fn exits.
func runFatal(t *testing.T, fn func(tb testing.TB)) *captureTB {
	t.Helper()
	m := &captureTB{T: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		fn(&goexitTB{captureTB: m})
	}()
	<-done
	return m
}

// goexitTB is a captureTB whose Fatalf records the message and then calls
// runtime.Goexit, like (*testing.T).Fatalf.
type goexitTB struct {
	*captureTB
}

func (g *goexitTB) Fatalf(format string, args ...any) {
	g.captureTB.Fatalf(format, args...)
	runtime.Goexit()
}

// openOnlyFS implements fs.FS but not fs.ReadDirFS.
type openOnlyFS struct {
	fs.FS
}

// noReadDirFileFS implements fs.ReadDirFS, but Open returns files that do not
// implement fs.ReadDirFile.
type noReadDirFileFS struct {
	fstest.MapFS
}

func (n noReadDirFileFS) Open(name string) (fs.File, error) {
	f, err := n.MapFS.Open(name)
	if err != nil {
		return nil, err
	}
	return struct{ fs.File }{f}, nil
}

// fakeDirEntry implements fs.DirEntry and fs.FileInfo.
type fakeDirEntry struct {
	name  string
	isDir bool
}

func (e fakeDirEntry) Name() string { return e.name }
func (e fakeDirEntry) IsDir() bool  { return e.isDir }
func (e fakeDirEntry) Type() fs.FileMode {
	if e.isDir {
		return fs.ModeDir
	}
	return 0
}
func (e fakeDirEntry) Info() (fs.FileInfo, error) { return e, nil }
func (e fakeDirEntry) Size() int64                { return 0 }
func (e fakeDirEntry) Mode() fs.FileMode {
	if e.isDir {
		return fs.ModeDir
	}
	return 0
}
func (e fakeDirEntry) ModTime() time.Time { return time.Time{} }
func (e fakeDirEntry) Sys() any           { return nil }

// ---------- ValidateClose ----------

func TestValidateClose(t *testing.T) {
	t.Parallel()

	t.Run("nil_closer_is_noop", func(t *testing.T) {
		t.Parallel()
		utesting.ValidateClose(t, nil)()
	})

	t.Run("successful_close", func(t *testing.T) {
		t.Parallel()
		c := &fakeCloser{err: nil}
		utesting.ValidateClose(t, c)()
		if !c.closed {
			t.Error("expected Close() to be called")
		}
	})

	t.Run("failing_close_reports_error", func(t *testing.T) {
		m := &captureTB{T: t}
		c := &fakeCloser{err: errors.New("close failed")}
		utesting.ValidateClose(m, c)()
		if !c.closed {
			t.Fatal("expected Close() to be called")
		}
		if len(m.errors) != 1 {
			t.Fatalf("expected 1 captured error, got %d: %v", len(m.errors), m.errors)
		}
	})
}

// ---------- WantCloseError ----------

func TestWantCloseError(t *testing.T) {
	t.Parallel()

	t.Run("nil_closer_is_noop", func(t *testing.T) {
		t.Parallel()
		utesting.WantCloseError(t, nil)()
	})

	t.Run("close_returns_error_no_failure", func(t *testing.T) {
		t.Parallel()
		c := &fakeCloser{err: errors.New("expected error")}
		utesting.WantCloseError(t, c)()
		if !c.closed {
			t.Error("expected Close() to be called")
		}
	})

	t.Run("close_returns_nil_reports_error", func(t *testing.T) {
		m := &captureTB{T: t}
		c := &fakeCloser{err: nil}
		utesting.WantCloseError(m, c)()
		if !c.closed {
			t.Fatal("expected Close() to be called")
		}
		if len(m.errors) != 1 {
			t.Fatalf("expected 1 captured error, got %d: %v", len(m.errors), m.errors)
		}
	})
}

// ---------- SkipTestOnWindows ----------

func TestSkipTestOnWindows(t *testing.T) {
	t.Parallel()
	// On non-Windows (Linux), SkipTestOnWindows must NOT call tb.Skip.
	// If this line executes, the test was not skipped — correct behavior.
	utesting.SkipTestOnWindows(t)
	if t.Skipped() {
		t.Error("unexpectedly skipped on non-Windows platform")
	}
}

// ---------- DirEntryListToNames ----------

func TestDirEntryListToNames(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		got := utesting.DirEntryListToNames(nil)
		if len(got) != 0 {
			t.Errorf("DirEntryListToNames(nil) returned %v, want empty slice", got)
		}
	})

	t.Run("empty_slice", func(t *testing.T) {
		t.Parallel()
		got := utesting.DirEntryListToNames([]fs.DirEntry{})
		if len(got) != 0 {
			t.Errorf("DirEntryListToNames(empty) returned %v, want empty slice", got)
		}
	})

	t.Run("order_preserved", func(t *testing.T) {
		t.Parallel()
		entries := []fs.DirEntry{
			fakeDirEntry{name: "beta", isDir: false},
			fakeDirEntry{name: "gamma", isDir: true},
			fakeDirEntry{name: "alpha", isDir: false},
		}
		got := utesting.DirEntryListToNames(entries)
		want := []string{"beta", "gamma", "alpha"}
		if len(got) != len(want) {
			t.Fatalf("len(got) = %d, want %d", len(got), len(want))
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("got[%d] = %q, want %q", i, got[i], want[i])
			}
		}
	})
}

// ---------- MustTime ----------

func TestMustTime(t *testing.T) {
	t.Parallel()

	t.Run("valid_rfc3339", func(t *testing.T) {
		t.Parallel()
		got := utesting.MustTime(t, "2006-01-02T15:04:05Z")
		want := time.Date(2006, 1, 2, 15, 4, 5, 0, time.UTC)
		if !got.Equal(want) {
			t.Errorf("MustTime = %v, want %v", got, want)
		}
	})

	t.Run("invalid_string_fails", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.MustTime(m, "not-a-valid-time")
		if len(m.fatals) != 1 {
			t.Fatalf("expected 1 captured fatal, got %d: %v", len(m.fatals), m.fatals)
		}
	})
}

// ---------- RandomString ----------

func TestRandomString(t *testing.T) {
	t.Parallel()

	for _, size := range []int{1, 10, 100, 1000} {
		sz := size
		t.Run(fmt.Sprintf("size_%d", sz), func(t *testing.T) {
			t.Parallel()
			got := utesting.RandomString(sz)
			if len(got) != sz {
				t.Errorf("RandomString(%d) length = %d, want %d", sz, len(got), sz)
			}
		})
	}

	t.Run("consecutive_calls_differ", func(t *testing.T) {
		t.Parallel()
		if a, b := utesting.RandomString(100), utesting.RandomString(100); a == b {
			t.Error("two RandomString(100) calls returned identical results")
		}
	})
}

// ---------- AssertInvalidPathError ----------

// TestAssertInvalidPathError verifies that AssertInvalidPathError accepts a
// correct *fs.PathError without reporting an error. Failure-detection paths
// (nil error, wrong Op, non-*PathError) inherently call t.Errorf, which cannot
// be captured without failing the test; they are implicitly covered by the
// integration tests in the parent ufs package that use this helper.
func TestAssertInvalidPathError(t *testing.T) {
	t.Parallel()

	t.Run("correct_patherror_passes", func(t *testing.T) {
		t.Parallel()
		perr := &fs.PathError{Op: "open", Path: "/tmp/test", Err: fs.ErrNotExist}
		utesting.AssertInvalidPathError(t, "/tmp/test", perr, "open")
		// Reaching this line without an error means no assertion failed. ✓
	})

	t.Run("different_op_correctly_matched", func(t *testing.T) {
		t.Parallel()
		perr := &fs.PathError{Op: "remove", Path: "/data/file.txt", Err: fs.ErrPermission}
		utesting.AssertInvalidPathError(t, "/data/file.txt", perr, "remove")
	})

	t.Run("empty_path_accepted", func(t *testing.T) {
		t.Parallel()
		perr := &fs.PathError{Op: "open", Path: "", Err: fs.ErrNotExist}
		utesting.AssertInvalidPathError(t, "", perr, "open")
	})
}

// ---------- Must ----------

func TestMust(t *testing.T) {
	t.Parallel()

	t.Run("nil_error_passes", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.Must(m, nil)
		if len(m.errors) != 0 {
			t.Errorf("expected no captured errors, got %v", m.errors)
		}
	})

	t.Run("non_nil_error_reported", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.Must(m, errors.New("boom"))
		if want := []string{"boom"}; !slices.Equal(m.errors, want) {
			t.Errorf("captured errors = %v, want %v", m.errors, want)
		}
	})
}

// ---------- ToMapKeys ----------

func TestToMapKeys(t *testing.T) {
	t.Parallel()

	t.Run("nil", func(t *testing.T) {
		t.Parallel()
		got := utesting.ToMapKeys[int](nil)
		if got == nil || len(got) != 0 {
			t.Errorf("ToMapKeys(nil) = %#v, want empty non-nil slice", got)
		}
	})

	t.Run("sorted", func(t *testing.T) {
		t.Parallel()
		got := utesting.ToMapKeys(map[string]bool{"gamma": true, "alpha": false, "beta": true})
		if want := []string{"alpha", "beta", "gamma"}; !slices.Equal(got, want) {
			t.Errorf("ToMapKeys = %v, want %v", got, want)
		}
	})
}

// ---------- AssertContains ----------

func TestAssertContains(t *testing.T) {
	t.Parallel()

	fsys := fstest.MapFS{
		"a.txt":     {Data: []byte("hello world")},
		"empty.txt": {Data: nil},
	}

	for _, tc := range []struct {
		name       string
		file       string
		substr     string
		wantErrors int
	}{
		{name: "contains", file: "a.txt", substr: "lo wo", wantErrors: 0},
		{name: "empty_substr", file: "empty.txt", substr: "", wantErrors: 0},
		{name: "missing_substr", file: "a.txt", substr: "goodbye", wantErrors: 1},
		{name: "missing_file", file: "nope.txt", substr: "x", wantErrors: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			m := &captureTB{T: t}
			utesting.AssertContains(m, fsys, tc.file, tc.substr)
			if len(m.errors) != tc.wantErrors {
				t.Errorf("captured %d errors, want %d: %v", len(m.errors), tc.wantErrors, m.errors)
			}
		})
	}
}

// ---------- AssertDir ----------

func TestAssertDir(t *testing.T) {
	t.Parallel()

	mapFS := fstest.MapFS{
		"dir/b.txt":     {},
		"dir/a.txt":     {},
		"dir/sub/c.txt": {},
		"emptydir":      {Mode: fs.ModeDir},
	}

	t.Run("match", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.AssertDir(m, mapFS, "dir", []string{"a.txt", "b.txt", "sub"})
		if len(m.errors) != 0 || len(m.fatals) != 0 {
			t.Errorf("unexpected failures: errors=%v fatals=%v", m.errors, m.fatals)
		}
	})

	t.Run("match_root", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.AssertDir(m, mapFS, ".", []string{"dir", "emptydir"})
		if len(m.errors) != 0 || len(m.fatals) != 0 {
			t.Errorf("unexpected failures: errors=%v fatals=%v", m.errors, m.fatals)
		}
	})

	t.Run("match_empty_dir", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.AssertDir(m, mapFS, "emptydir", []string{})
		if len(m.errors) != 0 || len(m.fatals) != 0 {
			t.Errorf("unexpected failures: errors=%v fatals=%v", m.errors, m.fatals)
		}
	})

	t.Run("mismatch_reports_both_paths", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.AssertDir(m, mapFS, "dir", []string{"a.txt", "sub"})
		if len(m.errors) != 2 {
			t.Errorf("captured %d errors, want 2: %v", len(m.errors), m.errors)
		}
	})

	t.Run("order_matters", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.AssertDir(m, mapFS, "dir", []string{"b.txt", "a.txt", "sub"})
		if len(m.errors) != 2 {
			t.Errorf("captured %d errors, want 2: %v", len(m.errors), m.errors)
		}
	})

	t.Run("missing_dir", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.AssertDir(m, mapFS, "nope", nil)
		if len(m.errors) != 2 {
			t.Errorf("captured %d errors, want 2: %v", len(m.errors), m.errors)
		}
	})

	t.Run("open_not_readdirfile", func(t *testing.T) {
		t.Parallel()
		m := &captureTB{T: t}
		utesting.AssertDir(m, noReadDirFileFS{mapFS}, "dir", []string{"a.txt", "b.txt", "sub"})
		if len(m.errors) != 1 {
			t.Errorf("captured %d errors, want 1: %v", len(m.errors), m.errors)
		}
	})

	t.Run("not_readdirfs_is_fatal", func(t *testing.T) {
		t.Parallel()
		m := runFatal(t, func(tb testing.TB) {
			utesting.AssertDir(tb, openOnlyFS{mapFS}, "dir", nil)
		})
		if len(m.fatals) != 1 {
			t.Errorf("captured %d fatals, want 1: %v", len(m.fatals), m.fatals)
		}
		if len(m.errors) != 0 {
			t.Errorf("unexpected errors after fatal: %v", m.errors)
		}
	})
}
