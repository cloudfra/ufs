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
	"fmt"
	"io/fs"
	"net/url"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"gopkg.in/yaml.v3"
)

const (
	// readOnlyOption is the [MountSpec] option that the fstab "ro" option and
	// the implicit root map to. It is served by drivers/decorators/readonlyfs.
	readOnlyOption = "readOnly"

	// roQueryParam is the URI query parameter that marks a read-only file
	// system; it is not a mount point.
	roQueryParam = "ro"

	// optionsQueryParam is the URI query parameter that holds the decorators
	// of a file system, in the order they are applied; it is not a mount
	// point. Its value is the options list of a [MountSpec] in YAML flow form.
	optionsQueryParam = "options"

	// embedFSPrefix is the scheme of drivers/embedfs, which has no URI-based
	// constructor.
	embedFSPrefix = "embed://"

	// mountOp is the operation reported in errors from mounting a file system.
	mountOp = "mount"

	// fstab mount options.
	fstabReadOnly  = "ro"
	fstabReadWrite = "rw"
	fstabDefaults  = "defaults"

	// fstabNoMountPoint is the fstab mount point that designates the root.
	fstabNoMountPoint = "none"
)

// CreateURI constructs a URI understood by [New] that layers additional file
// systems at specific mount paths inside the base file system. The nested map
// maps mount-point paths (e.g. "cache", "data/scratch") to the URI of the file
// system to mount there. Paths follow [fs.ValidPath] conventions.
//
// Pass the returned URI directly to [New]:
//
//	 ctx := context.Background()
//		uri, _ := ufs.CreateURI("file:///srv/data", map[string]string{
//		    "tmp": "memory://",
//		})
//		fsys, _ := ufs.New(ctx, uri)
//
// Returns an error if name or any nested URI cannot be parsed.
func CreateURI(name string, nested map[string]string) (string, error) {
	u, err := nameToURI(name)
	if err != nil {
		return "", err
	}
	vals := u.Query()
	mountPoints := make([]string, 0, len(nested))
	for mountPoint := range nested {
		mountPoints = append(mountPoints, mountPoint)
	}
	sort.Strings(mountPoints)
	for _, mountPoint := range mountPoints {
		mu, err := nameToURI(nested[mountPoint])
		if err != nil {
			return "", err
		}
		vals.Set(mountPoint, mu.String())
	}
	u.RawQuery = vals.Encode()
	return u.String(), nil
}

func nameToURI(name string) (*url.URL, error) {
	u, err := url.Parse(name)
	if err == nil {
		return u, nil
	}
	origErr := err
	if after, ok := strings.CutPrefix(name, "file://"); ok {
		// On Windows, "file://C:\path" fails url.Parse because the drive-letter
		// colon is misread as a host:port separator with "\path" as an invalid
		// port number. Recover by stripping "file://" and re-interpreting the
		// remainder as a local path, normalizing to forward slashes.
		localPath := filepath.ToSlash(after)
		if !strings.HasPrefix(localPath, "/") {
			localPath = "/" + localPath
		}
		return &url.URL{Scheme: "file", Path: localPath}, nil
	}
	if strings.Contains(name, "://") {
		return nil, fmt.Errorf("%q is not a uri for file system, %w", name, origErr)
	}
	// Assume the name is a local file path.
	u, err = url.Parse("file://" + name)
	if err != nil {
		return nil, fmt.Errorf("%q is not a uri for file system, %w", name, origErr)
	}
	return u, nil
}

