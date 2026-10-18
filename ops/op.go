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

// Package ops provides file-system operations that work across any standard
// file system: [Copy], [Rsync], [List], [ListFiles], [ForEachFilename],
// [ForEachFileInfo], [Walk], [Remove], [RemoveAll], [Rename] and
// [RenameByCopy]. Where the source or target implements an optional interface
// from the types package, the matching native method is used; otherwise the
// operation falls back to the generic file-system methods.
package ops
