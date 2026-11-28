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

// Package syncfs holds the concurrency guards for code that serves a file
// system to concurrent callers, such as a host mount or a grpc server.
//
// The drivers do not make these guarantees themselves. A file system is safe
// for concurrent operations, but an open handle belongs to one goroutine at a
// time, as [fs.File] promises nothing more, and closing a file system that
// is still in use is the caller's to avoid. Code that hands one handle to
// several goroutines wraps it with [NewFile], and code that serves a file
// system until it is stopped guards its operations with a [Lifetime], so the
// cost is paid once at the boundary rather than by every driver.
//
// The implementation is split by the type each method belongs to, one file per
// type: syncfs_file.go holds [File] and syncfs_lifetime.go holds [Lifetime].
package syncfs
