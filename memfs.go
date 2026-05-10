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
	"errors"
	"io"
	"io/fs"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

var (
	_ File          = (*memFile)(nil)
	_ FS            = (*memFS)(nil)
	_ fs.ReadFileFS = (*memFS)(nil)
	_ fs.ReadDirFS  = (*memFS)(nil)
	_ fs.ReadLinkFS = (*memFS)(nil)
	_ fs.GlobFS     = (*memFS)(nil)
)

type memFS struct {
	mu   sync.RWMutex
	root string
	dir  memNodeMap
}

type memNode struct {
	name    string
	content []byte
	mode    fs.FileMode
	modTime time.Time
}

type memNodeMap map[string]*memNode

type memFile struct {
	name      string
	content   []byte
	offset    int64
	mode      fs.FileMode
	modTime   time.Time
	mu        sync.Mutex
	fsys      *memFS
	dirOffset int
}

func (f *memFile) Stat() (fs.FileInfo, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return &fsInfo{
		name:    path.Base(f.name),
		size:    int64(len(f.content)),
		mode:    f.mode,
		modTime: f.modTime,
		isDir:   f.mode.IsDir(),
		sys:     nil,
	}, nil
}

func (f *memFile) Read(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
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

func (f *memFile) ReadAt(p []byte, off int64) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if off >= int64(len(f.content)) {
		return 0, io.EOF
	}
	n := copy(p, f.content[off:])
	if off+int64(n) >= int64(len(f.content)) {
		return n, io.EOF
	}
	return n, nil
}

func (f *memFile) Write(p []byte) (int, error) {
	return f.WriteString(string(p))
}

func (f *memFile) WriteString(s string) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.content = append(f.content, s...)
	f.modTime = time.Now()

	// Sync back to the filesystem
	if f.fsys != nil && !f.mode.IsDir() {
		f.fsys.mu.Lock()
		if e, ok := f.fsys.dir[f.name]; ok {
			e.content = bytes.Clone(f.content)
			e.modTime = f.modTime
		}
		f.fsys.mu.Unlock()
	}

	return len(s), nil
}

func (f *memFile) Seek(offset int64, whence int) (int64, error) {
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
		return 0, errors.New("invalid whence")
	}
	if f.offset < 0 {
		f.offset = 0
		return 0, errors.New("negative offset")
	}
	return f.offset, nil
}

func (f *memFile) Close() error {
	return nil
}

func (f *memFile) Readdir(n int) ([]fs.FileInfo, error) {
	if !f.mode.IsDir() {
		return nil, pathError("readdir", f.name, fs.ErrInvalid)
	}
	if f.fsys == nil {
		return nil, pathError("readdir", f.name, fs.ErrInvalid)
	}

	f.mu.Lock()
	defer f.mu.Unlock()
	fsys := f.fsys
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

	prefix := ""
	if f.name != "." {
		prefix = f.name
		if !strings.HasSuffix(prefix, "/") {
			prefix = prefix + "/"
		}
	}

	// Collect all unique child names
	seen := make(map[string]bool)
	var childNames []string
	for key := range fsys.dir {
		if key == "." {
			continue
		}
		if remainder, ok := strings.CutPrefix(key, prefix); ok {
			childName, _, _ := strings.Cut(remainder, "/")
			if childName != "" && !seen[childName] {
				seen[childName] = true
				childNames = append(childNames, childName)
			}
		}
	}
	sort.Strings(childNames)

	// Build file info for each child
	all := make([]fs.FileInfo, 0, len(childNames))
	for _, childName := range childNames {
		e := fsys.dir[prefix+childName]
		if e == nil {
			e = fsys.dir[prefix+childName+"/"]
		}
		if e != nil {
			all = append(all, &fsInfo{
				name:    e.name,
				size:    int64(len(e.content)),
				mode:    e.mode,
				modTime: e.modTime,
				isDir:   e.mode.IsDir(),
				sys:     nil,
			})
		}
	}

	// Handle exhaustion
	if f.dirOffset >= len(all) {
		if n > 0 {
			return nil, io.EOF
		}
		return nil, nil
	}

	// Determine how many entries to return
	if n > 0 {
		end := min(f.dirOffset+n, len(all))
		batch := all[f.dirOffset:end]
		f.dirOffset = end
		return batch, nil
	}

	// n <= 0: return all remaining
	batch := all[f.dirOffset:]
	f.dirOffset = len(all)
	return batch, nil
}

func (f *memFile) ReadDir(n int) ([]fs.DirEntry, error) {
	if !f.mode.IsDir() {
		return nil, pathError("readdirent", f.name, fs.ErrInvalid)
	}
	infos, err := f.Readdir(n)
	if err != nil {
		return nil, err
	}
	entries := make([]fs.DirEntry, 0, len(infos))
	for _, info := range infos {
		entries = append(entries, fs.FileInfoToDirEntry(info))
	}
	return entries, nil
}

