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

// Package gcsfs provides a ufs file system backed by a Google Cloud Storage
// bucket. Importing it registers the gs:// scheme with [ufs.New]:
//
//	import _ "github.com/cloudfra/ufs/drivers/gcsfs"
//
// The implementation is split by the interface each method belongs to, one file
// per interface: this one holds the file-system value, the handles and what
// [types.SimpleFS] asks for, the siblings named gcsfs_<interface>.go hold the
// rest, and gcsfs_notify.go already holds [types.Watcher].
package gcsfs

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
	"sync"
	"sync/atomic"
	"time"

	"cloud.google.com/go/storage"
	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

const (
	minChunkSize = 512 * 1024
	gcsFSPrefix  = "gs:"

	// subscriptionParam is the URI query parameter that names the Pub/Sub
	// subscription Watch pulls notifications from. It carries the "ufs." prefix
	// because a query parameter written without one names a mount point inside
	// the file system rather than a setting of it.
	subscriptionParam = "ufs.subscription"
)

var (
	_ types.WriteFS = (*gcsFS)(nil)
	_ types.File    = (*gcsFile)(nil)
)

func init() {
	ufs.Register(ufs.NewDriver("gcs", New, isGCSFSUri, 1, true, true))
}

// gcsFS reads ctx, bucket and baseDir on every request, so they stay exactly as
// constructed for the whole life of the value: a caller that reaches a file
// system while another goroutine closes it keeps a usable context and answers
// either success or an error matching [fs.ErrClosed], never a panic.
type gcsFS struct {
	ctx          context.Context
	bucket       string
	baseDir      string
	client       *storage.Client
	subscription string
	psClientOpts []option.ClientOption

	// closed says Close has been called, after which this file system refuses
	// everything it would have put on the wire.
	closed atomic.Bool
}

// closedErr answers why a call cannot be made because the file system has been
// closed, or nil when it can. A file system that gave its client up has no way to
// answer anything else, so every method that reaches the bucket asks here rather
// than letting the transport report a client that is gone.
func (fsys *gcsFS) closedErr(op, name string) error {
	if fsys.closed.Load() {
		return ufserrors.NewPathError(op, name, fs.ErrClosed)
	}
	return nil
}

// gcsFile represents an opened GCS object for reading, a virtual directory, or
// a GCS writer for an in-progress Create.
//
// mu guards the sequential cursor of the handle, so one handle handed to two
// callers gives them one position rather than two callers racing one counter.
type gcsFile struct {
	name    string
	content []byte

	// mu guards the sequential cursor of the handle: offset, which is where the
	// next Read continues from, and dirOffset below, which is where the next
	// ReadDir continues from. A handle is an object or a directory for its whole
	// life — isDir is fixed at Open — so one mutex covers both cursors.
	mu     sync.Mutex
	offset int64

	isDir   bool
	size    int64
	modTime time.Time
	// dirEntries is fixed when Open hands the handle back and nothing writes it
	// afterwards; dirOffset is the position into it that ReadDir continues from.
	dirEntries []fs.DirEntry
	dirOffset  int
	fsys       *gcsFS
	writer     *storage.Writer
}

func (f *gcsFile) Stat() (fs.FileInfo, error) {
	mode := fs.ModePerm
	if f.isDir {
		mode = fs.ModeDir | fs.ModePerm
	}
	return types.NewFileInfo(path.Base(f.name), f.size, mode, f.modTime), nil
}

