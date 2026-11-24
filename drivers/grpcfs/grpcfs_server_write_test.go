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
	"io"
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
)

// diskFS returns a server's inner over a directory that goes away with the test.
func diskFS(tb testing.TB) types.WriteFS {
	tb.Helper()
	return diskFSIn(tb, tb.TempDir())
}

// diskFSIn returns a server's inner over a directory that already exists, for a
// test that reaches the same files through the operating system as well.
func diskFSIn(tb testing.TB, dir string) types.WriteFS {
	tb.Helper()
	// Not ufs.New: New answers with a nestFS layer over the driver, and what a
	// file on disk answers for each rpc is what these tests are about, not what a
	// wrapper of the driver adds to it.
	inner, err := core.MakeLocalFS(dir) //nolint:staticcheck // SA1019: the bare localFS is the inner under test, not a nestFS wrapper
	if err != nil {
		tb.Fatalf("MakeLocalFS: %v", err)
	}
	closer, ok := inner.(io.Closer)
	if !ok {
		tb.Fatalf("localFS %q does not implement io.Closer", dir)
	}
	// The root handle over dir is released by Close, and otherwise only when the
	// value goes unreachable, which Go arranges from a finalizer. A served inner
	// stays reachable as long as the server holding it does, and Go opens that
	// handle on Windows without FILE_SHARE_DELETE, so the directory cannot be
	// removed under it: leaving the release to the collector is what made
	// t.TempDir() report "unlinkat …: The process cannot access the file because
	// it is being used by another process" on the Windows jobs. This cleanup is
	// registered after the one t.TempDir adds, so it runs before it.
	tb.Cleanup(func() {
		if err := closer.Close(); err != nil {
			tb.Errorf("Close of the served localFS = %v", err)
		}
	})
	return inner
}

// TestServerWriteFile covers the client-streaming upload, where the name and the
// mode travel in the first frame and the content in every frame.
func TestServerWriteFile(t *testing.T) {
	ctx := t.Context()
	inner := diskFS(t)
	client := serve(t, inner)

	t.Run("the_frames_concatenate_into_one_file", func(t *testing.T) {
		stream, err := client.WriteFile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, frame := range []*proto.WriteFileRequest{
			{Name: "upload.txt", Mode: uint32(0o600), Data: []byte("first ")},
			{Data: []byte("second ")},
			{Data: []byte("third")},
		} {
			if err := stream.Send(frame); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := stream.CloseAndRecv(); err != nil {
			t.Fatal(err)
		}

		got, err := inner.ReadFile("upload.txt")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "first second third" {
			t.Errorf("content = %q, want the frames in the order they were sent", got)
		}
		// What the mode of the first frame does is asserted on unix only, in
		// TestServerWriteFileMode: Windows reports no unix permission bits, so the
		// file an upload makes there answers 0o777 whatever the frame carried.
	})

	t.Run("an_empty_stream_names_no_file", func(t *testing.T) {
		stream, err := client.WriteFile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = stream.CloseAndRecv()
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("a_missing_parent_reports_NotFound", func(t *testing.T) {
		_, err := writeWhole(ctx, client, "missing/child.txt", "child")
		wantCode(t, err, codes.NotFound)
	})

	t.Run("a_file_system_with_no_whole_file_write_of_its_own_is_refused", func(t *testing.T) {
		// The service asks for a whole-file write through drivers/polyfills, which
		// uses the served file system's own [types.WriteFileFS] when it has one.
		// localFS has one, which is what the cases above run against; memFS does
		// not, and the fallback that would build the write out of Create is not
		// written yet, so the upload is refused. That refusal carries no io/fs
		// sentinel for the status mapping to read, so it arrives as Unknown rather
		// than the Unimplemented the answer deserves — the same gap TestServerSymlink
		// records for readlink, and one that closes when the fallback lands.
		_, err := writeWhole(ctx, serve(t, servedFS(t)), "upload.txt", "content")
		wantCode(t, err, codes.Unknown)
	})

	t.Run("writing_an_existing_file_replaces_its_content", func(t *testing.T) {
		upload(ctx, t, client, "replaced.txt", "the whole content")
		upload(ctx, t, client, "replaced.txt", "less")
		got, err := inner.ReadFile("replaced.txt")
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != "less" {
			t.Errorf("content = %q, want the second write to have truncated the first", got)
		}
	})
}

// TestServerMkdirAll covers the directory rpc, which is the only way the service
// makes a directory: there is no rpc for the one-level Mkdir a file system may
// answer, so a missing level above is not something a client can be told to make
// on its own.
func TestServerMkdirAll(t *testing.T) {
	ctx := t.Context()
	client := serve(t, servedFS(t))

	if _, err := client.MkdirAll(ctx, &proto.MkdirAllRequest{Name: "deep/er/est", Mode: uint32(0o755)}); err != nil {
		t.Fatal(err)
	}
	got, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "deep/er/est"})
	if err != nil {
		t.Fatal(err)
	}
	if !got.GetIsDir() {
		t.Error("IsDir = false, want a directory")
	}
	if _, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "deep/er"}); err != nil {
		t.Errorf("the middle level did not come with it: %v", err)
	}
}

