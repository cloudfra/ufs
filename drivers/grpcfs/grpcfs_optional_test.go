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

package grpcfs_test

import (
	"context"
	"errors"
	"io"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
	"github.com/cloudfra/ufs/drivers/grpcfs"
	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
)

// clientOver returns a client of a server standing on inner.
func clientOver(tb testing.TB, inner types.WriteFS) ufs.FS {
	tb.Helper()
	fsys, err := grpcfs.NewWithClient(tb.Context(), serve(tb, inner), servedClientURI())
	if err != nil {
		tb.Fatalf("NewWithClient: %v", err)
	}
	tb.Cleanup(func() { mustClose(tb, fsys) })
	return fsys.(ufs.FS)
}

// localServed returns a localFS on a temporary directory, which carries the
// optional interfaces memFS does not (Chmod, Link), so the rpcs reach a native
// method rather than an emulation.
func localServed(tb testing.TB) types.WriteFS {
	tb.Helper()
	// localFS itself rather than ufs.New, whose nestFS passes on only some of
	// the optional interfaces, so the rpcs reach localFS's own methods.
	fsys, err := core.MakeLocalFS((&url.URL{Scheme: "file", Path: filepath.ToSlash(tb.TempDir())}).String()) //nolint:staticcheck // see above
	if err != nil {
		tb.Fatalf("MakeLocalFS: %v", err)
	}
	tb.Cleanup(func() { mustClose(tb, fsys) })
	return fsys
}

func mustWrite(tb testing.TB, fsys types.WriteFS, name, content string) {
	tb.Helper()
	if err := polyfills.WriteFile(fsys, name, []byte(content), 0o644); err != nil {
		tb.Fatalf("WriteFile(%q): %v", name, err)
	}
}

func mustRead(tb testing.TB, fsys types.ReadFS, name string) string {
	tb.Helper()
	data, err := fsys.ReadFile(name)
	if err != nil {
		tb.Fatalf("ReadFile(%q): %v", name, err)
	}
	return string(data)
}

// TestClientOptionalOverNativeMethods drives each optional method the client
// carries against a served file system that implements it, and checks the
// effect through the served file system itself.
func TestClientOptionalOverNativeMethods(t *testing.T) {
	t.Parallel()
	inner := localServed(t)
	client := clientOver(t, inner)
	mustWrite(t, client, "a.txt", "alpha")

	if err := client.(types.MkdirFS).Mkdir("dir", 0o755); err != nil {
		t.Fatalf("Mkdir(dir) = %v, want nil", err)
	}
	if info, err := inner.Stat("dir"); err != nil || !info.IsDir() {
		t.Errorf("Stat(dir) on the server = %v, %v, want a directory", info, err)
	}
	if err := client.(types.MkdirFS).Mkdir("none/dir", 0o755); err == nil {
		t.Error("Mkdir(none/dir) with no parent = nil, want an error")
	}

	if runtime.GOOS != "windows" {
		if err := client.(types.ChmodFS).Chmod("a.txt", 0o600); err != nil {
			t.Fatalf("Chmod(a.txt, 0600) = %v, want nil", err)
		}
		if info, err := inner.Stat("a.txt"); err != nil || info.Mode().Perm() != 0o600 {
			t.Errorf("mode of a.txt on the server = %v, %v, want 0600", info.Mode().Perm(), err)
		}
	}

	mtime := time.Date(2020, 1, 2, 3, 4, 5, 0, time.UTC)
	if err := client.(types.ChtimesFS).Chtimes("a.txt", time.Time{}, mtime); err != nil {
		t.Fatalf("Chtimes(a.txt) = %v, want nil", err)
	}
	if info, err := inner.Stat("a.txt"); err != nil || !info.ModTime().Equal(mtime) {
		t.Errorf("mtime of a.txt on the server = %v, %v, want %v", info.ModTime(), err, mtime)
	}

	if err := client.(types.LinkFS).Link("a.txt", "dir/hard.txt"); err != nil {
		t.Fatalf("Link(a.txt, dir/hard.txt) = %v, want nil", err)
	}
	if got := mustRead(t, inner, "dir/hard.txt"); got != "alpha" {
		t.Errorf("dir/hard.txt on the server = %q, want %q", got, "alpha")
	}

	if err := client.(types.CopyFileFS).CopyFile("a.txt", "dir/copy.txt"); err != nil {
		t.Fatalf("CopyFile(a.txt, dir/copy.txt) = %v, want nil", err)
	}
	if got := mustRead(t, inner, "dir/copy.txt"); got != "alpha" {
		t.Errorf("dir/copy.txt on the server = %q, want %q", got, "alpha")
	}

	if err := client.Rename("dir/copy.txt", "moved.txt"); err != nil {
		t.Fatalf("Rename(dir/copy.txt, moved.txt) = %v, want nil", err)
	}
	if _, err := inner.Stat("dir/copy.txt"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Stat(dir/copy.txt) after the rename = %v, want fs.ErrNotExist", err)
	}
	var linkErr *os.LinkError
	if err := client.Rename("missing", "elsewhere"); !errors.As(err, &linkErr) || !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Rename(missing, elsewhere) = %v, want an *os.LinkError wrapping fs.ErrNotExist", err)
	}

	parent, isDir, err := client.(types.ClosestParentFS).ClosestParent("dir/x/y/z")
	if err != nil || parent != "dir" || !isDir {
		t.Errorf("ClosestParent(dir/x/y/z) = %q, %v, %v, want dir, true, nil", parent, isDir, err)
	}
}

