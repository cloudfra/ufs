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

package ops_test

import (
	"errors"
	"io/fs"
	"net/url"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	_ "github.com/cloudfra/ufs/drivers/decorators/faultfs"
	"github.com/cloudfra/ufs/drivers/decorators/readonlyfs"
	_ "github.com/cloudfra/ufs/drivers/decorators/telemetryfs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/ops"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// withOptions returns name with the decorator options opts, in the YAML flow
// form of the options query parameter.
func withOptions(name, opts string) string {
	return name + "?options=" + url.QueryEscape(opts)
}

// newChtimesFixture writes "dir/file" holding "content" to fsys and sets the
// modification time of both to past through the file system's native
// Chtimes.
func newChtimesFixture(t *testing.T, fsys types.WriteFS, past time.Time) {
	t.Helper()
	if err := fsys.MkdirAll("dir", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	writeTestFile(t, fsys, "dir/file", []byte("content"))
	cfs, ok := fsys.(types.ChtimesFS)
	if !ok {
		t.Fatalf("%T does not implement types.ChtimesFS", fsys)
	}
	for _, name := range []string{"dir/file", "dir"} {
		if err := cfs.Chtimes(name, past, past); err != nil {
			t.Fatalf("Chtimes(%q) = %v", name, err)
		}
	}
}

// noChtimesFS hides the ChtimesFS of the file system it wraps, so that
// ops.Chtimes takes its rewrite fallback.
type noChtimesFS struct {
	types.WriteFS
}

func assertModTime(t *testing.T, fsys types.ReadFS, name string, want time.Time) {
	t.Helper()
	info, err := fsys.Stat(name)
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(want) {
		t.Errorf("Stat(%q).ModTime() = %v, want %v", name, info.ModTime(), want)
	}
}

func newFS(t *testing.T, name string) types.WriteFS {
	t.Helper()
	fsys, err := ufs.New(t.Context(), name)
	if err != nil {
		t.Fatalf("New(%q) = %v", name, err)
	}
	return fsys
}

func TestChtimes(t *testing.T) {
	t.Parallel()
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	mtime := past.Add(-24 * time.Hour)
	atime := past.Add(-48 * time.Hour)
	for _, tc := range []struct {
		name  string
		setup func(t *testing.T) types.WriteFS
	}{
		{"local", func(t *testing.T) types.WriteFS { return newFS(t, t.TempDir()) }},
		{"memory", func(*testing.T) types.WriteFS { return core.MakeMemFS("memory://chtimes") }},
		{"nested", func(t *testing.T) types.WriteFS { return newFS(t, "memory:") }},
		{"fault", func(t *testing.T) types.WriteFS { return newFS(t, withOptions("memory:", "[{fault: {errorRate: 0}}]")) }},
		{"telemetry", func(t *testing.T) types.WriteFS { return newFS(t, withOptions("memory:", "[{telemetry: true}]")) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			fsys := tc.setup(t)
			t.Cleanup(ufsTesting.ValidateClose(t, fsys))
			newChtimesFixture(t, fsys, past)

			// Zero times leave the times as they are.
			if err := ops.Chtimes(fsys, "dir/file", time.Time{}, time.Time{}); err != nil {
				t.Fatalf("Chtimes(zero times) = %v", err)
			}
			assertModTime(t, fsys, "dir/file", past)

			for _, name := range []string{"dir/file", "dir"} {
				if err := ops.Chtimes(fsys, name, atime, mtime); err != nil {
					t.Fatalf("Chtimes(%q) = %v", name, err)
				}
				assertModTime(t, fsys, name, mtime)
			}
			if got, err := fsys.ReadFile("dir/file"); err != nil || string(got) != "content" {
				t.Errorf("ReadFile() = (%q, %v), want (%q, nil)", got, err, "content")
			}

			if err := ops.Chtimes(fsys, "dir/missing", atime, mtime); !errors.Is(err, fs.ErrNotExist) {
				t.Errorf("Chtimes(missing) = %v, want %v", err, fs.ErrNotExist)
			}
			ufsTesting.AssertInvalidPathError(t, "../escape", ops.Chtimes(fsys, "../escape", atime, mtime), "chtimes")
		})
	}
}

func TestChtimesFallback(t *testing.T) {
	t.Parallel()
	past := time.Now().Add(-time.Hour).Truncate(time.Second)
	inner := core.MakeMemFS("memory://chtimes-fallback")
	newChtimesFixture(t, inner, past)
	fsys := noChtimesFS{inner}
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))

	if err := ops.Chtimes(fsys, "dir/file", time.Time{}, time.Time{}); err != nil {
		t.Fatalf("Chtimes(zero times) = %v", err)
	}
	assertModTime(t, fsys, "dir/file", past)

	// The rewrite stamps the current time, not the time asked for.
	if err := ops.Chtimes(fsys, "dir/file", past, past.Add(-time.Hour)); err != nil {
		t.Fatalf("Chtimes() = %v", err)
	}
	info, err := fsys.Stat("dir/file")
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().After(past) {
		t.Errorf("ModTime() after the fallback = %v, want the rewrite time (after %v)", info.ModTime(), past)
	}
	if got, err := fsys.ReadFile("dir/file"); err != nil || string(got) != "content" {
		t.Errorf("ReadFile() = (%q, %v), want (%q, nil)", got, err, "content")
	}

	if err := ops.Chtimes(fsys, "dir", past, past); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Chtimes(dir) = %v, want %v", err, errors.ErrUnsupported)
	}
	if err := ops.Chtimes(fsys, "dir/missing", past, past); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Chtimes(missing) = %v, want %v", err, fs.ErrNotExist)
	}
	ufsTesting.AssertInvalidPathError(t, "../escape", ops.Chtimes(fsys, "../escape", past, past), "chtimes")
}

func TestChtimesReadOnly(t *testing.T) {
	t.Parallel()
	inner := core.MakeMemFS("memory://chtimes-ro")
	writeTestFile(t, inner, "file", nil)
	fsys := readonlyfs.New(inner)
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	now := time.Now()
	if err := ops.Chtimes(fsys, "file", now, now); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Chtimes() on a read-only file system = %v, want %v", err, fs.ErrPermission)
	}
	ufsTesting.AssertInvalidPathError(t, "../escape", fsys.(types.ChtimesFS).Chtimes("../escape", now, now), "chtimes")
}

func TestChtimesClosed(t *testing.T) {
	t.Parallel()
	now := time.Now()
	for _, fsys := range []types.WriteFS{core.MakeMemFS("memory://chtimes-closed"), newFS(t, "memory:")} {
		if err := fsys.Close(); err != nil {
			t.Fatal(err)
		}
		if err := ops.Chtimes(fsys, "file", now, now); !errors.Is(err, fs.ErrClosed) {
			t.Errorf("Chtimes() on a closed %T = %v, want %v", fsys, err, fs.ErrClosed)
		}
	}
}

func TestChtimesFaultInjected(t *testing.T) {
	t.Parallel()
	fsys := newFS(t, withOptions("memory:", "[{fault: {errorRate: 1}}]"))
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))
	now := time.Now()
	if err := ops.Chtimes(fsys, "file", now, now); err == nil {
		t.Error("Chtimes() with errorRate 1 = nil, want an injected error")
	}
}
