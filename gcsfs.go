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
	"io"
	"io/fs"
	"net/url"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"cloud.google.com/go/storage"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	minChunkSize = 512 * 1024
	gcsFSPrefix  = "gs:"
)

var (
	_ FS   = (*gcsFS)(nil)
	_ File = (*gcsFile)(nil)
)

type gcsFS struct {
	ctx          context.Context
	bucket       string
	baseDir      string
	client       *storage.Client
	subscription string
	psClientOpts []option.ClientOption
}

// gcsFile represents an opened GCS object for reading, a virtual directory, or
// a GCS writer for an in-progress Create.
type gcsFile struct {
	name       string
	content    []byte
	offset     int64
	isDir      bool
	size       int64
	modTime    time.Time
	dirEntries []fs.DirEntry
	dirOffset  int
	fsys       *gcsFS
	writer     *storage.Writer
}

func (f *gcsFile) Stat() (fs.FileInfo, error) {
	mode := fs.FileMode(fs.ModePerm)
	if f.isDir {
		mode = fs.ModeDir | fs.ModePerm
	}
	return &fsInfo{
		name:    path.Base(f.name),
		size:    f.size,
		mode:    mode,
		modTime: f.modTime,
		isDir:   f.isDir,
	}, nil
}

func (f *gcsFile) Read(p []byte) (int, error) {
	if f.isDir {
		return 0, pathError("read", f.name, fs.ErrInvalid)
	}
	if len(f.content) == 0 {
		return 0, io.EOF
	}
	if f.offset >= int64(len(f.content)) {
		return 0, io.EOF
	}
	n := copy(p, f.content[f.offset:])
	f.offset += int64(n)
	return n, nil
}

func (f *gcsFile) ReadAt(p []byte, off int64) (int, error) {
	if f.isDir {
		return 0, pathError("readat", f.name, fs.ErrInvalid)
	}
	if off >= int64(len(f.content)) {
		return 0, io.EOF
	}
	n := copy(p, f.content[off:])
	if off+int64(n) >= int64(len(f.content)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *gcsFile) Write(p []byte) (int, error) {
	return f.WriteString(string(p))
}

func (f *gcsFile) WriteString(s string) (int, error) {
	if f.writer == nil {
		return 0, pathError("write", f.name, fs.ErrInvalid)
	}
	return f.writer.Write([]byte(s))
}

func (f *gcsFile) Seek(offset int64, whence int) (int64, error) {
	if f.isDir {
		return 0, pathError("seek", f.name, fs.ErrInvalid)
	}
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		f.offset = int64(len(f.content)) + offset
	default:
		return 0, pathError("seek", f.name, fmt.Errorf("offset=%d whence=%d: invalid whence: %w", offset, whence, fs.ErrInvalid))
	}
	if f.offset < 0 {
		computed := f.offset
		f.offset = 0
		return 0, pathError("seek", f.name, fmt.Errorf("offset=%d whence=%d: position %d is before start of file: %w", offset, whence, computed, fs.ErrInvalid))
	}
	return f.offset, nil
}

func (f *gcsFile) Close() error {
	if f.writer != nil {
		w := f.writer
		f.writer = nil
		return w.Close()
	}
	return nil
}

func (f *gcsFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if !f.isDir {
		return nil, pathError("readdirent", f.name, fs.ErrInvalid)
	}
	all := f.dirEntries
	if f.dirOffset >= len(all) {
		if n > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}
	if n <= 0 {
		result := all[f.dirOffset:]
		f.dirOffset = len(all)
		return result, nil
	}
	end := min(f.dirOffset+n, len(all))
	result := all[f.dirOffset:end]
	f.dirOffset = end
	return result, nil
}

func (f *gcsFile) Readdir(n int) ([]fs.FileInfo, error) {
	if !f.isDir {
		return nil, pathError("readdir", f.name, fs.ErrInvalid)
	}
	entries, err := f.ReadDir(n)
	if err != nil {
		return nil, err
	}
	infos := make([]fs.FileInfo, 0, len(entries))
	for _, e := range entries {
		info, err := e.Info()
		if err != nil {
			return nil, err
		}
		infos = append(infos, info)
	}
	return infos, nil
}

func (fsys *gcsFS) getDeviceInfo() map[string]deviceInfo {
	return newDeviceInfoMap(deviceInfo{
		name:        "gs://" + fsys.bucket,
		deviceType:  "network",
		threadCount: 1,
	})
}

func (fsys *gcsFS) URI() *url.URL {
	vals := url.Values{"ro": {"true"}}
	if fsys.subscription != "" {
		vals.Set("subscription", fsys.subscription)
	}
	return &url.URL{
		Scheme:   "gs",
		Host:     fsys.bucket,
		Path:     "/" + fsys.baseDir,
		RawQuery: vals.Encode(),
	}
}

func (fsys *gcsFS) String() string {
	return fmt.Sprintf("gcsFS(%s)", fsys.URI())
}

