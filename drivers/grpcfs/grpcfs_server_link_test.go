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
	"io/fs"
	"path"
	"testing"
	"time"

	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
)

// linkFS is the file system these cases serve: memFS underneath, with symbolic
// links added on top. memFS has no links — every name it holds is a file or a
// directory — so the two rpcs that are only about links could not be served by it
// alone, and the links cannot come from the machine's file system either. The
// three methods that have to know about a link are answered here and everything
// else, including the target of a link, is left to the memFS below.
type linkFS struct {
	types.WriteFS
	links map[string]string
}

func (f linkFS) Lstat(name string) (fs.FileInfo, error) {
	if target, ok := f.links[name]; ok {
		return linkInfo{name: name, target: target}, nil
	}
	return f.WriteFS.Lstat(name)
}

// Stat answers for a link with what its target is, which is what following a link
// means. The target has to be a name the file system below holds: a dangling link
// is a hole in the served tree, not something this fixture invents.
func (f linkFS) Stat(name string) (fs.FileInfo, error) {
	if target, ok := f.links[name]; ok {
		return f.WriteFS.Stat(target)
	}
	return f.WriteFS.Stat(name)
}

func (f linkFS) ReadLink(name string) (string, error) {
	if target, ok := f.links[name]; ok {
		return target, nil
	}
	return f.WriteFS.ReadLink(name)
}

// linkInfo is the fs.FileInfo a link reports about itself: the mode carries the
// bit that says a name is a link, and the size is the length of the target, which
// is what a link's content is on a file system that has them.
type linkInfo struct {
	name   string
	target string
}

// Name is the last element of the path, as a file's own name is everywhere else
// in the stack; the path a caller asked for travels in the request, not here.
func (i linkInfo) Name() string { return path.Base(i.name) }

func (i linkInfo) Size() int64 { return int64(len(i.target)) }

func (i linkInfo) Mode() fs.FileMode { return fs.ModeSymlink | 0o777 }

func (i linkInfo) ModTime() time.Time { return time.Time{} }

func (i linkInfo) IsDir() bool { return false }

func (i linkInfo) Sys() any { return nil }

// TestServerLinks covers the two rpcs that carry the following decision rather
// than making it themselves: GetFile's symlink_handling field and ReadLink. Both
// are answered by the served file system, which is why the service sends the flag
// instead of resolving a link on the caller's behalf.
func TestServerLinks(t *testing.T) {
	ctx := t.Context()

	const content = "what the link stands for"
	inner := servedFS(t)
	if err := inner.MkdirAll("dir", 0o755); err != nil {
		t.Fatal(err)
	}
	if err := inner.MkdirAll("linkdir", 0o755); err != nil {
		t.Fatal(err)
	}
	ufsdriversTesting.WriteFile(t, inner, "dir/target.txt", content)
	links := linkFS{WriteFS: inner, links: map[string]string{
		"link.txt":     "dir/target.txt",
		"linkdir/link": "dir/target.txt",
	}}
	client := serve(t, links)

	t.Run("a_GetFile_without_the_flag_answers_for_the_target", func(t *testing.T) {
		got, err := client.GetFile(ctx, &proto.GetFileRequest{Name: "link.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetName() != "link.txt" {
			t.Errorf("Name = %q, want the name the request made, whatever was followed to answer for it", got.GetName())
		}
		if got.GetSize() != int64(len(content)) {
			t.Errorf("Size = %d, want %d, the size of the target", got.GetSize(), len(content))
		}
		if fs.FileMode(got.GetMode())&fs.ModeSymlink != 0 {
			t.Errorf("Mode = %v, want a mode without the link bit once the link was followed", fs.FileMode(got.GetMode()))
		}
	})

	t.Run("a_GetFile_that_refuses_to_follow_answers_for_the_link", func(t *testing.T) {
		got, err := client.GetFile(ctx, &proto.GetFileRequest{
			Name:            "link.txt",
			SymlinkHandling: proto.SymlinkHandling_SYMLINK_HANDLING_NO_FOLLOW,
		})
		if err != nil {
			t.Fatal(err)
		}
		mode := fs.FileMode(got.GetMode())
		if mode&fs.ModeSymlink == 0 {
			t.Errorf("Mode = %v, want the link bit, since the request refused to follow it", mode)
		}
		if got.GetSize() != int64(len("dir/target.txt")) {
			t.Errorf("Size = %d, want %d, the length of the target a link's content is",
				got.GetSize(), len("dir/target.txt"))
		}
	})

	t.Run("a_link_inside_a_directory_is_not_a_directory", func(t *testing.T) {
		// The directory part of the path is followed while the last name is not,
		// which is what Lstat does and what the flag asks for.
		got, err := client.GetFile(ctx, &proto.GetFileRequest{
			Name:            "linkdir/link",
			SymlinkHandling: proto.SymlinkHandling_SYMLINK_HANDLING_NO_FOLLOW,
		})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetIsDir() {
			t.Error("IsDir = true, want false: a link is followed only in the levels above its name")
		}
	})

	t.Run("ReadLink_answers_the_target_that_was_stored", func(t *testing.T) {
		got, err := client.ReadLink(ctx, &proto.ReadLinkRequest{Name: "link.txt"})
		if err != nil {
			t.Fatal(err)
		}
		if got.GetTarget() != "dir/target.txt" {
			t.Errorf("Target = %q, want the stored target", got.GetTarget())
		}
	})

	t.Run("ReadLink_of_a_name_that_is_not_a_link_is_the_served_file_system_to_answer", func(t *testing.T) {
		// memFS holds every name in the tree as a file or a directory, so its
		// refusal of ReadLink is the reason the rpc fails, and the server passes it
		// through rather than deciding what the reason was.
		_, err := client.ReadLink(ctx, &proto.ReadLinkRequest{Name: "dir/target.txt"})
		wantCode(t, err, codes.InvalidArgument)
	})

	t.Run("ReadLink_of_a_name_that_is_not_there_reports_NotFound", func(t *testing.T) {
		_, err := client.ReadLink(ctx, &proto.ReadLinkRequest{Name: "nope"})
		wantCode(t, err, codes.NotFound)
	})
}