// New opens a file system identified by name. The returned [FS] wraps the
// backend in a nestFS layer that automatically mounts archives found inside the
// tree (see below). Always call Close on the returned FS when done.
//
// # URI schemes
//
//   - memory://   — volatile in-memory file system; all data is lost when the
//     FS is closed or the process exits. Safe for concurrent use.
//   - null://     — /dev/null semantics: Create and MkdirAll always succeed,
//     writes are accepted but discarded, reads return empty content, Stat
//     reports everything as a directory. Useful in tests.
//   - angry://    — always returns [fs.ErrInvalid]; used to exercise
//     error-handling paths in tests.
//   - file://path  or a bare path — local directory, mounted read-write via
//     [os.OpenRoot] (Go 1.24+). Access outside the mount root is rejected by
//     the OS. On Windows, directory Stat always reports size 0 (unlike the raw
//     os package which may report 4096).
//   - gs://bucket/prefix — Google Cloud Storage bucket, optionally scoped to a
//     prefix. Credentials are resolved via ADC; unauthenticated access is tried
//     as a fallback.
//   - https:// or http:// URL ending in a recognized archive extension — the
//     archive is downloaded to a temporary directory, mounted read-only, and the
//     temporary directory is removed when Close is called.
//   - A path ending in .git — the repository is shallow-cloned into a temporary
//     directory (not available on AIX).
//   - A local path pointing to a recognized archive (.zip, .tar, .tar.gz, etc.)
//     is mounted read-only through the archive's contents.
//
// # Alternative input formats
//
// In addition to URIs, name may be an fstab-format string or a YAML document.
// Both formats are auto-detected before falling back to URI parsing.
//
// fstab (fields: source, mountpoint, type, options [, dump, pass]):
//
//	memory://   .      auto  rw  0  0
//	null://     cache  auto  ro  0  0
//
// The mount point ".", "/", or "none" designates the root filesystem. Leading
// slashes on other mount points are stripped. The "ro" option is shorthand for
// the readOnly decorator; "rw" and "defaults" are recognized but leave the FS
// writable.
// Comment lines (starting with #) and blank lines are ignored.
//
// YAML (flat list of [MountSpec] entries):
//
//   - source: "memory://"
//     mountPoint: "."
//   - source: "null://"
//     mountPoint: "cache"
//     options:
//   - readOnly: true
//   - fault:
//     errorRate: 0.1
//
// options is a list. Each entry is the case-sensitive name of a [Decorator]
// and its configuration. Decorators are applied in the order listed: the first
// wraps the source and each later one wraps the one before it, so the last
// entry is the outermost layer.
// Decorators register themselves when their package is imported, e.g.
// github.com/cloudfra/ufs/drivers/decorators/readonlyfs (readOnly) and
// github.com/cloudfra/ufs/drivers/decorators/faultfs (fault). A section that
// matches no registered decorator is an error.
//
// If no entry has a root mount point (".", "/", "none", or empty), a read-only
// null:// filesystem is used as the root, which needs the readOnly decorator.
//
// # Nested mounts and archive auto-mounting
//
// The returned FS wraps all backends in a nestFS layer. When a directory entry
// named foo.zip (or any recognized archive extension) exists, the virtual path
// foo.zip.d is automatically exposed as a read-only mount of that archive's
// contents. No explicit configuration is required.
//
// Use [CreateURI] to pre-configure additional mount points before calling New.
func New(ctx context.Context, name string) (WriteFS, error) {
	if specs := parseMountSpec(name); specs != nil {
		return newFromMountSpec(ctx, specs)
	}
	u, err := url.Parse(name)
	if err == nil {
		query := u.Query()
		baseURI := *u
		baseURI.RawQuery = ""
		baseURI.Fragment = ""
		// The decorators of the base travel with it; every other query
		// parameter is a mount point.
		if opts, ok := query[optionsQueryParam]; ok {
			baseURI.RawQuery = url.Values{optionsQueryParam: opts}.Encode()
		}
		nFS, err := openNestFS(ctx, baseURI.String())
		if err == nil {
			for mountPath, mountURI := range query {
				if mountPath == roQueryParam || mountPath == optionsQueryParam {
					continue
				}
				mountFS, err := openNestFS(ctx, mountURI[0])
				if err != nil {
					return nil, ufserrors.Join(err, nFS.Close())
				}
				if err := nFS.addMount(mountPath, mountFS); err != nil {
					return nil, ufserrors.Join(err, mountFS.Close(), nFS.Close())
				}
			}
			return nFS, nil
		}
	}
	return openNestFS(ctx, name)
}

// openNestFS opens name via newDecoratedFS and wraps the result in a nestFS
// layer. It returns the concrete *nestFS so callers can add mounts via
// addMount.
func openNestFS(ctx context.Context, name string) (*nestFS, error) {
	fsys, err := newDecoratedFS(ctx, name, nil)
	if err != nil {
		return nil, err
	}
	return makeNestFS(ctx, fsys), nil
}

// FSBuilder composes an [FS] from a root URI and a set of mounts that may be
// specified as URI strings or as pre-built [FS] instances (e.g.
// github.com/cloudfra/ufs/drivers/embedfs.New).
// Call [NewFSBuilder] to create one, chain [FSBuilder.Mount] /
// [FSBuilder.MountFS] to add mounts, then call [FSBuilder.Build] or
// [FSBuilder.BuildURI].
type FSBuilder struct {
	name   string
	mounts []fsBuildMount
}

type fsBuildMount struct {
	path string
	uri  string  // non-empty for URI mounts
	fsys WriteFS // non-nil for FS mounts
}

