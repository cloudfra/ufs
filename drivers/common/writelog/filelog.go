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

package writelog

import (
	"bufio"
	"cmp"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"

	"google.golang.org/protobuf/encoding/protodelim"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	pb "github.com/cloudfra/ufs/proto"
)

var (
	_ Log       = (*FileLog)(nil)
	_ io.Closer = (*FileLog)(nil)
)

const (
	// DefaultSegmentSize is the segment size NewFileLog uses when given 0.
	DefaultSegmentSize = 64 << 20

	segmentExt = ".wlog"
)

// FileLog is a [Log] that appends each entry, with its payload read from the
// source file system at append time, to segment files in a directory. Entries
// are length-delimited WriteLogEntry messages. A segment is closed once it
// reaches the segment size, and Snapshot also closes the current one; Commit
// deletes the segments a snapshot covered.
//
// Segment files are named NNNNNNNNNN.wlog in increasing order. Opening a
// directory keeps its existing segments, and truncates a torn final record
// left by a crash mid-append.
type FileLog struct {
	dir         string
	src         ufs.ReadFS
	segmentSize int64

	mu      sync.Mutex
	cur     *os.File
	curSize int64
	next    uint64   // number of the next segment to create
	closed  []string // closed segment paths, oldest first
	set     pendingSet
	isOpen  bool
}

// NewFileLog opens the log in dir, creating dir if needed, and reads payloads
// from src. segmentSize is the size at which a segment is closed; 0 uses
// DefaultSegmentSize.
func NewFileLog(dir string, src ufs.ReadFS, segmentSize int64) (*FileLog, error) {
	if segmentSize < 0 {
		return nil, fmt.Errorf("writelog: segment size %d must not be negative", segmentSize)
	}
	if segmentSize == 0 {
		segmentSize = DefaultSegmentSize
	}
	if err := os.MkdirAll(dir, osutil.DefaultDirectoryPermissions); err != nil {
		return nil, err
	}
	l := &FileLog{dir: dir, src: src, segmentSize: segmentSize, set: newPendingSet(), isOpen: true}
	segments, err := l.listSegments()
	if err != nil {
		return nil, err
	}
	for i, seg := range segments {
		last := i == len(segments)-1
		if err := readSegment(seg, last, func(e *pb.WriteLogEntry) { l.set.add(withoutContent(e)) }); err != nil {
			return nil, err
		}
		n, _ := segmentNumber(seg)
		l.next = n + 1
	}
	l.closed = segments
	return l, nil
}

// listSegments returns the segment files in dir, oldest first.
func (l *FileLog) listSegments() ([]string, error) {
	entries, err := os.ReadDir(l.dir)
	if err != nil {
		return nil, err
	}
	var segments []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if _, ok := segmentNumber(e.Name()); ok {
			segments = append(segments, filepath.Join(l.dir, e.Name()))
		}
	}
	slices.SortFunc(segments, func(a, b string) int {
		na, _ := segmentNumber(a)
		nb, _ := segmentNumber(b)
		return cmp.Compare(na, nb)
	})
	return segments, nil
}

// segmentNumber parses the number of the segment file name.
func segmentNumber(name string) (uint64, bool) {
	base, ok := strings.CutSuffix(filepath.Base(name), segmentExt)
	if !ok {
		return 0, false
	}
	n, err := strconv.ParseUint(base, 10, 64)
	return n, err == nil
}

// Append reads entry's payload from the source file system, when it is an
// OP_PUT, and appends the entry to the current segment. A put whose file no
// longer exists is skipped.
func (l *FileLog) Append(entry *pb.WriteLogEntry) error {
	if err := validate(entry); err != nil {
		return err
	}
	e := entry
	if isPut(entry) {
		withPayload, ok, err := readPayload(l.src, entry.GetName())
		if err != nil || !ok {
			return err
		}
		e = withPayload
	}

	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isOpen {
		return ufserrors.NewPathError("writelog", l.dir, fs.ErrClosed)
	}
	if l.cur == nil {
		name := filepath.Join(l.dir, fmt.Sprintf("%010d%s", l.next, segmentExt))
		f, err := os.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, osutil.DefaultFilePermissions) //nolint:gosec // G304: name is a segment in the log's own directory
		if err != nil {
			return err
		}
		l.cur, l.curSize = f, 0
		l.next++
	}
	n, err := protodelim.MarshalTo(l.cur, e)
	l.curSize += int64(n)
	if err != nil {
		return err
	}
	l.set.add(withoutContent(e))
	if l.curSize >= l.segmentSize {
		return l.rotateLocked()
	}
	return nil
}

