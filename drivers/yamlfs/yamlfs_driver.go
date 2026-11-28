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

package yamlfs

// This file holds the driver: the registration, the URI matcher and the
// constructor that reads a document off the host. It is not one of the
// yamlfs_<interface>.go files, because a driver is not an interface a file
// system value answers to.

import (
	"context"
	"fmt"
	"net/url"
	"path/filepath"
	"strings"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/osutil"
	"github.com/cloudfra/ufs/internal/pathutil"
	"github.com/cloudfra/ufs/internal/ufserrors"
)

const (
	// yamlFSScheme is the scheme [yamlFS.URI] reports a document file system
	// under, and yamlFSPrefix is what a URI addressed to this driver starts
	// with. They are one scheme spelled two ways, so a URI this file system
	// answers with is one this driver matches.
	yamlFSScheme = "yaml"
	yamlFSPrefix = yamlFSScheme + ":"
)

func init() {
	ufs.Register(ufs.NewDriver(yamlFSScheme, newYamlFS, isYamlFSURI, 1, true, false))
}

// isYamlFSURI reports whether name is a yaml: URI. It matches the scheme alone,
// the way every other driver matcher does, so a malformed address reaches
// [newYamlFS] and is reported by it rather than falling through to another
// driver. The match is case sensitive, as [ufs.NewDriver]'s matchers are.
func isYamlFSURI(name string) bool {
	return strings.HasPrefix(name, yamlFSPrefix)
}

// newYamlFS returns the file system held by the YAML or JSON document at the
// host path that a yaml: URI names, such as yaml:///etc/ufs/config.yaml.
//
// The address names a document by an absolute host path, the way an archive
// (archive:///path) and a bbolt file (bolt:/path) do: the "//" after the scheme
// is the authority separator, empty here, so what follows it is the URI path and
// that path starts at the host root. On Windows the drive takes the same form a
// local mount or a file: URI takes, yaml:///C:/srv/config.yaml. A path that is
// not absolute — yaml:config.yaml, whose path the URI parser keeps without a
// leading separator — is not an address this driver takes, because the address
// [yamlFS.URI] reports for it would not hold the same document back on a
// different working directory.
//
// The file system is labeled with that URI path without its leading separator,
// which is what [yamlFS.URI] expects: it puts the separator back when it reports
// the address, so the URI this file system answers with names the same document
// and reports that same URI again.
//
// The document is read once here, so the file system never sees a later change
// to the file, and every operation on it stays safe for concurrent use.
func newYamlFS(_ context.Context, name string) (types.WriteFS, error) {
	uriPath, err := documentPath(name)
	if err != nil {
		return nil, err
	}
	data, err := osutil.ReadFile(hostPath(uriPath))
	if err != nil {
		return nil, ufserrors.NewPathError("open", name, err)
	}
	fsys, err := New(strings.TrimPrefix(uriPath, pathutil.UnixSeparator), data)
	if err != nil {
		return nil, err
	}
	return fsys, nil
}

// documentPath returns the URI path of the document that the yaml: URI in name
// names, and reports an error for a name that is not such an address. The path
// is in URI form, so [hostPath] turns it into a path the host understands. The
// scheme is checked again here rather than trusted from [isYamlFSURI], the way
// gcsfs's parseGCSPath checks its own prefix, so that documentPath cannot be
// handed a name that belongs to another driver.
func documentPath(name string) (string, error) {
	u, err := url.Parse(name)
	if err != nil {
		return "", ufserrors.NewPathError("init", name, err)
	}
	if u.Scheme != yamlFSScheme {
		return "", ufserrors.NewPathError("init", name, fmt.Errorf("%q does not use the %s: scheme", name, yamlFSScheme))
	}
	if u.Host != "" {
		return "", ufserrors.NewPathError("init", name, fmt.Errorf("%q names the host %q, want %s//<host path> with no host", name, u.Host, yamlFSPrefix))
	}
	if u.Path == "" {
		return "", ufserrors.NewPathError("init", name, fmt.Errorf("%q does not name a document path, want %s//<host path>", name, yamlFSPrefix))
	}
	return u.Path, nil
}

// hostPath returns the path of the document named by a yaml: URI path, which is
// always written with URI separators. A Windows document on a drive is addressed
// yaml:///C:/srv/config.yaml, the way a local mount and a file: URI address it:
// the leading separator there belongs to the URI and not to the path, so it is
// dropped when the host says the rest starts with a volume, which is the same
// drop localFSNormalizePath makes in drivers/core/localfs_windows.go. On a host
// with no volumes the path is used as it is.
func hostPath(uriPath string) string {
	if strings.HasPrefix(uriPath, pathutil.UnixSeparator) {
		if rest := uriPath[len(pathutil.UnixSeparator):]; filepath.VolumeName(filepath.FromSlash(rest)) != "" {
			uriPath = rest
		}
	}
	return filepath.FromSlash(uriPath)
}
