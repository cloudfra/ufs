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
	"bytes"
	"context"
	"testing"
	"time"

	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
)

// TestServerWriteFile covers the client-streaming upload, where the name and the
// mode travel in the first frame and the content in every frame.
//
// The whole-file write the rpc answers with is not something every file system
// offers, so it goes through drivers/polyfills, which builds it out of the
// writable-handle methods a file system does have. Every inner a Server can be
// handed implements types.CreateFileFS, because that is part of types.WriteFS,
// so the upload always has something to be built from: what these cases can say
// about a refusal is about the name, not about a missing capability.
func TestServerWriteFile(t *testing.T) {
	ctx := t.Context()
	inner := servedFS(t)
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
		// The mode of the first frame reaches the file: memFS has no umask and
		// keeps the permission bits it is asked for, so 0o600 can only have come
		// from the frame rather than from a default or a mask. A file system with
		// a umask narrows it, as it narrows any mode.
		info, err := inner.Stat("upload.txt")
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0o600 {
			t.Errorf("perm = %b, want the mode the first frame carried", perm)
		}
	})

	t.Run("an_empty_stream_names_no_file", func(t *testing.T) {
		stream, err := client.WriteFile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, err = stream.CloseAndRecv()
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("a_frame_that_names_no_mode_makes_a_file_no_one_can_read", func(t *testing.T) {
		// proto3 cannot tell a mode field that is unset from one set to 0, so an
		// omitted mode reaches the file system as mode 000, which is what memFS
		// then stores. This is the disagreement between the .proto and the io/fs
		// permission bits that the handler comment names: substituting a default
		// for a 0 would break the caller that asked for 000.
		stream, err := client.WriteFile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Send(&proto.WriteFileRequest{Name: "nomode.txt", Data: []byte("content")}); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.CloseAndRecv(); err != nil {
			t.Fatal(err)
		}
		info, err := inner.Stat("nomode.txt")
		if err != nil {
			t.Fatal(err)
		}
		if perm := info.Mode().Perm(); perm != 0 {
			t.Errorf("perm = %b, want the mode 0 the frame left unset", perm)
		}
	})

	t.Run("a_missing_parent_is_refused", func(t *testing.T) {
		// The rpc says the parents of an upload are not created, which is not what
		// the served file system would have answered on its own: memFS builds the
		// levels of a name it is asked to write, while a file system rooted on disk
		// reports that they are missing. The handler is what makes the two agree.
		_, err := writeWhole(ctx, client, "missing/child.txt", "child")
		wantCode(t, err, codes.NotFound)
		if _, err := inner.Stat("missing"); err == nil {
			t.Error("the refused upload made the missing directory anyway")
		}
	})

	t.Run("a_name_under_a_regular_file_is_refused", func(t *testing.T) {
		// The same lookup sees a level that is there but is not a directory. This
		// name is refused by the served file system as well — memFS reports
		// fs.ErrExist for a path with a regular file among its levels — so the code
		// here is the one both halves of the service already agree on.
		upload(ctx, t, client, "regular.txt", "content")
		_, err := writeWhole(ctx, client, "regular.txt/child.txt", "child")
		wantCode(t, err, codes.AlreadyExists)
	})

	t.Run("an_upload_into_a_directory_is_refused", func(t *testing.T) {
		if _, err := client.MkdirAll(ctx, &proto.MkdirAllRequest{Name: "adir", Mode: uint32(0o755)}); err != nil {
			t.Fatal(err)
		}
		_, err := writeWhole(ctx, client, "adir", "content")
		wantCode(t, err, codes.InvalidArgument)
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

	// The ceilings the package sets, written out because a test in this package
	// cannot see the constants: a change to one of those is a change to these.
	const (
		// A frame of this size is above what grpc allows a server to receive by
		// default and below the ceiling the package raises it to, so the frames
		// below travel because the server carries the options the package tells a
		// host to put on it.
		frameSize = 8 << 20
		// The most content one WriteFile request may make a server hold.
		uploadCeiling = 32 << 20
	)

	t.Run("one_frame_can_be_larger_than_grpc_allows_by_default", func(t *testing.T) {
		// The rpc documents a whole small file traveling in one frame, and one
		// frame is one HTTP request body: what a server will accept is not the
		// library's 4 MiB default but the ceiling the package names.
		content := bytes.Repeat([]byte("y"), 5<<20)
		stream, err := client.WriteFile(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if err := stream.Send(&proto.WriteFileRequest{Name: "onebigframe.bin", Mode: uint32(0o600), Data: content}); err != nil {
			t.Fatal(err)
		}
		if _, err := stream.CloseAndRecv(); err != nil {
			t.Fatal(err)
		}
		got, err := inner.ReadFile("onebigframe.bin")
		if err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(got, content) {
			t.Errorf("%d bytes were written, want the %d that were sent", len(got), len(content))
		}
	})

	t.Run("an_upload_bigger_than_the_handler_holds_for_it_is_refused", func(t *testing.T) {
		// A handler that buffers a whole upload before writing it puts a ceiling
		// on what that can cost. Every frame here is one the transport accepts, so
		// the handler is what stops the upload — and it stops it with the code grpc
		// itself uses for a message that is too big, so the two agree.
		upper, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		stream, err := client.WriteFile(upper)
		if err != nil {
			t.Fatal(err)
		}
		frame := bytes.Repeat([]byte("x"), frameSize)
		for range uploadCeiling/len(frame) + 2 {
			if err := stream.Send(&proto.WriteFileRequest{Name: "toobig.bin", Mode: uint32(0o600), Data: frame}); err != nil {
				break
			}
		}
		_, err = stream.CloseAndRecv()
		wantCode(t, err, codes.ResourceExhausted)
		if _, err := inner.Stat("toobig.bin"); err == nil {
			t.Error("the refused upload left a file behind")
		}
	})

	t.Run("an_upload_one_byte_under_that_ceiling_is_written", func(t *testing.T) {
		// The ceiling is a ceiling rather than a target: an upload of one less
		// than it is several frames and arrives whole.
		upper, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()

		const wanted = uploadCeiling - 1
		stream, err := client.WriteFile(upper)
		if err != nil {
			t.Fatal(err)
		}
		for sent := 0; sent < wanted; {
			n := min(frameSize, wanted-sent)
			frame := &proto.WriteFileRequest{Name: "justunder.bin", Mode: uint32(0o600), Data: bytes.Repeat([]byte("z"), n)}
			if err := stream.Send(frame); err != nil {
				t.Fatalf("after %d bytes: %v", sent, err)
			}
			sent += n
		}
		if _, err := stream.CloseAndRecv(); err != nil {
			t.Fatal(err)
		}
		info, err := inner.Stat("justunder.bin")
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() != wanted {
			t.Errorf("size = %d, want the %d bytes the frames carried", info.Size(), wanted)
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
