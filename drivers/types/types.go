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

// Package types defines the interfaces and types that ufs file systems
// implement and exchange: the single-method interfaces that make up
// ufs.WriteFS (one per method beyond ufs.ReadFS), the optional interfaces a
// file system may add, such as [Watcher] and [BatchCreateFS], and the [File]
// and [FileInfo] values they return. Code that needs only one operation, such
// as a helper that only removes files, can depend on that interface alone.
//
// It depends only on the standard library, so the ufs package and every
// driver can import it. ufs.ReadFS, ufs.WriteFS and ufs.FS, and the types that
// depend on ufs's own structs such as ufs.DeviceInfoGetter, stay in package
// ufs.
package types
