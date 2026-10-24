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

package ufs_test

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// testAssetsArchivesDir holds the archive files that the tests read.
const testAssetsArchivesDir = "testing/testassets/archives"

// nestedArchiveContents are paths under the root of the content of
// nested-testassets.zip and the content of the file each one holds, so an
// archive inside the archive is reached through its own content directory. The
// test assets hold the path of every file inside testing/testassets/files as
// its own content, so the wanted content says which file was read.
var nestedArchiveContents = []struct {
	name string
	want string
}{
	{
		name: "index.html",
		want: "testing/testassets/files/index.html",
	},
	{
		name: "single-testassets.zip.d/index.html",
		want: "testing/testassets/files/index.html",
	},
	{
		name: "testassets.7z.d/assets/four/4.txt",
		want: "testing/testassets/files/assets/four/4.txt",
	},
}

// tempArchiveFS is the file system that a remote archive is mounted from: an
// archive read out of a local temporary directory that is deleted when the
// file system is closed. It stands in for the file system that the
// http-archive driver builds, which a test cannot open for itself: that
// driver's HTTP client rejects loopback addresses, so no test server can serve
// the archive. The archive is the real one, opened from inside the temporary
// directory, so an archive inside it is mounted from an open file of it rather
// than from a host path, as it is for the driver.
type tempArchiveFS struct {
	types.WriteFS
	dir string
}

// Close releases the archive and deletes the temporary directory that holds
// it, as the cleanup of the http-archive driver does.
func (fsys *tempArchiveFS) Close() error {
	return ufserrors.Join(fsys.WriteFS.Close(), osutil.DeleteDirectory(fsys.dir))
}

// tempArchiveDrivers counts the drivers that registerTempArchiveDriver makes,
// so that a name is registered only once even when the test binary runs the
// same test more than once (-count).
var tempArchiveDrivers atomic.Uint64

// registerTempArchiveDriver registers a driver that opens the test archive the
// way a remote archive is opened: in a temporary directory of its own, which is
// deleted when the file system is closed. The driver is named after the test
// that registers it, so that tests running in parallel do not share it or the
// directory they check. It returns the URI to mount it with and the directory
// it made, which the driver fills in when the mount is opened.
func registerTempArchiveDriver(t *testing.T) (uri string, tempDir *string) {
	t.Helper()

	name := fmt.Sprintf("test-temparchive-%d-%s", tempArchiveDrivers.Add(1),
		strings.NewReplacer("/", "-", " ", "-").Replace(t.Name()))
	tempDir = new(string)
	ufs.Register(ufs.NewDriver(name,
		func(ctx context.Context, _ string) (types.WriteFS, error) {
			dir, err := copyTestArchiveToTempDir("nested-testassets.zip")
			if err != nil {
				return nil, err
			}
			archive, err := ufs.NewNestFS(ctx, filepath.Join(dir, "nested-testassets.zip"))
			if err != nil {
				return nil, ufserrors.Join(err, osutil.DeleteDirectory(dir))
			}
			*tempDir = dir
			return &tempArchiveFS{WriteFS: archive, dir: dir}, nil
		},
		func(uri string) bool { return uri == name+":" },
		1, false, true))

	return name + ":", tempDir
}

