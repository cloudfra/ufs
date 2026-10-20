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

// Package polyfills provides unoptimized, best-effort standins to emulate
// functionality that the underlying file system does not have. Each entry point
// takes a file system of arbitrary capability, probes it for the optional
// interfaces it can build on, and performs the operation with the primitives
// that are present. The result matches what the file system's own implementation
// would do, so a caller keeps a single code path whether or not the file system
// supports the operation itself.
//
// A polyfill only emulates operations on top of interfaces the file system
// already implements; when it finds nothing usable it returns an unimplemented
// error rather than guessing. The standin costs extra round trips (a stat per
// level) and is never atomic, so a file system that implements the operation
// natively is always preferred.
package polyfills
