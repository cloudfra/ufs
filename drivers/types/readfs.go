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

package types

import (
	"fmt"
	"io"
	"io/fs"
)

// ReadFS is a read-only file system. In addition to the standard [fs.FS]
// interface it requires [io.Closer] for lifecycle management, the four
// extended read interfaces from the standard library, and [fmt.Stringer] so
// that every implementation can describe itself. Callers should always Close
// a ReadFS when they are done with it.
type ReadFS interface {
	fs.FS
	io.Closer
	fs.ReadDirFS
	fs.ReadFileFS
	fs.ReadLinkFS
	fs.StatFS

	fmt.Stringer

	DeviceInfoGet

	// TODO: Implement ExternalPathGet

	URIGet

	// String returns a human-readable description of the file system that
	// shows how it is composed — wrapper layers, mount points, and the
	// underlying storage backends. It is intended for debugging and logging.
	String() string
}
