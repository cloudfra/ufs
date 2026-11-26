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
//
// Neither the name of the copyright holder nor the names of its contributors
// may be used to endorse or promote products derived from this software
// without specific prior written permission.

// Package keyguardfs wraps a file system so that the files holding security
// key material cannot be read through it. The guard is in process: a caller
// that reaches the same bytes another way is not restricted by it.
package keyguardfs

import (
	"fmt"
	"io"
	"io/fs"
	"net/url"
	"path"
	"slices"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/drivers/uri"
	"github.com/cloudfra/ufs/internal/fsutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/patternutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

var (
	_ types.ReadFS    = (*keyGuardFS)(nil)
	_ types.WrapperFS = (*keyGuardFS)(nil)
)

// ModeRefuse and ModeHide say how a protected file is presented to a caller.
const (
	// ModeRefuse lists a protected file, and refuses to read it or to describe
	// it.
	ModeRefuse = "refuse"
	// ModeHide reports a protected file as missing to every operation,
	// including the listings that name it.
	ModeHide = "hide"
)

// DefaultPatterns is the built-in catalog of names that hold key material.
// Names it holds with an exact spelling came from the OpenSSH, Kubernetes,
// Age, HashiCorp Vault and GCP service-account conventions.
var DefaultPatterns = []string{
	".env", ".env.*",
	"*.key", "*.pem", "*.p12", "*.pfx", "*.pkcs12",
	"*.jks", "*.keystore", "*.kdb", "*.kdbx",
	"id_rsa", "id_dsa", "id_ecdsa", "id_ed25519", "id_xmss", "id_sk_*",
	"credentials.json", "service-account*.json",
	".ssh", "secrets",
}

// ErrProtected says that a file system refused a path because its policy
// protects it. It matches [fs.ErrPermission], so a caller that only asks
// whether a path is writable or readable sees a permission failure.
var ErrProtected error = protectedError{}

// protectedError is the reason a guard refuses a path, told as a permission
// failure so that [errors.Is] finds [fs.ErrPermission] in a refusal.
type protectedError struct{}

func (protectedError) Error() string {
	return "protected by the key material policy"
}

// Is reports a match for [fs.ErrPermission], which is the standard error this
// refusal stands for.
func (protectedError) Is(target error) bool {
	return target == fs.ErrPermission
}

// Options declares which files a guard protects.
type Options struct {
	// Patterns are [path.Match] patterns. A path is protected when a pattern
	// matches the whole path or one component of it, so ".ssh" protects
	// everything below a .ssh directory. Empty selects [DefaultPatterns].
	Patterns []string `yaml:"patterns"`
	// Mode is [ModeRefuse] or [ModeHide]. Empty selects [ModeRefuse].
	Mode string `yaml:"mode"`
}

// keyGuardFS reads through an inner file system, refusing what its patterns
// protect.
type keyGuardFS struct {
	inner    types.ReadFS
	patterns []string
	mode     string
}

// New guards inner against the paths opts protects. It reports an error for an
// unknown Mode or a malformed pattern, so that a policy that protects nothing
// fails where it is built.
func New(inner types.ReadFS, opts Options) (types.ReadFS, error) {
	patterns := opts.Patterns
	if len(patterns) == 0 {
		patterns = DefaultPatterns
	}
	if err := patternutil.Validate(patterns); err != nil {
		return nil, err
	}
	switch opts.Mode {
	case "", ModeRefuse, ModeHide:
	default:
		return nil, fmt.Errorf("%q is not a keyGuard mode, want %q or %q", opts.Mode, ModeRefuse, ModeHide)
	}

	return &keyGuardFS{
		inner:    inner,
		patterns: slices.Clone(patterns),
		mode:     modeOrDefault(opts.Mode),
	}, nil
}

// modeOrDefault names the mode a guard runs in, which is [ModeRefuse] when no
// mode was asked for.
func modeOrDefault(mode string) string {
	if mode == "" {
		return ModeRefuse
	}

	return mode
}

func (fsys *keyGuardFS) Open(name string) (fs.File, error) {
	if err := pathutil.Validate("open", name); err != nil {
		return nil, err
	}
	if fsys.protected(name) {
		return nil, fsys.denied("open", name)
	}

	file, err := fsys.inner.Open(name)
	if err != nil {
		return nil, err
	}
	if !fsys.hiding() {
		return file, nil
	}

	// The handle of a directory lists its entries itself, so a guard that hides
	// names has to filter that handle too. Otherwise Open(dir) hands out the
	// names that ReadDir(dir) withholds. The handle is adapted with
	// [polyfills.ToFile] first, because it never drops a capability: fs.ReadDirFile
	// is how nestFS recognizes a mounted directory, and io.Seeker and io.ReaderAt
	// are how the hosts serve a read at an offset. A handle of a ufs driver is
	// already a [types.File], so the usual case adds no adaptation.
	dir, ok := polyfills.ToFile(file).(hiddenDirFile)
	if !ok {
		return file, nil
	}

	return &hiddenDirHandle{hiddenDirFile: dir, guard: fsys, dir: name}, nil
}

// hiddenDirFile is the handle of a directory, with the methods a guard keeps
// when it filters a listing.
type hiddenDirFile interface {
	types.File
	fs.ReadDirFile
}

// hiddenDirHandle answers ReadDir from one filtered snapshot of the listing,
// tracking the position that the guarded handle does not. The snapshot keeps a
// call with a count limit from losing the entries behind a run of hidden ones.
type hiddenDirHandle struct {
	hiddenDirFile
	guard   *keyGuardFS
	dir     string
	entries []fs.DirEntry
	off     int
}

func (dir *hiddenDirHandle) ReadDir(count int) ([]fs.DirEntry, error) {
	if dir.entries == nil {
		entries, err := dir.hiddenDirFile.ReadDir(-1)
		if err != nil {
			return nil, err
		}
		dir.entries = dir.guard.visible(dir.dir, entries)
	}
	rest := dir.entries[dir.off:]
	if count <= 0 {
		dir.off = len(dir.entries)
		return rest, nil
	}
	if len(rest) == 0 {
		return nil, io.EOF
	}
	if count < len(rest) {
		rest = rest[:count]
	}
	dir.off += len(rest)

	return rest, nil
}

func (fsys *keyGuardFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := pathutil.Validate("readdir", name); err != nil {
		return nil, err
	}
	if fsys.hiding() && fsys.protected(name) {
		return nil, fsys.denied("readdir", name)
	}

	entries, err := fsys.inner.ReadDir(name)
	if err != nil {
		return nil, err
	}
	if !fsys.hiding() {
		return entries, nil
	}

	return fsys.visible(name, entries), nil
}

func (fsys *keyGuardFS) ReadFile(name string) ([]byte, error) {
	if err := pathutil.Validate("readfile", name); err != nil {
		return nil, err
	}
	if fsys.protected(name) {
		return nil, fsys.denied("readfile", name)
	}

	return fsys.inner.ReadFile(name)
}

func (fsys *keyGuardFS) Stat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("stat", name); err != nil {
		return nil, err
	}
	if fsys.protected(name) {
		return nil, fsys.denied("stat", name)
	}

	return fsys.inner.Stat(name)
}