func (fsys *gcsFS) Open(name string) (fs.File, error) {
	if name == cwdPath {
		entries, err := fsys.listDir("")
		if err != nil {
			return nil, pathError("open", cwdPath, err)
		}
		return &gcsFile{
			name:       cwdPath,
			isDir:      true,
			dirEntries: entries,
			fsys:       fsys,
		}, nil
	}
	if err := validPath("open", name); err != nil {
		return nil, err
	}

	// Use path.Join (not gcsJoin) so backslash characters are not normalized
	// to forward slashes — invalid fs.FS paths must not resolve to real objects.
	objPath := path.Join(fsys.baseDir, name)
	bkt := fsys.client.Bucket(fsys.bucket)

	attrs, err := bkt.Object(objPath).Attrs(fsys.ctx)
	if err == nil {
		rc, err := bkt.Object(objPath).NewReader(fsys.ctx)
		if err != nil {
			return nil, pathError("open", name, err)
		}
		content, err := io.ReadAll(rc)
		rc.Close()
		if err != nil {
			return nil, pathError("open", name, err)
		}
		return &gcsFile{
			name:    name,
			content: content,
			isDir:   false,
			size:    attrs.Size,
			modTime: attrs.Updated,
			fsys:    fsys,
		}, nil
	}
	if !errors.Is(err, storage.ErrObjectNotExist) {
		return nil, pathError("open", name, err)
	}

	// Not a file — check if it's a virtual directory (has objects under it).
	entries, err := fsys.listDir(name)
	if err != nil {
		return nil, pathError("open", name, err)
	}
	if len(entries) == 0 {
		return nil, pathError("open", name, fs.ErrNotExist)
	}
	return &gcsFile{
		name:       name,
		isDir:      true,
		dirEntries: entries,
		fsys:       fsys,
		// Zero modTime keeps virtual-directory stat consistent across calls.
	}, nil
}

func (fsys *gcsFS) Stat(name string) (fs.FileInfo, error) {
	if err := validPath("stat", name); err != nil {
		return nil, err
	}

	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	stat, statErr := f.Stat()
	closeErr := f.Close()
	if statErr != nil {
		return nil, joinErrors(statErr, closeErr)
	}
	return stat, closeErr
}