// TestClientOptionalUnsupported checks that a method the served file system
// cannot perform at all comes back as errors.ErrUnsupported in a
// *fs.PathError, which is how a caller tells "not here" from "failed".
func TestClientOptionalUnsupported(t *testing.T) {
	t.Parallel()
	client := clientOver(t, servedFS(t))
	mustWrite(t, client, "a.txt", "alpha")

	for name, err := range map[string]error{
		"Symlink": client.(types.SymlinkFS).Symlink("a.txt", "link"),
		"Chmod":   client.(types.ChmodFS).Chmod("a.txt", 0o600),
		"Chown":   client.(types.ChownFS).Chown("a.txt", 0, 0),
		"Lchown":  client.(types.LchownFS).Lchown("a.txt", 0, 0),
	} {
		var pathErr *fs.PathError
		if !errors.Is(err, errors.ErrUnsupported) || !errors.As(err, &pathErr) {
			t.Errorf("%s on memFS = %v, want a *fs.PathError wrapping errors.ErrUnsupported", name, err)
		}
	}
}

// noContentClient fails every rpc that would carry file content, so a test
// can show that an operation moved none over the wire.
type noContentClient struct {
	proto.FSClient
}

func (noContentClient) ReadFile(context.Context, *proto.ReadFileRequest, ...grpc.CallOption) (proto.FS_ReadFileClient, error) {
	return nil, errors.New("content was asked for over the wire")
}

func (noContentClient) WriteFile(context.Context, ...grpc.CallOption) (proto.FS_WriteFileClient, error) {
	return nil, errors.New("content was sent over the wire")
}

// TestClientCopyAndRenameStayOnTheServer copies and renames on a served file
// system with neither method of its own, through a client that cannot move
// content, so the emulations must run on the server.
func TestClientCopyAndRenameStayOnTheServer(t *testing.T) {
	t.Parallel()
	inner := servedFS(t)
	mustWrite(t, inner, "a.txt", "alpha")
	fsys, err := grpcfs.NewWithClient(t.Context(), noContentClient{serve(t, inner)}, servedClientURI())
	if err != nil {
		t.Fatalf("NewWithClient: %v", err)
	}
	t.Cleanup(func() { mustClose(t, fsys) })

	if err := fsys.(types.CopyFileFS).CopyFile("a.txt", "b.txt"); err != nil {
		t.Fatalf("CopyFile(a.txt, b.txt) = %v, want nil", err)
	}
	if err := fsys.(types.RenameFS).Rename("b.txt", "c.txt"); err != nil {
		t.Fatalf("Rename(b.txt, c.txt) = %v, want nil", err)
	}
	if got := mustRead(t, inner, "c.txt"); got != "alpha" {
		t.Errorf("c.txt on the server = %q, want %q", got, "alpha")
	}
}

