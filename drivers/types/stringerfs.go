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
)

// StringerFS is a file system that can describe itself. It pairs [SimpleFS]
// with [fmt.Stringer] so that a wrapper can print the file system it
// holds instead of a bare Go value; internal helpers such as the one in package
// fsutil prefer this method when it is available.
//
// String returns a human-readable description of the file system that shows how
// it is composed — wrapper layers, mount points, and the underlying storage
// backends. It is intended for debugging and logging only: it is not
// round-trippable, so use [URIGetFS] when a file system must be reconstructed.
type StringerFS interface {
	SimpleFS
	fmt.Stringer
}