func (f *gcsFile) Read(p []byte) (int, error) {
	if f.isDir {
		return 0, ufserrors.NewPathError("read", f.name, fs.ErrInvalid)
	}
	if len(f.content) == 0 {
		return 0, io.EOF
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.offset >= int64(len(f.content)) {
		return 0, io.EOF
	}
	n := copy(p, f.content[f.offset:])
	f.offset += int64(n)
	return n, nil
}

// ReadAt reads from off without moving offset, so it needs no lock: content is
// fixed when the handle is opened and this method touches no state of its own.
// Two ReadAt calls, or a ReadAt and a Read, therefore never cost each other a
// byte — only the sequential cursor of Read is shared, and only Read and Seek
// move it.
func (f *gcsFile) ReadAt(p []byte, off int64) (int, error) {
	if f.isDir {
		return 0, ufserrors.NewPathError("readat", f.name, fs.ErrInvalid)
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
		return 0, ufserrors.NewPathError("write", f.name, fs.ErrInvalid)
	}
	return f.writer.Write([]byte(s))
}

func (f *gcsFile) Seek(offset int64, whence int) (int64, error) {
	if f.isDir {
		return 0, ufserrors.NewPathError("seek", f.name, fs.ErrInvalid)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch whence {
	case io.SeekStart:
		f.offset = offset
	case io.SeekCurrent:
		f.offset += offset
	case io.SeekEnd:
		f.offset = int64(len(f.content)) + offset
	default:
		return 0, ufserrors.NewPathError("seek", f.name, fmt.Errorf("offset=%d whence=%d: invalid whence: %w", offset, whence, fs.ErrInvalid))
	}
	if f.offset < 0 {
		computed := f.offset
		f.offset = 0
		return 0, ufserrors.NewPathError("seek", f.name, fmt.Errorf("offset=%d whence=%d: position %d is before start of file: %w", offset, whence, computed, fs.ErrInvalid))
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

// ReadDir returns the next n children of the directory, or every remaining
// child when n is not positive, and moves the position the next call continues
// from. That position is guarded, so one directory handle handed to two walkers
// gives them one cursor: between them they are handed every child exactly once,
// rather than one child twice and another skipped. The children themselves were
// fixed when Open returned the handle, so the slices handed back share one
// read-only array and one caller's result cannot change another's.
func (f *gcsFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if !f.isDir {
		return nil, ufserrors.NewPathError("readdirent", f.name, fs.ErrInvalid)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
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

// Readdir is the legacy io/fs form of the same walk. It leaves the cursor guard
// to ReadDir rather than taking it here, so the two forms cannot hold it at the
// same time and always continue from one position.
func (f *gcsFile) Readdir(n int) ([]fs.FileInfo, error) {
	if !f.isDir {
		return nil, ufserrors.NewPathError("readdir", f.name, fs.ErrInvalid)
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

func (fsys *gcsFS) Open(name string) (fs.File, error) {
	if err := fsys.closedErr("open", name); err != nil {
		return nil, err
	}
	if name == pathutil.CwdPath {
		entries, err := fsys.listDir("")
		if err != nil {
			return nil, ufserrors.NewPathError("open", pathutil.CwdPath, err)
		}
		return &gcsFile{
			name:       pathutil.CwdPath,
			isDir:      true,
			dirEntries: entries,
			fsys:       fsys,
		}, nil
	}
	if err := pathutil.Validate("open", name); err != nil {
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
			return nil, ufserrors.NewPathError("open", name, err)
		}
		content, err := io.ReadAll(rc)
		closeErr := rc.Close()
		if err != nil || closeErr != nil {
			return nil, ufserrors.Join(ufserrors.NewPathError("open", name, err), closeErr)
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
		return nil, ufserrors.NewPathError("open", name, err)
	}

	// Not a file — check if it's a virtual directory (has objects under it).
	entries, err := fsys.listDir(name)
	if err != nil {
		return nil, ufserrors.NewPathError("open", name, err)
	}
	if len(entries) == 0 {
		return nil, ufserrors.NewPathError("open", name, fs.ErrNotExist)
	}
	return &gcsFile{
		name:       name,
		isDir:      true,
		dirEntries: entries,
		fsys:       fsys,
		// Zero modTime keeps virtual-directory stat consistent across calls.
	}, nil
}

// listDir lists the immediate children of a virtual GCS directory.
// name is the FS-relative path; pass "" or CwdPath for the root. Every caller of
// it — Open, Stat and Remove — has already refused a closed file system, so this
// one never sees a client that Close has given up.
func (fsys *gcsFS) listDir(name string) ([]fs.DirEntry, error) {
	var listPrefix string
	if name == "" || name == pathutil.CwdPath {
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
			entries = append(entries, fs.FileInfoToDirEntry(types.NewFileInfo(dirName, 0, fs.ModeDir|fs.ModePerm, time.Time{})))
		} else {
			fileName := strings.TrimPrefix(attrs.Name, listPrefix)
			if fileName == "" {
				continue
			}
			entries = append(entries, fs.FileInfoToDirEntry(types.NewFileInfo(fileName, attrs.Size, fs.ModePerm, attrs.Updated)))
		}
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	return entries, nil
}

// Close makes this file system unusable and gives up the storage client it
// holds. Closing twice is not an error, and only the first call reaches the
// client. The context, bucket and prefix are left as constructed: blanking them
// here is what handed a nil context to a caller that had already read the field
// and was mid-request.
func (fsys *gcsFS) Close() error {
	if !fsys.closed.CompareAndSwap(false, true) {
		return nil
	}
	return fsys.client.Close()
}

// New returns a file system for the gs://bucket/dir URI name, authenticating
// with the default Google Cloud credentials. When no credentials are available
// it falls back to unauthenticated access, which works for public buckets.
func New(ctx context.Context, name string) (types.WriteFS, error) {
	return makeGCSFS(ctx, name)
}

func makeGCSFS(ctx context.Context, name string) (*gcsFS, error) {
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

	return makeGCSFSWithClient(ctx, gcsClient, name)
}

// NewWithClient returns a file system for the gs://bucket/dir URI name that
// uses gcsClient for every request, for callers that need custom credentials,
// endpoints, or options.
func NewWithClient(ctx context.Context, gcsClient *storage.Client, name string) (types.WriteFS, error) {
	return makeGCSFSWithClient(ctx, gcsClient, name)
}

func makeGCSFSWithClient(ctx context.Context, gcsClient *storage.Client, name string) (*gcsFS, error) {
	bucket, objectDir, err := parseGCSPath(name, "init")
	if err != nil {
		return nil, err
	}
	var subscription string
	if u, err := url.Parse(name); err == nil {
		subscription = u.Query().Get(subscriptionParam)
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
	path = pathutil.CoerceUnix(filepath.Clean(path))
	if path == pathutil.CwdPath {
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
		return "", "", ufserrors.NewPathError(op, raw, fmt.Errorf("'%s' does not contain the gs:// prefix", raw))
	}
	parts := strings.SplitN(after, "/", 2)
	if len(parts) == 0 || parts[0] == "" {
		return "", "", ufserrors.NewPathError(op, raw, fmt.Errorf("'%s' does not have a bucket name", raw))
	}
	if len(parts) == 2 {
		return parts[0], parts[1], nil
	}
	return parts[0], "", nil
}

func isGCSFSUri(name string) bool {
	return strings.HasPrefix(name, gcsFSPrefix)
}