// TestClientOpenFile checks the generalized open the client builds from Open
// and Create: a create-and-truncate writes, a read-only open reads, an
// exclusive create of an existing file is refused, and a write that would keep
// the content is unsupported rather than silently truncating.
func TestClientOpenFile(t *testing.T) {
	t.Parallel()
	client := clientOver(t, servedFS(t))

	f, err := client.OpenFile("new.txt", os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatalf("OpenFile(new.txt, create) = %v, want nil", err)
	}
	if _, err := f.Write([]byte("hello")); err != nil {
		t.Fatalf("Write = %v, want nil", err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close = %v, want nil", err)
	}
	if got := mustRead(t, client, "new.txt"); got != "hello" {
		t.Errorf("new.txt = %q, want hello", got)
	}

	r, err := client.OpenFile("new.txt", os.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("OpenFile(new.txt, read) = %v, want nil", err)
	}
	data, err := io.ReadAll(r)
	if err != nil || string(data) != "hello" {
		t.Errorf("read through OpenFile = %q, %v, want hello", data, err)
	}
	mustClose(t, r)

	if _, err := client.OpenFile("new.txt", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644); !errors.Is(err, fs.ErrExist) {
		t.Errorf("OpenFile(new.txt, excl) = %v, want fs.ErrExist", err)
	}
	if _, err := client.OpenFile("new.txt", os.O_WRONLY, 0); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("OpenFile(new.txt, write without truncate) = %v, want errors.ErrUnsupported", err)
	}
}

// TestClientWatch watches the served tree through the client and checks that
// changes made on the server arrive, that a burst arrives whole, and that
// closing the watch stops the hook.
func TestClientWatch(t *testing.T) {
	t.Parallel()
	inner := servedFS(t)
	client := clientOver(t, inner)

	events := make(chan string, 1024)
	closer, err := client.(types.Watcher).Watch(t.Context(), ".", func(_ types.NotifyOp, name string) {
		events <- name
	})
	if err != nil {
		t.Fatalf("Watch(.) = %v, want nil", err)
	}
	// memFS's own change bus buffers 256 events per watcher and drops the rest
	// while the watcher is busy, so the burst stays inside that: what is under
	// test is the stream carrying every change it is handed.
	const burst = 50
	for i := range burst {
		mustWrite(t, inner, "f"+strconv.Itoa(i), "x")
	}
	seen := map[string]bool{}
	deadline := time.After(10 * time.Second)
	for len(seen) < burst {
		select {
		case name := <-events:
			seen[name] = true
		case <-deadline:
			t.Fatalf("saw %d of %d changes", len(seen), burst)
		}
	}
	if err := closer.Close(); err != nil {
		t.Errorf("Close = %v, want nil", err)
	}
	mustWrite(t, inner, "after.txt", "x")
	select {
	case name := <-events:
		if name == "after.txt" {
			t.Error("the hook was called after Close returned")
		}
	default:
	}

	if _, err := client.(types.Watcher).Watch(t.Context(), "missing", func(types.NotifyOp, string) {}); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("Watch(missing) = %v, want fs.ErrNotExist", err)
	}
}

// notWatching hides memFS's Watch.
type notWatching struct{ types.WriteFS }

// TestClientWatchUnsupported checks that a served file system that cannot
// watch is reported as such.
func TestClientWatchUnsupported(t *testing.T) {
	t.Parallel()
	client := clientOver(t, notWatching{servedFS(t)})
	if _, err := client.(types.Watcher).Watch(t.Context(), ".", func(types.NotifyOp, string) {}); !errors.Is(err, errors.ErrUnsupported) {
		t.Errorf("Watch over a file system that cannot watch = %v, want errors.ErrUnsupported", err)
	}
}

// TestServerShutdownEndsWatch checks that Shutdown ends a watch stream rather
// than waiting for its client, which would otherwise keep Shutdown, and a
// grpc.Server's GracefulStop, waiting forever.
func TestServerShutdownEndsWatch(t *testing.T) {
	t.Parallel()
	server := grpcfs.NewServer(servedFS(t))
	stream := &watchStream{ctx: t.Context(), sent: make(chan *proto.WatchResponse, 1)}
	done := make(chan error, 1)
	go func() { done <- server.Watch(&proto.WatchRequest{Name: "."}, stream) }()
	<-stream.sent // the watch is in place

	shut := make(chan struct{})
	go func() {
		server.Shutdown()
		close(shut)
	}()
	select {
	case <-shut:
	case <-time.After(10 * time.Second):
		t.Fatal("Shutdown did not return while a watch was open")
	}
	wantCode(t, <-done, codes.Unavailable)
}

// watchStream is a proto.FS_WatchServer that records what the handler sends.
type watchStream struct {
	grpc.ServerStream
	ctx  context.Context
	sent chan *proto.WatchResponse
}

func (s *watchStream) Context() context.Context { return s.ctx }

func (s *watchStream) Send(resp *proto.WatchResponse) error {
	select {
	case s.sent <- resp:
	default:
	}
	return nil
}