func (fsys *keyGuardFS) Lstat(name string) (fs.FileInfo, error) {
	if err := pathutil.Validate("lstat", name); err != nil {
		return nil, err
	}
	if fsys.protected(name) {
		return nil, fsys.denied("lstat", name)
	}

	return fsys.inner.Lstat(name)
}

func (fsys *keyGuardFS) ReadLink(name string) (string, error) {
	if err := pathutil.Validate("readlink", name); err != nil {
		return "", err
	}
	if fsys.protected(name) {
		return "", fsys.denied("readlink", name)
	}

	return fsys.inner.ReadLink(name)
}

// visible returns the entries of the listing of dir that this guard leaves
// visible, in the order the guarded file system reported them. A hidden file
// keeps its place in the tree, so only the listings naming it change.
func (fsys *keyGuardFS) visible(dir string, entries []fs.DirEntry) []fs.DirEntry {
	visible := make([]fs.DirEntry, 0, len(entries))
	for _, entry := range entries {
		if !fsys.protected(path.Join(dir, entry.Name())) {
			visible = append(visible, entry)
		}
	}

	return visible
}

// protected reports whether this guard's policy covers name.
func (fsys *keyGuardFS) protected(name string) bool {
	return patternutil.Match(fsys.patterns, name)
}

// hiding reports whether this guard hides what it protects rather than
// refusing to read it.
func (fsys *keyGuardFS) hiding() bool {
	return fsys.mode == ModeHide
}

// denied is the error for a protected name: a permission failure naming the
// policy, or a missing file for a guard that hides its refusals.
func (fsys *keyGuardFS) denied(op, name string) error {
	if fsys.hiding() {
		return ufserrors.NewPathError(op, name, fs.ErrNotExist)
	}

	return ufserrors.NewPathError(op, name, ErrProtected)
}

// InnerFS returns the guarded file system, which a caller holding it can read
// without the policy, see [types.WrapperFS].
func (fsys *keyGuardFS) InnerFS() fs.FS {
	return fsys.inner
}

// IsSame reports whether other holds the guarded file system's data, since a
// guard stores no data of its own, see [types.ComparableFS].
func (fsys *keyGuardFS) IsSame(other fs.FS) bool {
	return types.SameFS(fsys.inner, other)
}

// IsMountedArchiveDir forwards to the guarded file system, see
// [types.MountedArchiveDirFS].
func (fsys *keyGuardFS) IsMountedArchiveDir(name string) bool {
	if inner, ok := fsys.inner.(types.MountedArchiveDirFS); ok {
		return inner.IsMountedArchiveDir(name)
	}

	return false
}

// DeviceInfo returns the guarded file system's device map, see
// [types.DeviceInfoGet].
func (fsys *keyGuardFS) DeviceInfo() types.DeviceMap {
	return fsys.inner.DeviceInfo()
}

// URI returns the guarded file system's URI with ufs.ro=true added, because a
// guard never writes, see [types.URIGet].
func (fsys *keyGuardFS) URI() (*url.URL, error) {
	u, err := fsys.inner.URI()
	if err != nil {
		return nil, err
	}

	// Added with uri.AddParameter rather than the standard library's helper: the
	// pair walk leaves the guarded file system's own parameters in the order and
	// escaping they were written in, and returns a copy, so the guarded file
	// system keeps the URL it handed over. A nil URI stays nil rather than
	// becoming a URL invented out of nothing.
	return uri.AddParameter(u, "ufs.ro", "true")
}

func (fsys *keyGuardFS) String() string {
	return types.Description(map[string]any{
		"keyGuardFS": map[string]any{
			"inner":    types.Nested(fsutil.String(fsys.inner)),
			"mode":     fsys.mode,
			"patterns": fmt.Sprintf("%d", len(fsys.patterns)),
		},
	})
}

func (fsys *keyGuardFS) Close() error {
	return fsys.inner.Close()
}
