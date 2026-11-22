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

package polyfills

import (
	"embed"
	"errors"
	"fmt"
	"net/url"
	"os"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/pathutil"
)

// ErrUnsupportedURI is reported by [URI] for a file system whose type carries no
// locatable source that a URI could name.
var ErrUnsupportedURI = errors.New("unsupported file system type")

// URI reports the canonical URI of fsys, so a caller holding a file system of
// unknown provenance can ask the question [types.URIGet] answers without a type
// assertion of its own: the URI the file system reports when it reports one, or
// one derived from a standard library file system type whose source is known,
// or [ErrUnsupportedURI] naming the type.
//
// fsys is any rather than [io/fs.FS] because the standard library's directory
// root, [*os.Root], is not an [io/fs.FS] — its Open returns a [*os.File] — yet
// names the directory it was opened on, which is exactly what a URI carries.
//
// A derived URL built from a type is only as reopenable as that type's source:
// the file:// URI of an [*os.Root] can be handed back to ufs.New and opens the
// same directory again, while "embed:" says where the data lives in the process
// and ufs.New has no URI-based constructor for embedfs, so a reopen from it does
// not build the same content.
//
// Only a type whose assertion reveals its source is recognized. An [*os.Root]
// reveals its directory through [os.Root.Name], so it resolves to a file:// URI
// spelled the way the local driver reports its own. The result of os.DirFS is
// the unexported os.dirFS, whose root is held in an unexported string, and the
// [io/fs.FS] from [os.Root.FS] is the unexported *os.rootFS, which reveals
// neither a name nor a URI method, so neither is recognized and each ends in the
// error below. A nil [*os.Root] ends there too rather than dereferencing a
// receiver that has no name to read.
//
// The returned URL is a copy that belongs to the caller: editing it does not
// change the URL the file system reports, and the file system keeps the URL it
// returns to its own callers.
func URI(fsys any) (*url.URL, error) {
	if ug, ok := fsys.(types.URIGet); ok {
		u, err := ug.URI()
		if err != nil {
			return nil, err
		}
		if u != nil {
			out := *u
			return &out, nil
		}
		// A nil answer falls through rather than being reported: the assertion
		// found a URI method that declined to answer, which is not evidence that
		// the file system has no locatable source.
	}
	if root, ok := fsys.(*os.Root); ok && root != nil {
		// Spelled the way drivers/core spells the URI of a local file system, so
		// a root and a localFS opened on the same directory report the same URI.
		return &url.URL{Scheme: "file", Path: pathutil.CoerceUnix(root.Name())}, nil
	}
	if _, ok := fsys.(embed.FS); ok {
		return &url.URL{Scheme: "embed"}, nil
	}
	return nil, fmt.Errorf("%w: %T", ErrUnsupportedURI, fsys)
}