// NewFSBuilder creates a builder rooted at the given URI string. An empty
// string is treated as "null://" (a FS that discards all writes and returns
// empty content on reads). To use a pre-parsed [*url.URL], pass u.String().
func NewFSBuilder(name string) *FSBuilder {
	return &FSBuilder{name: name}
}

// Mount adds a URI-based mount at path. It returns the builder for chaining.
func (b *FSBuilder) Mount(path, uri string) *FSBuilder {
	b.mounts = append(b.mounts, fsBuildMount{path: path, uri: uri})
	return b
}

// MountFS adds a pre-built [FS] as a mount at path. It returns the builder
// for chaining. Pre-built mounts cannot be serialized by [FSBuilder.BuildURI].
func (b *FSBuilder) MountFS(path string, fsys WriteFS) *FSBuilder {
	b.mounts = append(b.mounts, fsBuildMount{path: path, fsys: fsys})
	return b
}

// Build constructs the [FS], opening the root URI and applying all configured
// mounts. The caller must Close the returned FS when done.
func (b *FSBuilder) Build(ctx context.Context) (FS, error) {
	rootName := b.name
	if rootName == "" {
		rootName = nullFSPrefix
	}
	nFS, err := openNestFS(ctx, rootName)
	if err != nil {
		return nil, err
	}
	for _, m := range b.mounts {
		var mountFS *nestFS
		if m.fsys != nil {
			mountFS = makeNestFS(ctx, m.fsys)
		} else {
			mountFS, err = openNestFS(ctx, m.uri)
			if err != nil {
				return nil, ufserrors.Join(err, nFS.Close())
			}
		}
		if err := nFS.addMount(m.path, mountFS); err != nil {
			return nil, ufserrors.Join(err, mountFS.Close(), nFS.Close())
		}
	}
	return nFS, nil
}

// BuildURI serialises the builder to a URI string accepted by [New]. It
// returns an error if any mount was added via [FSBuilder.MountFS], because
// pre-built [FS] instances have no URI representation.
func (b *FSBuilder) BuildURI() (string, error) {
	rootName := b.name
	if rootName == "" {
		rootName = nullFSPrefix
	}
	nested := make(map[string]string, len(b.mounts))
	for _, m := range b.mounts {
		if m.fsys != nil {
			return "", fmt.Errorf("mount %q uses a pre-built FS and cannot be serialized to a URI", m.path)
		}
		nested[m.path] = m.uri
	}
	return CreateURI(rootName, nested)
}

func newBaseFS(ctx context.Context, name string) (WriteFS, error) {
	// drivers/embedfs wraps a Go embed.FS directly and has no URI-based
	// constructor; give a clear error instead of an unhelpful "not found".
	if strings.HasPrefix(name, embedFSPrefix) {
		return nil, ufserrors.NewPathError(mountOp, name, fmt.Errorf("embed:// file systems must be created with drivers/embedfs.New, not New(): %w", fs.ErrInvalid))
	}
	r := getRegistrar()
	driver, err := r.matchDriver(name)
	if err != nil {
		// Drivers outside this package (drivers/...) register their scheme only
		// when imported, so a missing blank import looks like an unknown path.
		return nil, ufserrors.NewPathError(mountOp, name, fmt.Errorf("%q is not a valid mount path for %s; if it needs a driver from github.com/cloudfra/ufs/drivers, check that the driver package is imported, %w", name, runtime.GOOS, err))
	}
	fsys, err := r.create(ctx, name)
	if err != nil {
		return nil, fmt.Errorf("failed to mount %q with driver %q, %w", name, driver.Name, err)
	}
	return fsys, nil
}

// MountSpec describes a single mount entry with a source URI, a mount point,
// and mount options.
type MountSpec struct {
	Source     string `yaml:"source"`
	MountPoint string `yaml:"mountPoint"`
	// Options lists the decorators wrapped around the mounted file system,
	// in the order they are applied: the first wraps the source and the last
	// is the outermost layer.
	Options []MountOption `yaml:"options"`
}

// MountOption configures one decorator of a [MountSpec]. In YAML it is a
// mapping with a single key, the decorator's name, whose value is the
// decorator's configuration:
//
//   - readOnly: true
//   - fault:
//     errorRate: 0.1
type MountOption struct {
	// Name is the name of a registered [Decorator], see [RegisterDecorator].
	Name string

	// Config is the decorator's raw configuration, see [DecodeOptions].
	Config any
}

// UnmarshalYAML reads the single-key mapping form of a [MountOption].
func (o *MountOption) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode || len(node.Content) != 2 {
		return fmt.Errorf("line %d: a mount option must be a mapping with a single decorator name", node.Line)
	}
	if err := node.Content[0].Decode(&o.Name); err != nil {
		return err
	}
	return node.Content[1].Decode(&o.Config)
}

