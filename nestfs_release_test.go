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

package ufs

import (
	"context"
	"path"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestCloseOnRelease checks what tells a mount that releasing closes apart from
// one it keeps: whether ufs opened the mount for itself, and whether the file
// system it holds is backed by a file that releasing can let go of.
func TestCloseOnRelease(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	archive := path.Join(testAssetsArchivesDir, "single-testassets.zip")

	// The two archive mounts hold the same kind of file system, so the only
	// difference between them is how they came to be mounted.
	parent := makeNestFS(ctx, mustBaseFS(t, "memory://"))
	handedOver := makeNestFS(ctx, mustBaseFS(t, archive))
	opened := makeNestFS(ctx, mustBaseFS(t, archive))
	ufsTesting.Must(t, parent.addMount("handed", handedOver))
	ufsTesting.Must(t, parent.addAutoMount("opened", opened))

	if handedOver.closeOnRelease() {
		t.Error("closeOnRelease() = true for a mount added with addMount, want it kept")
	}
	if !opened.closeOnRelease() {
		t.Error("closeOnRelease() = false for a mount added with addAutoMount, want it closed")
	}

	// A mount that holds no file has nothing for releasing to let go of, and
	// closing it would throw its content away, so it is kept even though ufs
	// opened it for itself.
	for _, name := range []string{"memory://", "null:"} {
		volatile := makeNestFS(ctx, mustBaseFS(t, name))
		ufsTesting.Must(t, parent.addAutoMount("volatile/"+name, volatile))
		if volatile.closeOnRelease() {
			t.Errorf("closeOnRelease() = true for an automatically mounted %s mount, want it kept", name)
		}
	}

	// The content directory that nestFS mounts for an archive is marked the
	// same way, which is what lets releasing let go of the archive file.
	fsys := mustNestFS(t, testAssetsArchivesDir)
	defer ufsTesting.ValidateClose(t, fsys)()
	ufsTesting.AssertContains(t, fsys, "single-testassets.zip.d/index.html", "testing/testassets/files/index.html")

	nfs := fsys.(*nestFS).mounts.getMount("single-testassets.zip.d")
	if nfs == nil {
		t.Fatal(`getMount("single-testassets.zip.d") = nil, want the archive mounted`)
	}
	if !nfs.closeOnRelease() {
		t.Error("closeOnRelease() = false for an archive content directory, want it closed")
	}
}

// TestReleaseKeepsMountsTheCallerConfigured checks that a mount the caller
// configures stays mounted through a release, in every form that configures a
// mount. The caller named these mounts, so they are the caller's bindings to
// keep, whether the caller gave a file system for ufs to mount or a URI for ufs
// to open. The archive mount is the one that has a file to let go of, so it is
// the one that would be dropped if releasing reached too far.
func TestReleaseKeepsMountsTheCallerConfigured(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	archiveURI := "archive://" + path.Join(pathutil.CwdPath, testAssetsArchivesDir, "single-testassets.zip")

	tests := []struct {
		name string
		open func(context.Context) (types.WriteFS, error)
	}{
		{
			name: "uri query",
			open: func(ctx context.Context) (types.WriteFS, error) {
				uri, err := CreateURI("memory://", map[string]string{"data": archiveURI, "mem": "memory://mem"})
				if err != nil {
					return nil, err
				}
				return New(ctx, uri)
			},
		},
		{
			name: "fstab",
			open: func(ctx context.Context) (types.WriteFS, error) {
				return New(ctx, "memory:// . auto rw\n"+archiveURI+" data auto ro\nmemory://mem mem auto rw\n")
			},
		},
		{
			name: "builder uri",
			open: func(ctx context.Context) (types.WriteFS, error) {
				return NewFSBuilder("memory://").
					Mount("data", archiveURI).
					Mount("mem", "memory://mem").
					Build(ctx)
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			fsys, err := tt.open(ctx)
			if err != nil {
				t.Fatalf("opening = %v, want nil", err)
			}
			defer ufsTesting.ValidateClose(t, fsys)()

			nFS, ok := fsys.(*nestFS)
			if !ok {
				t.Fatalf("opening = %T, want *nestFS", fsys)
			}
			for _, name := range []string{"data", "mem"} {
				nfs := nFS.mounts.getMount(name)
				if nfs == nil {
					t.Fatalf("getMount(%q) = nil, want the mount mounted before Release", name)
				}
				if nfs.closeOnRelease() {
					t.Errorf("closeOnRelease() = true for the %s mount the caller configured, want it kept", name)
				}
			}

			// What the caller wrote to its own mount outlives the release.
			writeTestFile(t, fsys, "mem/keep.txt", []byte("keep"))
			assertFileContent(t, fsys, "data/index.html", "testing/testassets/files/index.html")

			if err := nFS.Release(); err != nil {
				t.Fatalf("Release() = %v, want nil", err)
			}

			for _, name := range []string{"data", "mem"} {
				if nfs := nFS.mounts.getMount(name); nfs == nil {
					t.Errorf("getMount(%q) = nil after Release, want the mount the caller configured kept", name)
				}
			}
			assertFileContent(t, fsys, "data/index.html", "testing/testassets/files/index.html")
			assertFileContent(t, fsys, "mem/keep.txt", "keep")
		})
	}
}

// TestReleaseKeepsMountTheCallerBuilt checks that releasing leaves a file system
// the caller built and handed over open: it is the caller's file system, so
// releasing reaches only what is mounted below it, and only closing lets go of
// it.
func TestReleaseKeepsMountTheCallerBuilt(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	handed := &closeCounter{WriteFS: mustBaseFS(t, "memory://handed")}
	fsys, err := NewFSBuilder("memory://").
		MountFS("handed", handed).
		Build(ctx)
	if err != nil {
		t.Fatalf("Build() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	nFS, ok := fsys.(*nestFS)
	if !ok {
		t.Fatalf("Build() = %T, want *nestFS", fsys)
	}

	if err := fsys.Release(); err != nil {
		t.Fatalf("Release() = %v, want nil", err)
	}

	if nfs := nFS.mounts.getMount("handed"); nfs == nil {
		t.Error(`getMount("handed") = nil after Release, want the handed-over mount kept`)
	}
	if got := handed.closed(); got != 0 {
		t.Errorf("the handed-over file system closed %d times, want 0", got)
	}

	// The kept mount still serves its paths, which is what the caller expects
	// of a file system it built itself.
	writeTestFile(t, fsys, "handed/keep.txt", []byte("keep"))
	assertFileContent(t, fsys, "handed/keep.txt", "keep")
}

// TestMountMapReleaseAll checks that releasing drops the mounts ufs opened for
// itself, including those nested below them, and leaves a mount the caller
// handed over mounted with its own file system still open.
func TestMountMapReleaseAll(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mm := makeMountMap("test")
	defer ufsTesting.ValidateClose(t, mm)()

	// An archive mount as mountArchive makes one: opened by ufs, so releasing
	// closes it.
	archiveBase := &closeCounter{WriteFS: mustBaseFS(t, path.Join(testAssetsArchivesDir, "single-testassets.zip"))}
	archiveMount := makeNestFS(ctx, archiveBase)
	archiveMount.autoMounted = true
	ufsTesting.Must(t, mm.put("outer.zip.d", archiveMount))

	// An archive mounted inside an archive mount, as mountArchive does for an
	// archive held by an archive.
	nestedArchiveBase := &closeCounter{WriteFS: mustBaseFS(t, path.Join(testAssetsArchivesDir, "testassets.7z"))}
	ufsTesting.Must(t, archiveMount.addAutoMount("nested.7z.d", makeNestFS(ctx, nestedArchiveBase)))

	plainBase := &closeCounter{WriteFS: mustBaseFS(t, "memory://")}
	keptMount := makeNestFS(ctx, plainBase)
	ufsTesting.Must(t, mm.put("data", keptMount))

	innerArchiveBase := &closeCounter{WriteFS: mustBaseFS(t, path.Join(testAssetsArchivesDir, "single-testassets.zip"))}
	ufsTesting.Must(t, keptMount.addAutoMount("inner.zip.d", makeNestFS(ctx, innerArchiveBase)))

	if err := mm.releaseAll(); err != nil {
		t.Fatalf("releaseAll() = %v, want nil", err)
	}

	for _, tt := range []struct {
		what    string
		counter *closeCounter
		want    int
	}{
		{"archive mount", archiveBase, 1},
		{"nested archive", nestedArchiveBase, 1},
		{"archive below the kept mount", innerArchiveBase, 1},
		{"mount the caller handed over", plainBase, 0},
	} {
		if got := tt.counter.closed(); got != tt.want {
			t.Errorf("%s base closed %d times, want %d", tt.what, got, tt.want)
		}
	}

	if nfs := mm.getMount("outer.zip.d"); nfs != nil {
		t.Errorf("getMount(\"outer.zip.d\") = %s, want the archive mount dropped", nfs)
	}
	if nfs := mm.getMount("data"); nfs == nil {
		t.Error("getMount(\"data\") = nil, want the mount the caller handed over to stay mounted")
	}
	if nfs := keptMount.mounts.getMount("inner.zip.d"); nfs != nil {
		t.Errorf("getMount(\"inner.zip.d\") = %s, want the archive mount below the kept mount dropped", nfs)
	}
}

// TestMountMapReleaseAllKeepsMountThatFailsToClose checks that a mount that
// cannot be closed stays in the map, so that a later close can try again, and
// the mounts that could be released are released anyway.
func TestMountMapReleaseAllKeepsMountThatFailsToClose(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mm := makeMountMap("test")

	bad := &failCloser{WriteFS: mustBaseFS(t, path.Join(testAssetsArchivesDir, "single-testassets.zip"))}
	badMount := makeNestFS(ctx, bad)
	badMount.autoMounted = true
	ufsTesting.Must(t, mm.put("outer.zip.d", badMount))

	good := &closeCounter{WriteFS: mustBaseFS(t, path.Join(testAssetsArchivesDir, "testassets.7z"))}
	goodMount := makeNestFS(ctx, good)
	goodMount.autoMounted = true
	ufsTesting.Must(t, mm.put("nested.7z.d", goodMount))

	err := mm.releaseAll()
	if err == nil {
		t.Fatal("releaseAll() = nil, want the error from closing the mount")
	}
	if !strings.Contains(err.Error(), "close failed") {
		t.Errorf("releaseAll() = %v, want it to report the close error", err)
	}
	if nfs := mm.getMount("outer.zip.d"); nfs == nil {
		t.Error("getMount(\"outer.zip.d\") = nil, want the mount kept when it cannot be closed")
	}
	if bad.closed() != 1 {
		t.Errorf("Close() called %d times, want 1", bad.closed())
	}
	if nfs := mm.getMount("nested.7z.d"); nfs != nil {
		t.Errorf("getMount(\"nested.7z.d\") = %s, want the other mount released despite the failure", nfs)
	}
	if got := good.closed(); got != 1 {
		t.Errorf("the other mount base closed %d times, want 1", got)
	}
}

func TestMountMapRemove(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	mm := makeMountMap("test")

	base := &closeCounter{WriteFS: mustBaseFS(t, "memory://")}
	ufsTesting.Must(t, mm.put("data", makeNestFS(ctx, base)))

	if err := mm.remove("data"); err != nil {
		t.Errorf("remove(\"data\") = %v, want nil", err)
	}
	if nfs := mm.getMount("data"); nfs != nil {
		t.Errorf("getMount(\"data\") = %s, want the mount dropped", nfs)
	}
	if base.closed() != 1 {
		t.Errorf("base closed %d times, want 1", base.closed())
	}

	if err := mm.remove("missing"); err != nil {
		t.Errorf("remove(\"missing\") = %v, want nil for a name that is not mounted", err)
	}

	bad := &failCloser{WriteFS: mustBaseFS(t, "memory://bad")}
	ufsTesting.Must(t, mm.put("bad", makeNestFS(ctx, bad)))
	if err := mm.remove("bad"); err == nil {
		t.Error("remove(\"bad\") = nil, want the error from closing the mount")
	}
	if nfs := mm.getMount("bad"); nfs == nil {
		t.Error("getMount(\"bad\") = nil, want the mount kept when it cannot be closed")
	}
}

// TestReleaseDoesNotCloseBase checks that releasing leaves the file system
// itself open and usable, releasing only what its mounts hold on to.
func TestReleaseDoesNotCloseBase(t *testing.T) {
	t.Parallel()
	ctx := t.Context()

	base := &closeCounter{WriteFS: mustBaseFS(t, "memory://")}
	fsys := makeNestFS(ctx, base)
	ufsTesting.Must(t, fsys.addMount("data", makeNestFS(ctx, mustBaseFS(t, "memory://data"))))

	if err := fsys.Release(); err != nil {
		t.Fatalf("Release() = %v, want nil", err)
	}
	if base.closed() != 0 {
		t.Errorf("base closed %d times, want 0", base.closed())
	}

	writeTestFile(t, fsys, "keep.txt", []byte("keep"))
	assertFileContent(t, fsys, "keep.txt", "keep")
}

// TestReleaseRemountsArchiveContentDirs checks that releasing an archive does
// not lose it: the content directory of an archive, and of the archives inside
// it, is mounted again from the same archive file the next time a path under it
// is used.
func TestReleaseRemountsArchiveContentDirs(t *testing.T) {
	t.Parallel()
	fsys := mustNestFS(t, pathutil.CwdPath)
	defer ufsTesting.ValidateClose(t, fsys)()

	tests := []struct {
		name string
		want string
	}{
		{
			name: "testing/testassets/archives/nested-testassets.zip.d/index.html",
			want: "testing/testassets/files/index.html",
		},
		{
			name: "testing/testassets/archives/nested-testassets.zip.d/single-testassets.zip.d/index.html",
			want: "testing/testassets/files/index.html",
		},
		{
			name: "testing/testassets/archives/nested-testassets.zip.d/testassets.7z.d/assets/four/4.txt",
			want: "testing/testassets/files/assets/four/4.txt",
		},
	}

	for _, tt := range tests {
		assertFileContent(t, fsys, tt.name, tt.want)
	}

	if err := fsys.Release(); err != nil {
		t.Fatalf("Release() = %v, want nil", err)
	}

	for _, tt := range tests {
		assertFileContent(t, fsys, tt.name, tt.want)
	}
}

// closeOrder records the order in which the file systems that report to it are
// closed.
type closeOrder struct {
	mu    sync.Mutex
	names []string
}

func (o *closeOrder) record(name string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.names = append(o.names, name)
}

func (o *closeOrder) list() []string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return slices.Clone(o.names)
}

// closeRecorder reports to a closeOrder and closes the file system it wraps.
type closeRecorder struct {
	types.WriteFS
	name  string
	order *closeOrder
}

func (fsys *closeRecorder) Close() error {
	fsys.order.record(fsys.name)
	return fsys.WriteFS.Close()
}

// TestReleaseClosesArchivesInsideAnArchiveFirst checks the order that releasing
// an archive mount closes the file systems in: an archive inside an archive is
// closed before the archive that holds it, so that nothing holds on to a file
// when the file system that holds the file lets go of it. That is what lets a
// temporary directory holding an archive be removed while the archive is
// mounted from it.
func TestReleaseClosesArchivesInsideAnArchiveFirst(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	order := &closeOrder{}

	outer := &closeRecorder{
		WriteFS: mustBaseFS(t, path.Join(testAssetsArchivesDir, "nested-testassets.zip")),
		name:    "outer",
		order:   order,
	}
	mm := makeMountMap("test")
	outerMount := makeNestFS(ctx, outer)
	outerMount.autoMounted = true
	ufsTesting.Must(t, mm.put("nested-testassets.zip.d", outerMount))

	// The archives inside the outer one, mounted as mountArchive mounts them.
	innerNames := []string{"single-testassets.zip.d", "testassets.7z.d"}
	for _, name := range innerNames {
		inner := &closeRecorder{
			WriteFS: mustBaseFS(t, path.Join(testAssetsArchivesDir, strings.TrimSuffix(name, archiveDirExt))),
			name:    name,
			order:   order,
		}
		ufsTesting.Must(t, outerMount.addAutoMount(name, makeNestFS(ctx, inner)))
	}

	if err := mm.releaseAll(); err != nil {
		t.Fatalf("releaseAll() = %v, want nil", err)
	}

	names := order.list()
	outerAt := slices.Index(names, "outer")
	if outerAt < 0 {
		t.Fatalf("releaseAll() closed %q, want the outer archive closed too", names)
	}
	for _, name := range innerNames {
		at := slices.Index(names, name)
		if at < 0 {
			t.Errorf("releaseAll() closed %q, want %q closed as well", names, name)
			continue
		}
		if at > outerAt {
			t.Errorf("releaseAll() closed %q, want %q closed before %q", names, name, "outer")
		}
	}
}