// rotateLocked closes the current segment, if any. l.mu must be held.
func (l *FileLog) rotateLocked() error {
	if l.cur == nil {
		return nil
	}
	name := l.cur.Name()
	err := ufserrors.Join(l.cur.Sync(), l.cur.Close())
	l.cur = nil
	l.closed = append(l.closed, name)
	return err
}

// Snapshot closes the current segment and returns every pending entry in the
// closed segments, deduped and in replay order.
func (l *FileLog) Snapshot() (*Snapshot, error) {
	l.mu.Lock()
	if !l.isOpen {
		l.mu.Unlock()
		return nil, ufserrors.NewPathError("writelog", l.dir, fs.ErrClosed)
	}
	if err := l.rotateLocked(); err != nil {
		l.mu.Unlock()
		return nil, err
	}
	segments := slices.Clone(l.closed)
	seqs := l.set.seqs()
	l.mu.Unlock()

	set := newPendingSet()
	for _, seg := range segments {
		if err := readSegment(seg, false, set.add); err != nil {
			return nil, err
		}
	}
	removes, mkdirs, puts := set.ordered()
	entries := slices.Concat(removes, mkdirs, puts)
	return &Snapshot{Entries: entries, seqs: seqs, segments: segments}, nil
}

// Commit deletes the segments the snapshot was read from and removes its
// entries that nothing newer has replaced.
func (l *FileLog) Commit(s *Snapshot) error {
	if s == nil {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	var errs []error
	for _, seg := range s.segments {
		if err := os.Remove(seg); err != nil && !errors.Is(err, fs.ErrNotExist) {
			errs = append(errs, err)
		}
		l.closed = slices.DeleteFunc(l.closed, func(c string) bool { return c == seg })
	}
	l.set.commit(s.seqs)
	return ufserrors.Join(errs...)
}

// Pending reports whether name has an uncommitted OP_PUT entry.
func (l *FileLog) Pending(name string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.set.pending(name)
}

// Bytes reports the total size of the pending OP_PUT entries.
func (l *FileLog) Bytes() int64 {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.set.bytes
}

// Close closes the current segment. The segments stay on disk for the next
// NewFileLog. Later calls return nil.
func (l *FileLog) Close() error {
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.isOpen {
		return nil
	}
	l.isOpen = false
	return l.rotateLocked()
}

// countingReader counts the bytes read through it.
type countingReader struct {
	r *bufio.Reader
	n int64
}

func (c *countingReader) Read(p []byte) (int, error) {
	n, err := c.r.Read(p)
	c.n += int64(n)
	return n, err
}

func (c *countingReader) ReadByte() (byte, error) {
	b, err := c.r.ReadByte()
	if err == nil {
		c.n++
	}
	return b, err
}

// readSegment calls add for every entry in the segment file name. When last
// is set, a record that can't be read is taken to be torn by a crash
// mid-append and the file is truncated before it; otherwise it is an error.
func readSegment(name string, last bool, add func(*pb.WriteLogEntry)) error {
	f, err := os.Open(name) //nolint:gosec // G304: name is a segment in the log's own directory
	if err != nil {
		return err
	}
	r := &countingReader{r: bufio.NewReader(f)}
	opts := protodelim.UnmarshalOptions{MaxSize: -1}
	var good int64
	for {
		e := &pb.WriteLogEntry{}
		err := opts.UnmarshalFrom(r, e)
		if errors.Is(err, io.EOF) {
			return f.Close()
		}
		if err == nil {
			err = validate(e)
		}
		if err != nil {
			if !last {
				return ufserrors.Join(fmt.Errorf("writelog: corrupt segment %q at offset %d: %w", name, good, err), f.Close())
			}
			return ufserrors.Join(f.Close(), os.Truncate(name, good))
		}
		add(e)
		good = r.n
	}
}
