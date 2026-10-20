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

// Package types defines the core interfaces and value types of the ufs
// file-system model: the read-only ([ReadFS]) and read-write ([WriteFS])
// file-system interfaces, the optional capability interfaces a backend can
// implement (for example [BatchCreateFS], [CopyFileFS] and [Watcher]), the
// file-handle and file-information types, and the device information a file
// system reports. The public constructors in the ufs package, the individual
// drivers, and the ops helpers all program against these types, so a driver
// that implements them composes with the rest of the project.
package types
