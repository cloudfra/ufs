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

import "io/fs"

// readOnlyFile is a File whose Write and WriteString always fail. It keeps a
// backend's read-write handle, such as the one memFS returns from Open, from
// being used to modify a file that was opened for reading.
type readOnlyFile struct {
	File
}

func (f *readOnlyFile) Write([]byte) (int, error) {
	return 0, fs.ErrInvalid
}

func (f *readOnlyFile) WriteString(string) (int, error) {
	return 0, fs.ErrInvalid
}
