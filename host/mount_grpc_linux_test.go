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

//go:build linux

package host

import (
	"errors"
	"fmt"
	"io/fs"
	"net"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/grpcfs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/proto"
)

// mountServedOverGrpc mounts a file system that is not reached directly: the file
// system fsys holds is served over the FS service on a loopback socket, the grpc://
// driver builds a file system for that address, and that one is mounted on the host
// at a temporary directory. It returns the directory and the driver's file system,
// which is what a caller holding the served address reaches.
//
// The chain is the one ufsmount builds from both ends of -mount: the memory file
// system and the socket are what -uri and -mount grpc://<address> bring up, and the
// driver is what a caller holding that address reaches. Only the driver is mounted
// here, which is what the mount of a remote file system means: a read or a write
// under the returned directory travels through a socket, an rpc and a driver before
// it reaches the file system that stores it.
//
// The cleanups are registered in the order that makes the shutdown order the
// serving side needs: unmount first, then the driver, then the server, and the
// served file system last — which the caller registered before this function ran.
// A ReadFile or ListFiles handler holds the handle it opened on the served file
// system until it unwinds, so GracefulStop has to return before that close.
func mountServedOverGrpc(t *testing.T, fsys types.WriteFS) (mountDir string, remote types.WriteFS) {
	t.Helper()
	ctx := t.Context()

	var lc net.ListenConfig
	lis, err := lc.Listen(ctx, "tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}

	// The options are not decoration: the handlers assume the message ceiling they
	// set, and grpc's own default is smaller than the single-frame upload the
	// WriteFile rpc documents.
	server := grpc.NewServer(grpcfs.ServerOptions()...)
	proto.RegisterFSServer(server, grpcfs.NewServer(fsys))
	served := make(chan error, 1)
	go func() {
		served <- server.Serve(lis)
	}()
	t.Cleanup(func() {
		server.GracefulStop()
		if err := <-served; err != nil && !errors.Is(err, grpc.ErrServerStopped) {
			t.Errorf("serving %q: %v", lis.Addr(), err)
		}
	})

	remote, err = grpcfs.New(ctx, "grpc://"+lis.Addr().String())
	if err != nil {
		t.Fatalf("grpcfs.New of the served address %q: %v", lis.Addr(), err)
	}
	t.Cleanup(validateClose(t, remote))

	return testMount(t, remote), remote
}

// mountSettleTimeout is how long a check waits for a file under a mount to be what
// the write that changed it meant. A mount is not a synchronous view of the file
// system behind it, in both directions:
//
//   - A write through the mount of a driver that delivers at Close, as
//     drivers/grpcfs does, is not finished when close(2) returns. The kernel sends
//     the release when it drops the last descriptor and does not wait for the
//     answer, so the upload the release ends lands a moment later.
//   - A change made on the other side of the mount reaches the mounted directory
//     only once the attributes the kernel kept for fuseAttrTimeout have expired, and
//     the content of a file whose size did not change only once it has noticed that
//     the file is a different one.
const mountSettleTimeout = 10 * time.Second

// poll calls watch until it answers with want, or until mountSettleTimeout has
// passed. The last answer is returned as an error, since that is all a caller that
// is only checking needs, and the answer of a read that went through says what the
// file held.
func poll(tb testing.TB, watch func() (string, error), want string) error {
	tb.Helper()
	deadline := time.Now().Add(mountSettleTimeout)
	for {
		got, err := watch()
		if err == nil {
			if got == want {
				return nil
			}
			err = fmt.Errorf("holds %q, want %q", got, want)
		}
		if time.Now().After(deadline) {
			return err
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// mountedContent reads name from the mounted directory, as any local file is read.
func mountedContent(mountDir, name string) (string, error) {
	content, err := osutil.ReadFile(filepath.Join(mountDir, name))
	return string(content), err
}

// servedContent reads name from the file system the socket serves, with no driver,
// socket or mount in between: the other end of the chain.
func servedContent(fsys fs.FS, name string) (string, error) {
	content, err := fs.ReadFile(fsys, name)
	return string(content), err
}

// waitMounted waits for name to hold want in the mounted directory.
func waitMounted(tb testing.TB, mountDir, name, want string) error {
	tb.Helper()
	return poll(tb, func() (string, error) { return mountedContent(mountDir, name) }, want)
}

// waitServed waits for name to hold want in the file system the socket serves.
func waitServed(tb testing.TB, fsys fs.FS, name, want string) error {
	tb.Helper()
	return poll(tb, func() (string, error) { return servedContent(fsys, name) }, want)
}

// wantAtBothEnds checks that name holds want in the mounted directory and in the
// file system the socket serves, which is the whole point of the chain: a change at
// one end is the same change at the other.
func wantAtBothEnds(t *testing.T, fsys fs.FS, mountDir, name, want string) {
	t.Helper()
	if err := waitMounted(t, mountDir, name, want); err != nil {
		t.Errorf("%q through the mount: %v", name, err)
	}
	if err := waitServed(t, fsys, name, want); err != nil {
		t.Errorf("%q in the served file system: %v", name, err)
	}
}

// mountedNames lists the mounted directory as one string of sorted names, so that a
// listing can be polled the same way content can.
func mountedNames(mountDir string) (string, error) {
	entries, err := osutil.ReadDir(mountDir)
	if err != nil {
		return "", err
	}
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	slices.Sort(names)
	return strings.Join(names, "\n"), nil
}

// TestMountGrpcWriteReadOverwrite drives the chain from a memory file system served
// over a socket, through the grpc:// driver and the mount, down to the local
// directory it ends in: content seeded on the served side is read there, files are
// written there and found on the served side, and names are overwritten in both
// directions of size — the shorter rewrite being the one that matters, because a
// size kept from before the rewrite shows up there and nowhere else.
//
// Which end each change is made from is decided by what the chain can do, not by
// what a local directory can:
//
//   - A name that is not there yet is written in the mounted directory, which is
//     what mounting a remote file system is for.
//   - A name that is already there is rewritten through the driver, because the same
//     write in the mounted directory cannot do it. For an existing name the kernel
//     carries the truncate itself, as a SETATTR the FUSE adapter does not answer (see
//     the list at the top of fuse_linux.go), so the adapter is only ever asked for a
//     write-open that keeps what is there — which a driver that uploads whole files
//     cannot carry out (github.com/cloudfra/ufs/issues/218).
func TestMountGrpcWriteReadOverwrite(t *testing.T) {
	t.Parallel()
	requireFUSE(t)

	fsys, err := ufs.New(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	// Seeded before the mount is up, so that the first reads below travel from the
	// memory file system through the service, the driver and the mount.
	seeded := map[string]string{
		"seeded.txt":      "written in the memory file system",
		"nested/deep.txt": "written in a directory of the memory file system",
	}
	if err := fsys.MkdirAll("nested", fs.ModePerm); err != nil {
		t.Fatal(err)
	}
	for name, content := range seeded {
		if err := writeFile(fsys, name, content); err != nil {
			t.Fatal(err)
		}
	}

	mountDir, remote := mountServedOverGrpc(t, fsys)

	for name, want := range seeded {
		got, err := mountedContent(mountDir, name)
		if err != nil || got != want {
			t.Errorf("%q read through the mount = %q: %v, want %q", name, got, err, want)
		}
	}

	// A new name, written the way any file in a local directory is written. The
	// driver ends the upload when the kernel releases the handle, so both ends are
	// waited for rather than read once.
	const localName = "local.txt"
	const first = "first version"
	if err := osutil.WriteFile(filepath.Join(mountDir, localName), []byte(first)); err != nil {
		t.Fatalf("WriteFile(%q) through the mount: %v", localName, err)
	}
	wantAtBothEnds(t, fsys, mountDir, localName, first)

	// Overwrites in both directions of size, checked at both ends every time. The
	// shorter one is what a size carried over from the previous version would spoil.
	overwrites := []string{
		"a second version long enough to change the size of the file",
		"tiny",
	}
	for _, want := range overwrites {
		if err := writeFile(remote, localName, want); err != nil {
			t.Fatalf("writeFile(%q): %v", localName, err)
		}
		wantAtBothEnds(t, fsys, mountDir, localName, want)
	}

	// The same rewrite of a name the mount did not create, which the mount only
	// ever knew from a lookup.
	const replaced = "replaced through the driver"
	if err := writeFile(remote, "seeded.txt", replaced); err != nil {
		t.Fatalf("writeFile(%q): %v", "seeded.txt", err)
	}
	wantAtBothEnds(t, fsys, mountDir, "seeded.txt", replaced)

	// A new name inside a directory that was seeded on the served side, written from
	// the mounted directory.
	const inside = "nested/inside.txt"
	const insideContent = "written through the mount into a served directory"
	if err := osutil.WriteFile(filepath.Join(mountDir, inside), []byte(insideContent)); err != nil {
		t.Fatalf("WriteFile(%q) through the mount: %v", inside, err)
	}
	wantAtBothEnds(t, fsys, mountDir, inside, insideContent)

	// The mounted directory lists what the two sides put there.
	wantListing := strings.Join([]string{"local.txt", "nested", "seeded.txt"}, "\n")
	if err := poll(t, func() (string, error) { return mountedNames(mountDir) }, wantListing); err != nil {
		t.Errorf("the mounted directory: %v", err)
	}
}

// writerContent is what writer w writes in round round. Every round writes more than
// the one before, so a rewrite that left the tail of the previous version behind
// cannot pass.
func writerContent(w, round int) string {
	return strings.Repeat(fmt.Sprintf("writer %d round %d; ", w, round), round+1)
}

// sharedVersions is the content the writers of one shared name write at the same
// time. All four hold the same number of bytes on purpose: a mount keeps the size it
// read for fuseAttrTimeout, so versions of different lengths could be read back
// short for reasons that have nothing to do with the writers. They differ in what
// they say, which is enough for a read of a spliced file to match none of them.
var sharedVersions = func() []string {
	versions := make([]string, 4)
	for i := range versions {
		versions[i] = strings.Repeat(fmt.Sprintf("shared writer %d; ", i), 40)
	}
	return versions
}()

// TestMountGrpcConcurrentFileIO runs the chain from several goroutines at once:
// writers each creating a name of their own in the mounted directory and rewriting
// it through the driver, readers of a file nothing writes to, and four writers
// rewriting one name that they share while it is read at the same time. Afterwards
// every name holds exactly what it should at both ends, and the race detector — which
// the coverage and deflake runs turn on — sees nothing in the mount, the driver or
// the service.
//
// The four writers of one name write four different versions, and what is checked is
// that the name ends up holding one of them entire. A rewrite through this chain is
// one whole file replacing another, so the winner is not defined but a splice is a
// bug: bytes of two versions in one file, or a length that no version ever had, could
// only have come from file IO that the chain does not serialize.
func TestMountGrpcConcurrentFileIO(t *testing.T) {
	t.Parallel()
	requireFUSE(t)

	fsys, err := ufs.New(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(validateClose(t, fsys))

	mountDir, remote := mountServedOverGrpc(t, fsys)

	const (
		writers = 8
		rounds  = 3
	)
	const (
		stableName, stableContent = "stable.txt", "read while other files are being written"
		sharedName                = "shared.txt"
	)

	// Seeded before the goroutines start, so that a reader always finds a name that
	// is there, holding one of the versions.
	if err := writeFile(remote, stableName, stableContent); err != nil {
		t.Fatal(err)
	}
	if err := writeFile(remote, sharedName, sharedVersions[0]); err != nil {
		t.Fatal(err)
	}

	// What every name has to hold in the end, worked out here rather than recorded
	// by the writers, so the map is only ever read once the goroutines start. The
	// shared name is not in it: which of its versions wins is not defined.
	final := make(map[string]string, writers+1)
	names := make([]string, 0, writers+1)
	names = append(names, stableName)
	final[stableName] = stableContent
	for w := range writers {
		name := fmt.Sprintf("writer-%d.txt", w)
		names = append(names, name)
		final[name] = writerContent(w, rounds-1)
	}

	// One writer per name: the first version comes from an ordinary write in the
	// mounted directory, the rest from the driver, since a name the mount already
	// knows cannot be rewritten through it.
	var writing sync.WaitGroup
	for w := range writers {
		name := fmt.Sprintf("writer-%d.txt", w)
		writing.Go(func() {
			if err := osutil.WriteFile(filepath.Join(mountDir, name), []byte(writerContent(w, 0))); err != nil {
				t.Errorf("WriteFile(%q) through the mount: %v", name, err)
				return
			}
			// The version written through the mount is not in the served file
			// system yet: this driver delivers at release, and the kernel only
			// sends the release once it has dropped the last descriptor, so the
			// upload that finishes the write lands after close(2) returns. The
			// driver writes below deliver synchronously, so a round written before
			// that upload has arrived can be replaced by the round it should have
			// overwritten — which is what the final check below catches when it
			// finds the served file holding the first version.
			if err := waitServed(t, fsys, name, writerContent(w, 0)); err != nil {
				t.Errorf("%q in the served file system after the write through the mount: %v", name, err)
				return
			}
			for round := 1; round < rounds; round++ {
				if err := writeFile(remote, name, writerContent(w, round)); err != nil {
					t.Errorf("writeFile(%q) round %d: %v", name, round, err)
					return
				}
			}
			if err := waitServed(t, fsys, name, final[name]); err != nil {
				t.Errorf("%q in the served file system: %v", name, err)
			}
			if err := waitMounted(t, mountDir, name, final[name]); err != nil {
				t.Errorf("%q through the mount: %v", name, err)
			}
		})
	}

	// Readers of a name nothing writes to, running while all that is going on, so
	// that reads and writes share the mount, the driver and the socket at the same
	// time.
	var reading sync.WaitGroup
	for range 4 {
		reading.Go(func() {
			for range 20 {
				content, err := mountedContent(mountDir, stableName)
				if err != nil || content != stableContent {
					t.Errorf("%q read while other files were written = %q: %v, want %q", stableName, content, err, stableContent)
					return
				}
			}
		})
	}

	// Four writers on one name at the same time, and readers of it while they are.
	var rewriting sync.WaitGroup
	for i := range sharedVersions {
		rewriting.Go(func() {
			for range 3 {
				if err := writeFile(remote, sharedName, sharedVersions[i]); err != nil {
					t.Errorf("writeFile(%q) as version %d: %v", sharedName, i, err)
					return
				}
			}
		})
	}
	var sharedReads sync.WaitGroup
	for range 2 {
		sharedReads.Go(func() {
			for range 30 {
				content, err := mountedContent(mountDir, sharedName)
				if err != nil {
					t.Errorf("%q read through the mount while it was rewritten: %v", sharedName, err)
					return
				}
				if !slices.Contains(sharedVersions, content) {
					t.Errorf("%q read through the mount while it was rewritten = %q, want one of the %d versions written to it", sharedName, content, len(sharedVersions))
					return
				}
			}
		})
	}

	writing.Wait()
	reading.Wait()
	rewriting.Wait()
	sharedReads.Wait()

	// Every name read back through the mount, all at once.
	var checking sync.WaitGroup
	for _, name := range names {
		checking.Go(func() {
			if err := waitMounted(t, mountDir, name, final[name]); err != nil {
				t.Errorf("%q through the mount: %v", name, err)
			}
		})
	}
	checking.Wait()

	for _, name := range names {
		if err := waitServed(t, fsys, name, final[name]); err != nil {
			t.Errorf("%q in the served file system: %v", name, err)
		}
	}

	// The name several writers rewrote at once ends up holding one version entire at
	// each end, whichever of them won.
	for _, read := range []struct {
		where   string
		content func() (string, error)
	}{
		{"through the mount", func() (string, error) { return mountedContent(mountDir, sharedName) }},
		{"in the served file system", func() (string, error) { return servedContent(fsys, sharedName) }},
	} {
		content, err := read.content()
		if err != nil {
			t.Errorf("%q read %s: %v", sharedName, read.where, err)
			continue
		}
		if !slices.Contains(sharedVersions, content) {
			t.Errorf("%q read %s = %q, want one of the %d versions written to it", sharedName, read.where, content, len(sharedVersions))
		}
	}

	// Nothing else was created along the way.
	wantNames := make([]string, 0, len(names)+1)
	wantNames = append(wantNames, names...)
	wantNames = append(wantNames, sharedName)
	slices.Sort(wantNames)
	if got, err := mountedNames(mountDir); err != nil {
		t.Fatal(err)
	} else if diff := cmp.Diff(strings.Join(wantNames, "\n"), got); diff != "" {
		t.Errorf("the mounted directory listed the wrong names diff(-want,+got):\n%s", diff)
	}
}