// TestReleaseNestedArchiveMountsLeavesNoOpenFiles checks that releasing the
// mounts of an archive that has archives of its own lets go of every local file
// it holds: the handle on the outer archive and the handles on the archives
// inside it all have to be let go, and the inner ones before the outer one, so
// that nothing keeps a file open that another application wants to replace or
// remove. The archives are found in the tree, so nestFS mounts their content
// directories itself, which makes them mounts of its own and the ones releasing
// lets go of.
func TestReleaseNestedArchiveMountsLeavesNoOpenFiles(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	dir, err := copyTestArchiveToTempDir("nested-testassets.zip")
	if err != nil {
		t.Fatalf("cannot copy the archive into a temporary directory, %v", err)
	}
	defer func() {
		if err := osutil.DeleteDirectory(dir); err != nil {
			t.Errorf("DeleteDirectory(%q) = %v, want nil", dir, err)
		}
	}()

	// The builder wraps the directory in the same nestFS layer that finds
	// archives in the tree and mounts their content directories itself.
	fsys, err := ufs.NewFSBuilder(dir).Build(ctx)
	if err != nil {
		t.Fatalf("cannot open %q, %v", dir, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	// Reading the paths mounts the archive and the archives inside it.
	for _, tt := range nestedArchiveContents {
		ufsTesting.AssertContains(t, fsys, path.Join("nested-testassets.zip.d", tt.name), tt.want)
	}

	// The archives are normally open here, which is what the check after
	// Release is out to catch; say so when they are not, so that a passing
	// check is not mistaken for a meaningful one.
	open := openFilesUnder(dir)
	if len(open) == 0 {
		t.Logf("no file is open under %q before Release, so there is no handle to let go of", dir)
	}

	if err := fsys.Release(); err != nil {
		t.Fatalf("Release() = %v, want nil", err)
	}

	if open := openFilesUnder(dir); len(open) != 0 {
		t.Errorf("Release() left %d files open under %q: %q", len(open), dir, open)
	}

	// The archive file belongs to the caller, not to ufs, so releasing leaves
	// it where it is.
	if !osutil.Exists(filepath.Join(dir, "nested-testassets.zip")) {
		t.Errorf("Release() removed the archive file in %q", dir)
	}

	// Releasing twice is not an error, and the second release has nothing
	// left to release.
	if err := fsys.Release(); err != nil {
		t.Errorf("Release() the second time = %v, want nil", err)
	}

	// fsys itself is not closed, so it is still usable, and the content
	// directories are mounted again from the archive files that hold them.
	for _, tt := range nestedArchiveContents {
		ufsTesting.AssertContains(t, fsys, path.Join("nested-testassets.zip.d", tt.name), tt.want)
	}
	f, err := fsys.Create("keep.txt")
	if err != nil {
		t.Errorf("Create(%q) after Release() = %v, want nil", "keep.txt", err)
	} else {
		if _, err := f.Write([]byte("keep")); err != nil {
			t.Errorf("Write(%q) after Release() = %v, want nil", "keep.txt", err)
		}
		ufsTesting.Must(t, f.Close())
	}
	ufsTesting.AssertContains(t, fsys, "keep.txt", "keep")
}

// TestReleaseKeepsTempFilesOfMountsTheCallerConfigured is the other side of the
// same setup: the archive held in a local temporary directory is mounted by the
// caller, either by giving ufs a URI to open or by opening it and handing the
// file system over, so releasing leaves it alone, the copy in the temporary
// directory and the handle on it included. Only closing the file system lets go
// of them.
func TestReleaseKeepsTempFilesOfMountsTheCallerConfigured(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		open func(t *testing.T, ctx context.Context, uri string) (ufs.FS, error)
	}{
		{
			name: "uri binding",
			open: func(_ *testing.T, ctx context.Context, uri string) (ufs.FS, error) {
				return ufs.NewFSBuilder("memory://").Mount("deep", uri).Build(ctx)
			},
		},
		{
			name: "handed file system",
			open: func(_ *testing.T, ctx context.Context, uri string) (ufs.FS, error) {
				archive, err := ufs.New(ctx, uri)
				if err != nil {
					return nil, err
				}
				return ufs.NewFSBuilder("memory://").MountFS("deep", archive).Build(ctx)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			uri, tempDir := registerTempArchiveDriver(t)

			fsys, err := tt.open(t, ctx, uri)
			if err != nil {
				t.Fatalf("cannot build the file system, %v", err)
			}
			defer ufsTesting.ValidateClose(t, fsys)()

			for _, tt := range nestedArchiveContents {
				ufsTesting.AssertContains(t, fsys, path.Join("deep", tt.name), tt.want)
			}
			if *tempDir == "" {
				t.Fatal("the driver did not report the temporary directory it made")
			}

			if err := fsys.Release(); err != nil {
				t.Fatalf("Release() = %v, want nil", err)
			}

			if !osutil.Exists(*tempDir) {
				t.Errorf("Release() deleted the temporary directory %q of a mount the caller configured", *tempDir)
			}
			// The mount is still there and still serves the archives inside
			// the archive, which is what the caller expects of a mount it
			// configured.
			for _, tt := range nestedArchiveContents {
				ufsTesting.AssertContains(t, fsys, path.Join("deep", tt.name), tt.want)
			}

			ufsTesting.Must(t, fsys.Close())
			if osutil.Exists(*tempDir) {
				t.Errorf("Close() left the temporary directory %q of the mount the caller configured behind", *tempDir)
			}
		})
	}
}

// copyTestArchiveToTempDir copies the test archive named name into a temporary
// directory of its own and returns that directory.
func copyTestArchiveToTempDir(name string) (string, error) {
	dir, _, err := osutil.CreateTempDirectory()
	if err != nil {
		return "", err
	}
	if err := copyTestArchive(name, dir); err != nil {
		return "", ufserrors.Join(err, osutil.DeleteDirectory(dir))
	}
	return dir, nil
}

// copyTestArchive copies the test archive named name into the directory dir.
func copyTestArchive(name string, dir string) error {
	data, err := osutil.ReadFile(filepath.Join(testAssetsArchivesDir, name))
	if err != nil {
		return err
	}
	return osutil.WriteFile(filepath.Join(dir, name), data)
}

// openFilesUnder returns the files that this process holds open under dir. It
// returns nothing on a system without /proc, where the caller is left with the
// check that the directory itself is gone.
func openFilesUnder(dir string) []string {
	const fdDir = "/proc/self/fd"

	entries, err := osutil.ReadDir(fdDir)
	if err != nil {
		return nil
	}

	var open []string
	for _, entry := range entries {
		target, err := os.Readlink(filepath.Join(fdDir, entry.Name()))
		if err != nil {
			continue
		}
		if strings.HasPrefix(target, dir+string(os.PathSeparator)) {
			open = append(open, target)
		}
	}
	return open
}