// listDir lists the immediate children of a virtual GCS directory.
// name is the FS-relative path; pass "" or cwdPath for the root.
func (fsys *gcsFS) listDir(name string) ([]fs.DirEntry, error) {
	var listPrefix string
	if name == "" || name == cwdPath {
		if fsys.baseDir != "" {
			listPrefix = fsys.baseDir + "/"
		}
	} else {
		listPrefix = path.Join(fsys.baseDir, name) + "/"
	}

	it := fsys.client.Bucket(fsys.bucket).Objects(fsys.ctx, &storage.Query{
		Prefix:    listPrefix,
		Delimiter: "/",
	})

	var entries []fs.DirEntry
	for {
		attrs, err := it.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		if attrs.Prefix != "" {
			dirName := strings.TrimSuffix(strings.TrimPrefix(attrs.Prefix, listPrefix), "/")
			if dirName == "" {
				continue
			}
			entries = append(entries, fs.FileInfoToDirEntry(&fsInfo{
				name:  dirName,
				mode:  fs.ModeDir | fs.ModePerm,
				isDir: true,
			}))
		} else {
			fileName := strings.TrimPrefix(attrs.Name, listPrefix)
			if fileName == "" {
				continue
			}
			entries = append(entries, fs.FileInfoToDirEntry(&fsInfo{
				name:    fileName,
				size:    attrs.Size,
				mode:    fs.ModePerm,
				modTime: attrs.Updated,
				isDir:   false,
			}))
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

func (fsys *gcsFS) Close() error {
	fsys.ctx = nil
	fsys.bucket = ""
	fsys.baseDir = ""
	return fsys.client.Close()
}

func (fsys *gcsFS) Create(name string) (File, error) {
	if err := validPath("create", name); err != nil {
		return nil, err
	}

	objPath := path.Join(fsys.baseDir, name)
	w := fsys.client.Bucket(fsys.bucket).Object(objPath).NewWriter(fsys.ctx)
	w.ChunkSize = minChunkSize

	return &gcsFile{
		name:   name,
		isDir:  false,
		fsys:   fsys,
		writer: w,
	}, nil
}

func (fsys *gcsFS) MkdirAll(name string, _ fs.FileMode) error {
	if err := validPath("mkdir", name); err != nil {
		return err
	}
	// GCS has no real directories; virtual directories emerge from object prefixes.
	return nil
}

func (fsys *gcsFS) ReadFile(name string) ([]byte, error) {
	if err := validPath("readfile", name); err != nil {
		return nil, err
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gf := f.(*gcsFile)
	if gf.isDir {
		return nil, pathError("readfile", name, fs.ErrInvalid)
	}
	return gf.content, nil
}

func (fsys *gcsFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if name != cwdPath {
		if err := validPath("readdir", name); err != nil {
			return nil, err
		}
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gf := f.(*gcsFile)
	if !gf.isDir {
		return nil, pathError("readdir", name, fs.ErrInvalid)
	}
	return gf.ReadDir(-1)
}

func (fsys *gcsFS) ReadLink(name string) (string, error) {
	if err := validPath("readlink", name); err != nil {
		return "", err
	}
	// GCS has no symlinks.
	return "", pathError("readlink", name, fs.ErrInvalid)
}

func (fsys *gcsFS) Lstat(name string) (fs.FileInfo, error) {
	if name != cwdPath {
		if err := validPath("lstat", name); err != nil {
			return nil, err
		}
	}
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return f.Stat()
}

func (fsys *gcsFS) Remove(name string) error {
	if err := validPath("remove", name); err != nil {
		return err
	}
	bkt := fsys.client.Bucket(fsys.bucket)
	objPath := path.Join(fsys.baseDir, name)

	// If there are virtual-directory children, refuse (non-empty directory).
	entries, err := fsys.listDir(name)
	if err != nil {
		return pathError("remove", name, err)
	}
	if len(entries) > 0 {
		return pathError("remove", name, errDirNotEmpty)
	}

	if err := bkt.Object(objPath).Delete(fsys.ctx); err != nil {
		if errors.Is(err, storage.ErrObjectNotExist) {
			return pathError("remove", name, fs.ErrNotExist)
		}
		return pathError("remove", name, err)
	}
	return nil
}

func (fsys *gcsFS) RemoveAll(name string) error {
	if name != cwdPath {
		if err := validPath("removeall", name); err != nil {
			return err
		}
	}
	bkt := fsys.client.Bucket(fsys.bucket)

	var listPrefix string
	if name == cwdPath {
		listPrefix = fsys.baseDir
		if listPrefix != "" {
			listPrefix += "/"
		}
	} else {
		objPath := path.Join(fsys.baseDir, name)
		// Best-effort delete of the exact object; ignore not-found.
		if delErr := bkt.Object(objPath).Delete(fsys.ctx); delErr != nil && !errors.Is(delErr, storage.ErrObjectNotExist) {
			return pathError("removeall", name, delErr)
		}
		listPrefix = objPath + "/"
	}

	it := bkt.Objects(fsys.ctx, &storage.Query{Prefix: listPrefix})
	for {
		attrs, err := it.Next()
		if errors.Is(err, iterator.Done) {
			break
		}
		if err != nil {
			return pathError("removeall", name, err)
		}
		if delErr := bkt.Object(attrs.Name).Delete(fsys.ctx); delErr != nil && !errors.Is(delErr, storage.ErrObjectNotExist) {
			return pathError("removeall", name, delErr)
		}
	}
	return nil
}

func newGCSFS(ctx context.Context, name string) (*gcsFS, error) {
	gcsClient, err := storage.NewClient(ctx)
	if err != nil {
		if strings.Contains(err.Error(), "credentials") {
			noauthGCSClient, noauthErr := storage.NewClient(ctx, option.WithoutAuthentication())
			if noauthErr != nil {
				return nil, err
			}
			gcsClient = noauthGCSClient
		} else {
			return nil, err
		}
	}

	return newGCSFSWithClient(ctx, gcsClient, name)
}

func newGCSFSWithClient(ctx context.Context, gcsClient *storage.Client, name string) (*gcsFS, error) {
	bucket, objectDir, err := parseGCSPath(name, "init")
	if err != nil {
		return nil, err
	}
	var subscription string
	if u, err := url.Parse(name); err == nil {
		subscription = u.Query().Get("subscription")
	}
	return &gcsFS{
		ctx:          ctx,
		client:       gcsClient,
		bucket:       bucket,
		baseDir:      objectDir,
		subscription: subscription,
	}, nil
}

func getChunkSize(size int) int {
	if size < minChunkSize {
		return minChunkSize
	}
	if size*2 > googleapi.DefaultUploadChunkSize {
		return googleapi.DefaultUploadChunkSize
	}
	return size * 2
}

func gcsJoin(parts ...string) string {
	path := strings.ReplaceAll(strings.Join(parts, "/"), "\\", "/")
	path = strings.TrimLeft(path, "/")
	prefix := ""
	after, ok := strings.CutPrefix(path, "gs://")
	if ok {
		prefix = "gs://"
		path = after
	}
	path = coerceUnix(filepath.Clean(path))
	if path == cwdPath {
		return prefix
	}
	return prefix + path
}

func parseGCSPath(p string, op string) (string, string, error) {
	raw := p
	// Strip query string before parsing bucket/prefix.
	if idx := strings.IndexByte(p, '?'); idx >= 0 {
		p = p[:idx]
	}
	after, ok := strings.CutPrefix(p, "gs://")
	if !ok {
		return "", "", pathError(op, raw, fmt.Errorf("'%s' does not contain the gs:// prefix", raw))
	}
	parts := strings.SplitN(after, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", pathError(op, raw, fmt.Errorf("'%s' does not have a bucket name", raw))
	}
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return parts[0], "", nil
}

func isGCSFSUri(name string) bool {
	return strings.HasPrefix(name, gcsFSPrefix)
}
