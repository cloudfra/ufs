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
//
// The implementation is split by the interface each method belongs to, one file
// per interface: this one holds what [types.SimpleFS] asks for and the policy
// every other half reads, and the siblings named keyguardfs_<interface>.go hold
// the rest.
package keyguardfs

import (
	"fmt"
	"io/fs"
	"slices"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/drivers/types"
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

func (fsys *keyGuardFS) Close() error {
	return fsys.inner.Close()
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
