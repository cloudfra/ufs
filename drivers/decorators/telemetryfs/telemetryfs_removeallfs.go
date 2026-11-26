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

package telemetryfs

// RemoveAll is the [types.RemoveAllFS] half of [types.WriteFS], reported
// around a delegation to the wrapped file system.

func (fsys *telemetryFS) RemoveAll(name string) error {
	span, start := fsys.begin(opRemoveAll)
	err := fsys.inner.RemoveAll(name)
	fsys.end(opRemoveAll, name, span, start, err)
	return err
}
