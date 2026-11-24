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

//go:build unix

package grpcfs_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
)

// TestServerSymlink covers the two ways the service reaches a symbolic link:
// GetFile's symlink_handling, which says whether the last element of the path is
// followed, and ReadLink, which asks what the link itself says.
//
// The link is made in the served directory by the operating system rather than
// through the service: reading a link comes with types.WriteFS, but making one
// does not, so there is no rpc that would put a link there for these cases.
func TestServerSymlink(t *testing.T) {
	ctx := t.Context()
	dir := t.TempDir()
	inner := diskFSIn(t, dir)
	client := serve(t, inner)

	const content = "what the link points at"
	if err := os.WriteFile(filepath.Join(dir, "target.txt"), []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("target.txt", filepath.Join(dir, "link.txt")); err != nil {
		t.Fatal(err)
	}

	t.Run("following_the_link_answers_the_file_it_points_at", func(t *testing.T) {
		got, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "link.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetMode()&uint32(fs.ModeSymlink) != 0 {
			t.Errorf("Mode = %o, want the target's mode, not a link's", got.GetMode())
		}
		if got.GetSize() != int64(len(content)) {
			t.Errorf("Size = %d, want the target's size", got.GetSize())
		}
	})

	t.Run("not_following_the_link_answers_the_link", func(t *testing.T) {
		got, err := client.GetFile(ctx, &proto.GetFileRequest{
			Name: "link.txt", SymlinkHandling: proto.SymlinkHandling_SYMLINK_HANDLING_NO_FOLLOW,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetMode()&uint32(fs.ModeSymlink) == 0 {
			t.Errorf("Mode = %o, want the symlink bit NO_FOLLOW asks for", got.GetMode())
		}
	})

	t.Run("ReadLink_answers_what_the_link_says", func(t *testing.T) {
		got, err := client.ReadLink(ctx, &proto.ReadLinkRequest{Name: "link.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetTarget() != "target.txt" {
			t.Errorf("Target = %q, want the path as the link stored it", got.GetTarget())
		}
	})

	t.Run("ReadLink_of_a_name_that_is_not_a_link_is_an_error", func(t *testing.T) {
		// The file system answers that with EINVAL, which is not one of the
		// io/fs.Err* sentinels the status mapping reads, so it arrives as Unknown
		// rather than the InvalidArgument the failure deserves.
		_, err := client.ReadLink(ctx, &proto.ReadLinkRequest{Name: "target.txt"})
		wantCode(t, err, codes.Unknown)
	})
}

// TestServerWriteFileMode covers the mode an upload's first frame carries reaching
// the file the served file system makes. It lives here because it is a unix
// assertion: Windows reports no unix permission bits, so a file created there
// answers 0o777 whatever mode it was asked for. The mode is one a umask would
// not have produced on its own, so the answer can only come from the frame.
func TestServerWriteFileMode(t *testing.T) {
	ctx := t.Context()
	inner := diskFS(t)
	client := serve(t, inner)

	stream, err := client.WriteFile(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := stream.Send(&proto.WriteFileRequest{Name: "mode.txt", Mode: uint32(0o600), Data: []byte("content")}); err != nil {
		t.Fatal(err)
	}
	if _, err := stream.CloseAndRecv(); err != nil {
		t.Fatal(err)
	}

	info, err := inner.Stat("mode.txt")
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("perm = %b, want the mode the first frame carried", perm)
	}
}
