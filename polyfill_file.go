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
	"bytes"
	"io"
	"io/fs"
	"log/slog"
	"os"

	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

type bufferMode int

const (
	bufferMemory bufferMode = iota
	bufferDisk
)

var _ File = (*nestFile)(nil)

// nestFile wraps an fs.File and polyfills any methods from the File interface
// that the underlying implementation does not provide.
//
// When Seek or ReadAt is absent, the file content is buffered so that both
// operations work correctly at any position. In memory mode, content is read
// eagerly into a bytes.Reader. In disk mode, content is streamed to a temporary
// file. The buffer is released on Close. Read is also redirected through the
// buffer so that the file position stays consistent across Read/Seek/ReadAt.
type nestFile struct {
	fs.File
	buf             *bytes.Reader // non-nil when content is buffered in memory
	tmpFile         *os.File      // non-nil when content is buffered on disk
	writeFunc       func([]byte) (int, error)
	seekFunc        func(int64, int) (int64, error)
	readAtFunc      func([]byte, int64) (int, error)
	writeStringFunc func(string) (int, error)
}

func (f *nestFile) Read(p []byte) (int, error) {
	if f.buf != nil {
		return f.buf.Read(p)
	}
	if f.tmpFile != nil {
		return f.tmpFile.Read(p)
	}
	return f.File.Read(p)
}

func (f *nestFile) Close() error {
	f.buf = nil
	if f.tmpFile != nil {
		name := f.tmpFile.Name()
		if err := f.tmpFile.Close(); err != nil {
			return err
		}
		if err := osutil.Remove(name); err != nil {
			return err
		}
		f.tmpFile = nil
	}
	return f.File.Close()
}

func (f *nestFile) Write(p []byte) (int, error) {
	return f.writeFunc(p)
}

func (f *nestFile) Seek(off int64, whence int) (int64, error) {
	return f.seekFunc(off, whence)
}

func (f *nestFile) ReadAt(p []byte, off int64) (int, error) {
	return f.readAtFunc(p, off)
}

func (f *nestFile) WriteString(s string) (int, error) {
	return f.writeStringFunc(s)
}

// polyfillSeekReadAt populates nf.seekFunc and nf.readAtFunc for the underlying
// file f. If f already provides both io.Seeker and io.ReaderAt the native
// implementations are used directly. Otherwise the file content is buffered so
// that Seek and ReadAt work at any position. mode selects in-memory (bufferMemory)
// or temp-file (bufferDisk) buffering. On failure f is closed and the error
// returned.
func polyfillSeekReadAt(nf *nestFile, f fs.File, mode bufferMode) error {
	_, hasSeek := f.(io.Seeker)
	_, hasReadAt := f.(io.ReaderAt)
	if hasSeek && hasReadAt {
		nf.seekFunc = f.(io.Seeker).Seek
		nf.readAtFunc = f.(io.ReaderAt).ReadAt
		return nil
	}
	if mode == bufferDisk {
		return polyfillSeekReadAtDisk(nf, f)
	}
	return polyfillSeekReadAtMemory(nf, f)
}

func polyfillSeekReadAtMemory(nf *nestFile, f fs.File) error {
	data, err := io.ReadAll(f)
	if err != nil {
		if closeErr := f.Close(); closeErr != nil {
			slog.Warn("failed to close file after ReadAll error", "error", closeErr)
		}
		return err
	}
	nf.buf = bytes.NewReader(data)
	nf.seekFunc = nf.buf.Seek
	nf.readAtFunc = nf.buf.ReadAt
	return nil
}

func polyfillSeekReadAtDisk(nf *nestFile, f fs.File) error {
	tmp, err := osutil.CreateTemp("", "ufs-polyfill-*.tmp")
	if err != nil {
		fCloseErr := f.Close()
		return ufserrors.Join(err, fCloseErr)
	}
	if _, err := io.Copy(tmp, f); err != nil {
		closeErr := tmp.Close()
		removeErr := osutil.Remove(tmp.Name())
		fCloseErr := f.Close()
		return ufserrors.Join(err, closeErr, removeErr, fCloseErr)
	}
	if _, err := tmp.Seek(0, io.SeekStart); err != nil {
		closeErr := tmp.Close()
		removeErr := osutil.Remove(tmp.Name())
		fCloseErr := f.Close()
		return ufserrors.Join(err, closeErr, removeErr, fCloseErr)
	}
	nf.tmpFile = tmp
	nf.seekFunc = tmp.Seek
	nf.readAtFunc = tmp.ReadAt
	return nil
}

// wrapFile returns f unchanged if it already satisfies File. Otherwise it wraps
// f, polyfilling any missing methods. When readOnly is true, Write and
// WriteString always return fs.ErrInvalid. mode selects in-memory or disk-backed
// buffering for Seek/ReadAt polyfills.
func wrapFile(f fs.File, readOnly bool, mode bufferMode) (File, error) {
	if full, ok := f.(File); ok {
		return full, nil
	}
	nf := &nestFile{File: f}
	if err := polyfillSeekReadAt(nf, f, mode); err != nil {
		return nil, err
	}
	if readOnly {
		nf.writeFunc = func(_ []byte) (int, error) {
			return 0, fs.ErrInvalid
		}
		nf.writeStringFunc = func(_ string) (int, error) {
			return 0, fs.ErrInvalid
		}
		return nf, nil
	}
	if w, ok := f.(io.Writer); ok {
		nf.writeFunc = w.Write
	} else {
		nf.writeFunc = func(_ []byte) (int, error) {
			return 0, fs.ErrInvalid
		}
	}
	if sw, ok := f.(io.StringWriter); ok {
		nf.writeStringFunc = sw.WriteString
	} else if _, ok := f.(io.Writer); ok {
		nf.writeStringFunc = func(s string) (int, error) {
			return nf.writeFunc([]byte(s))
		}
	} else {
		nf.writeStringFunc = func(_ string) (int, error) {
			return 0, fs.ErrInvalid
		}
	}
	return nf, nil
}

// wrapReadOnlyFSFile returns f unchanged if it already satisfies File.
// Otherwise it wraps f for read-only use with in-memory buffering.
func wrapReadOnlyFSFile(f fs.File) (File, error) {
	return wrapFile(f, true, bufferMemory)
}

// wrapFSFile returns f unchanged if it already satisfies File. Otherwise it
// wraps f for read-write use with in-memory buffering.
func wrapFSFile(f fs.File) (File, error) {
	return wrapFile(f, false, bufferMemory)
}