// MarshalYAML writes the single-key mapping form of a [MountOption].
func (o MountOption) MarshalYAML() (any, error) {
	return map[string]any{o.Name: o.Config}, nil
}

func parseMountSpec(input string) []MountSpec {
	if specs, err := parseYAMLMountSpec(input); err == nil {
		return specs
	}
	if specs, err := parseFstabMountSpec(input); err == nil {
		return specs
	}
	return nil
}

func parseYAMLMountSpec(input string) ([]MountSpec, error) {
	var specs []MountSpec
	if err := yaml.Unmarshal([]byte(input), &specs); err != nil {
		return nil, fmt.Errorf("yaml unmarshal: %w", err)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("yaml: no mount entries")
	}
	for i := range specs {
		if specs[i].Source == "" {
			return nil, fmt.Errorf("yaml: entry %d missing source", i)
		}
		specs[i].MountPoint = normalizeMountPoint(specs[i].MountPoint)
	}
	return specs, nil
}

func parseFstabMountSpec(input string) ([]MountSpec, error) {
	var specs []MountSpec
	for i, line := range strings.Split(input, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 4 {
			return nil, fmt.Errorf("fstab: line %d has %d fields, need at least 4", i+1, len(fields))
		}
		options := fields[3]
		if !isFstabOptions(options) {
			return nil, fmt.Errorf("fstab: line %d has unrecognized options %q", i+1, options)
		}

		spec := MountSpec{
			Source:     fields[0],
			MountPoint: normalizeMountPoint(fields[1]),
		}
		if hasFstabOption(options, fstabReadOnly) {
			spec.Options = []MountOption{{Name: readOnlyOption, Config: true}}
		}
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("fstab: no mount entries")
	}
	return specs, nil
}

func isFstabOptions(s string) bool {
	for opt := range strings.SplitSeq(s, ",") {
		switch opt {
		case fstabReadOnly, fstabReadWrite, fstabDefaults:
			return true
		}
	}
	return false
}

func hasFstabOption(s, option string) bool {
	for opt := range strings.SplitSeq(s, ",") {
		if opt == option {
			return true
		}
	}
	return false
}

func normalizeMountPoint(mp string) string {
	mp = strings.TrimPrefix(mp, "/")
	if mp == "" || mp == fstabNoMountPoint {
		return pathutil.CwdPath
	}
	return mp
}

// defaultRootSpec is used when no root mount point is provided.
var defaultRootSpec = MountSpec{
	Source:     nullFSPrefix,
	MountPoint: pathutil.CwdPath,
	Options:    []MountOption{{Name: readOnlyOption, Config: true}},
}

// newDecoratedFS opens name via newBaseFS and wraps it with the decorators in
// the options query parameter of name, if any, followed by those in opts.
func newDecoratedFS(ctx context.Context, name string, opts []MountOption) (WriteFS, error) {
	name, uriOpts, err := splitURIOptions(name)
	if err != nil {
		return nil, ufserrors.NewPathError(mountOp, name, err)
	}
	if len(uriOpts) > 0 {
		opts = append(uriOpts, opts...)
	}
	baseFS, err := newBaseFS(ctx, name)
	if err != nil {
		return nil, err
	}
	fsys, err := getRegistrar().decorate(ctx, baseFS, opts)
	if err != nil {
		return nil, ufserrors.Join(ufserrors.NewPathError(mountOp, name, err), baseFS.Close())
	}
	return fsys, nil
}

func newFromMountSpec(ctx context.Context, specs []MountSpec) (WriteFS, error) {
	var root *MountSpec
	var mounts []MountSpec
	for i := range specs {
		if pathutil.IsCwd(specs[i].MountPoint) {
			root = &specs[i]
		} else {
			mounts = append(mounts, specs[i])
		}
	}
	if root == nil {
		root = &defaultRootSpec
	}

	rootFS, err := newDecoratedFS(ctx, root.Source, root.Options)
	if err != nil {
		return nil, err
	}
	nFS := makeNestFS(ctx, rootFS)

	for _, m := range mounts {
		mountFS, err := newDecoratedFS(ctx, m.Source, m.Options)
		if err != nil {
			return nil, ufserrors.Join(err, nFS.Close())
		}
		mountNestFS := makeNestFS(ctx, mountFS)
		if err := nFS.addMount(m.MountPoint, mountNestFS); err != nil {
			return nil, ufserrors.Join(err, mountNestFS.Close(), nFS.Close())
		}
	}

	return nFS, nil
}
