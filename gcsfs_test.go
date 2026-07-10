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

package ufs

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"strings"
	"testing"

	"cloud.google.com/go/storage"
	"github.com/fsouza/fake-gcs-server/fakestorage"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/api/googleapi"
)

var fakeUpdatedTime = mustTime("2006-01-02T15:04:05Z")

func TestIsGCSFSUri(t *testing.T) {
	testCases := []struct {
		name string
		want bool
	}{
		{
			name: "gs:",
			want: true,
		},
		{
			name: "gs://",
			want: true,
		},
		{
			name: "gsfs://",
			want: false,
		},
		{
			name: cwdPath,
			want: false,
		},
		{
			name: "mem://",
			want: false,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isGCSFSUri(tc.name)
			if got != tc.want {
				t.Errorf("got: %t, want: %t", got, tc.want)
			}
		})
	}
}

func TestGetChunkSize(t *testing.T) {
	tests := []struct {
		size int
		want int
	}{
		{
			size: 0,
			want: minChunkSize,
		},
		{
			size: minChunkSize,
			want: minChunkSize * 2,
		},
		{
			size: minChunkSize * 2,
			want: minChunkSize * 4,
		},
		{
			size: minChunkSize * 100000,
			want: googleapi.DefaultUploadChunkSize,
		},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("getChunkSize(%d) = %d", tc.size, tc.want), func(t *testing.T) {
			t.Parallel()
			got := getChunkSize(tc.size)
			if tc.want != got {
				t.Errorf("getChunkSize(%d) want: '%d' got: '%d'", tc.size, tc.want, got)
			}
		})
	}
}

func TestGCSJoin(t *testing.T) {
	tests := []struct {
		parts []string
		want  string
	}{
		{
			parts: []string{"gs://"},
			want:  "gs://",
		},
		{
			parts: []string{"gs://bucket\\"},
			want:  "gs://bucket",
		},
		{
			parts: []string{"gs:\\\\bucket\\a"},
			want:  "gs://bucket/a",
		},
		{
			parts: []string{"gs://first"},
			want:  "gs://first",
		},
		{
			parts: []string{"gs://first", "a", "b", "c"},
			want:  "gs://first/a/b/c",
		},
		{
			parts: []string{"gs://first/a/b", "c"},
			want:  "gs://first/a/b/c",
		},
		{
			parts: []string{"gs://first\\a\\b", "c"},
			want:  "gs://first/a/b/c",
		},
		{
			parts: []string{"gs://first\\a\\b\\c"},
			want:  "gs://first/a/b/c",
		},
		{
			parts: []string{"gs://first/a/..", "b/../c"},
			want:  "gs://first/c",
		},
		{
			parts: []string{"", "b/c"},
			want:  "b/c",
		},
		{
			parts: []string{"", "b/../c"},
			want:  "c",
		},
	}

	for _, tc := range tests {
		t.Run(tc.want, func(t *testing.T) {
			t.Parallel()
			got := gcsJoin(tc.parts...)
			if d := cmp.Diff(tc.want, got); d != "" {
				t.Errorf("got %s, want %s diff(-want,+got):\n %v", got, tc.want, d)
			}
		})
	}
}

func TestParseGCSPath(t *testing.T) {
	tests := []struct {
		name       string
		wantBucket string
		wantObject string
	}{
		{
			name:       "gs://first",
			wantBucket: "first",
		},
		{
			name:       "gs://first/a/b/c",
			wantBucket: "first",
			wantObject: "a/b/c",
		},
		{
			name:       "gs://first/a",
			wantBucket: "first",
			wantObject: "a",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotBucket, gotObj, err := parseGCSPath(tc.name, "test")
			if err != nil {
				t.Error(err)
			}
			if d := cmp.Diff(tc.wantBucket, gotBucket); d != "" {
				t.Errorf("got %s, want %s diff(-want,+got):\n %v", gotBucket, tc.wantBucket, d)
			}
			if d := cmp.Diff(tc.wantObject, gotObj); d != "" {
				t.Errorf("got %s, want %s diff(-want,+got):\n %v", gotObj, tc.wantObject, d)
			}
		})
	}
}

func TestParseGCSPathErrors(t *testing.T) {
	tests := []struct {
		name            string
		wantErrContains string
	}{
		{
			name:            "file://a/c",
			wantErrContains: "does not contain the gs:// prefix",
		},
		{
			name:            "gs://",
			wantErrContains: "does not have a bucket name",
		},
		{
			name:            "gs:///object",
			wantErrContains: "does not have a bucket name",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gotBucket, gotObj, err := parseGCSPath(tc.name, "test")
			if err == nil {
				t.Errorf("want error that contains '%s'", tc.wantErrContains)
			} else if !strings.Contains(err.Error(), tc.wantErrContains) {
				t.Errorf("got: '%s' want contains: '%s'", err, tc.wantErrContains)
			}
			if gotBucket != "" {
				t.Errorf("bucket: want: '', got: '%s'", gotBucket)
			}
			if gotObj != "" {
				t.Errorf("object: want: '', got: '%s'", gotObj)
			}
		})
	}
}