// TestServerRemove covers the two removal rpcs and the one case the service has
// no status code of its own for: a directory that is not empty.
func TestServerRemove(t *testing.T) {
	ctx := t.Context()
	inner := servedFS(t)
	client := serve(t, inner)

	t.Run("Remove_takes_one_name_away", func(t *testing.T) {
		if _, err := client.MkdirAll(ctx, &proto.MkdirAllRequest{Name: "gone", Mode: uint32(0o755)}); err != nil {
			t.Fatal(err)
		}
		ufsdriversTesting.WriteFile(t, inner, "gone/file.txt", "content")
		if _, err := client.Remove(ctx, &proto.RemoveRequest{Name: "gone/file.txt"}); err != nil {
			t.Fatal(err)
		}
		_, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "gone/file.txt"})
		wantCode(t, err, codes.NotFound)
	})

	t.Run("Remove_of_a_name_that_is_not_there_reports_NotFound", func(t *testing.T) {
		_, err := client.Remove(ctx, &proto.RemoveRequest{Name: "not/there"})
		wantCode(t, err, codes.NotFound)
	})

	t.Run("Remove_of_a_directory_that_is_not_empty_is_refused", func(t *testing.T) {
		if _, err := client.MkdirAll(ctx, &proto.MkdirAllRequest{Name: "full", Mode: uint32(0o755)}); err != nil {
			t.Fatal(err)
		}
		ufsdriversTesting.WriteFile(t, inner, "full/file.txt", "content")
		_, err := client.Remove(ctx, &proto.RemoveRequest{Name: "full"})
		wantCode(t, err, codes.FailedPrecondition)
		if _, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "full/file.txt"}); err != nil {
			t.Errorf("the refused removal took the child with it: %v", err)
		}
	})

	t.Run("RemoveAll_takes_a_whole_tree", func(t *testing.T) {
		if _, err := client.MkdirAll(ctx, &proto.MkdirAllRequest{Name: "tree/branch", Mode: uint32(0o755)}); err != nil {
			t.Fatal(err)
		}
		ufsdriversTesting.WriteFile(t, inner, "tree/branch/leaf.txt", "content")
		if _, err := client.RemoveAll(ctx, &proto.RemoveAllRequest{Name: "tree"}); err != nil {
			t.Fatal(err)
		}
		_, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "tree"})
		wantCode(t, err, codes.NotFound)
	})

	t.Run("RemoveAll_of_nothing_is_not_an_error", func(t *testing.T) {
		if _, err := client.RemoveAll(ctx, &proto.RemoveAllRequest{Name: "never/there"}); err != nil {
			t.Errorf("RemoveAll on a missing name: %v", err)
		}
	})
}