func (fsys *memFS) Open(name string) (fs.File, error) {
	// Handle root directory
	if name == "." {
		return &memFile{
			name:    ".",
			content: nil,
			mode:    fs.ModeDir,
			modTime: time.Now(),
			fsys:    fsys,
		}, nil
	}
	if err := validPath("open", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

	e, err := fsys.findEnt(name)
	if err != nil {
		return nil, err
	}
	content := bytes.Clone(e.content)
	return &memFile{
		name:    name,
		content: content,
		mode:    e.mode,
		modTime: e.modTime,
		fsys:    fsys,
	}, nil
}

func (fsys *memFS) Close() error {
	return nil
}

func (fsys *memFS) Create(name string) (File, error) {
	if err := validPath("create", name); err != nil {
		return nil, err
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()

	now := time.Now()
	fsys.dir[name] = &memNode{
		name:    path.Base(name),
		content: nil,
		mode:    fs.ModePerm,
		modTime: now,
	}
	// Ensure parent dirs exist
	parent := path.Dir(name)
	if parent != "." {
		fsys.ensureParent(parent, now)
	}
	return &memFile{
		name:    name,
		content: nil,
		mode:    fs.ModePerm,
		modTime: now,
		fsys:    fsys,
	}, nil
}

func (fsys *memFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := validPath("mkdir", name); err != nil {
		return err
	}
	fsys.mu.Lock()
	defer fsys.mu.Unlock()

	full := name
	if !strings.HasSuffix(full, "/") {
		full = full + "/"
	}
	parts := strings.Split(strings.Trim(full, "/"), "/")
	accum := ""
	now := time.Now()
	for _, part := range parts {
		if part == "" {
			continue
		}
		accum += part + "/"
		if _, ok := fsys.dir[accum]; !ok {
			fsys.dir[accum] = &memNode{
				name:    part,
				content: nil,
				mode:    fs.ModeDir | perm,
				modTime: now,
			}
		}
	}
	return nil
}

func (fsys *memFS) findEnt(name string) (*memNode, error) {
	e, ok := fsys.dir[name]
	if ok {
		return e, nil
	}
	// Try with trailing slash for directories
	slashName := name + "/"
	e, ok = fsys.dir[slashName]
	if ok {
		return e, nil
	}
	return nil, pathError("open", name, fs.ErrNotExist)
}

func (fsys *memFS) ensureParent(dir string, now time.Time) {
	full := dir
	if !strings.HasSuffix(full, "/") {
		full = full + "/"
	}
	parts := strings.Split(strings.Trim(full, "/"), "/")
	accum := ""
	for _, part := range parts {
		if part == "" {
			continue
		}
		accum += part + "/"
		if _, ok := fsys.dir[accum]; !ok {
			fsys.dir[accum] = &memNode{
				name:    part,
				content: nil,
				mode:    fs.ModeDir | fs.ModePerm,
				modTime: now,
			}
		}
	}
}
func (fsys *memFS) ReadFile(name string) ([]byte, error) {
	if err := validPath("readfile", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	e, err := fsys.findEnt(name)
	if err != nil {
		return nil, err
	}
	return bytes.Clone(e.content), nil
}

func (fsys *memFS) ReadLink(name string) (string, error) {
	if err := validPath("readlink", name); err != nil {
		return "", err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	if _, err := fsys.findEnt(name); err != nil {
		return "", err
	}
	// memFS has no symlinks; every extant path is a regular file or directory.
	return "", pathError("readlink", name, fs.ErrInvalid)
}

func (fsys *memFS) Lstat(name string) (fs.FileInfo, error) {
	// Root directory is never stored in the map.
	if name == "." {
		return &fsInfo{name: ".", mode: fs.ModeDir, isDir: true}, nil
	}
	if err := validPath("lstat", name); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()
	e, err := fsys.findEnt(name)
	if err != nil {
		return nil, err
	}
	return &fsInfo{
		name:    e.name,
		size:    int64(len(e.content)),
		mode:    e.mode,
		modTime: e.modTime,
		isDir:   e.mode.IsDir(),
	}, nil
}

func (fsys *memFS) ReadDir(name string) ([]fs.DirEntry, error) {
	f, err := fsys.Open(name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	mf := f.(*memFile)
	if !mf.mode.IsDir() {
		return nil, pathError("readdir", name, fs.ErrInvalid)
	}
	return mf.ReadDir(-1)
}

func (fsys *memFS) Glob(pattern string) ([]string, error) {
	if _, err := path.Match(pattern, ""); err != nil {
		return nil, err
	}
	fsys.mu.RLock()
	defer fsys.mu.RUnlock()

	seen := make(map[string]bool)
	var matches []string
	for key := range fsys.dir {
		name := strings.TrimSuffix(key, "/")
		if seen[name] {
			continue
		}
		matched, err := path.Match(pattern, name)
		if err != nil {
			return nil, err
		}
		if matched {
			seen[name] = true
			matches = append(matches, name)
		}
	}
	sort.Strings(matches)
	return matches, nil
}

func newMemFS(name string) (FS, error) {
	return &memFS{
		root: strings.TrimPrefix(name, "memory://"),
		dir:  memNodeMap{},
	}, nil
}