func TestGCSFS(t *testing.T) {
	client := createStorage(t)
	testFileSystem(t, func(ctx context.Context, name string) (FS, error) {
		return newGCSFSWithClient(ctx, client, name)
	}, "gs://first")
}

func createStorage(tb testing.TB) *storage.Client {
	server := fakestorage.NewServer([]fakestorage.Object{
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: "first",
				Name:       "a",
				Updated:    fakeUpdatedTime,
			},
			Content: []byte("content: first/a"),
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: "first",
				Name:       "b",
				Updated:    fakeUpdatedTime,
			},
			Content: []byte("content: first/b"),
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: "first",
				Name:       "dir/c",
				Updated:    fakeUpdatedTime,
			},
			Content: []byte("content: first/dir/c"),
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: "first",
				Name:       "dir/d",
				Updated:    fakeUpdatedTime,
			},
			Content: []byte("content: first/dir/d"),
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: "first",
				Name:       "dir/sub/e",
				Updated:    fakeUpdatedTime,
			},
			Content: []byte("content: first/dir/sub/e"),
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: "first",
				Name:       "dir/sub/f",
				Updated:    fakeUpdatedTime,
			},
			Content: []byte("content: first/dir/sub/f"),
		},
		{
			ObjectAttrs: fakestorage.ObjectAttrs{
				BucketName: "first",
				Name:       "dir/subdir/g",
				Updated:    fakeUpdatedTime,
			},
			Content: []byte("content: first/dir/subdir/g"),
		},
	})

	tb.Cleanup(server.Stop)

	return server.Client()
}

func TestGCSFSRemove(t *testing.T) {
	client := createStorage(t)
	ctx := t.Context()
	fsys, err := newGCSFSWithClient(ctx, client, "gs://first")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	t.Run("file_exists", func(t *testing.T) {
		if err := fsys.Remove("a"); err != nil {
			t.Fatalf("Remove('a') = %v, want nil", err)
		}
		if _, err := fsys.Stat("a"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after Remove, Stat('a') = %v, want ErrNotExist", err)
		}
	})

	t.Run("not_exist", func(t *testing.T) {
		if err := fsys.Remove("nonexistent.txt"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("Remove(nonexistent) = %v, want ErrNotExist", err)
		}
	})

	t.Run("non_empty_dir", func(t *testing.T) {
		if err := fsys.Remove("dir"); !errors.Is(err, errDirNotEmpty) {
			t.Errorf("Remove(non-empty dir) = %v, want errDirNotEmpty", err)
		}
	})
}

func TestGCSFSRemoveAll(t *testing.T) {
	ctx := t.Context()

	t.Run("subtree", func(t *testing.T) {
		client := createStorage(t)
		fsys, err := newGCSFSWithClient(ctx, client, "gs://first")
		if err != nil {
			t.Fatal(err)
		}
		defer fsys.Close()

		if err := fsys.RemoveAll("dir"); err != nil {
			t.Fatalf("RemoveAll('dir') = %v, want nil", err)
		}
		if _, err := fsys.Stat("dir/c"); !errors.Is(err, fs.ErrNotExist) {
			t.Errorf("after RemoveAll('dir'), Stat('dir/c') = %v, want ErrNotExist", err)
		}
		if _, err := fsys.Stat("a"); err != nil {
			t.Errorf("after RemoveAll('dir'), Stat('a') = %v, want nil", err)
		}
	})

	t.Run("not_exist_is_noop", func(t *testing.T) {
		client := createStorage(t)
		fsys, err := newGCSFSWithClient(ctx, client, "gs://first")
		if err != nil {
			t.Fatal(err)
		}
		defer fsys.Close()

		if err := fsys.RemoveAll("nonexistent"); err != nil {
			t.Errorf("RemoveAll(nonexistent) = %v, want nil", err)
		}
	})

	t.Run("root", func(t *testing.T) {
		client := createStorage(t)
		fsys, err := newGCSFSWithClient(ctx, client, "gs://first")
		if err != nil {
			t.Fatal(err)
		}
		defer fsys.Close()

		if err := fsys.RemoveAll(cwdPath); err != nil {
			t.Fatalf("RemoveAll('.') = %v, want nil", err)
		}
		entries, err := fsys.ReadDir(cwdPath)
		if err != nil {
			t.Fatalf("ReadDir after RemoveAll('.') = %v, want nil", err)
		}
		if len(entries) != 0 {
			t.Errorf("after RemoveAll('.'), expected empty FS, got %d entries", len(entries))
		}
	})
}
