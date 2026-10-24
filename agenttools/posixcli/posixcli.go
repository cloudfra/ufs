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

// Package posixcli is an experimental agent tool for supporting basic posix tools like ls, rm, touch, etc for UFS mounts.
// The goal is that an agent can embed and use UFS without FUSE mounts for a safe contained file system interaction while using tools that the model is familiar with.
package posixcli

import "context"

// Run is a stub that an AI agent can call to perform an action against the virtual file system as if it had access to the linux terminal.
// Instead of hitting the terminal directly the command is translated into ufs API calls.
// We will support basic tool calls. Anything fancy such as using awk etc as part of the call will not be supported and should return an error saying that the terminal environment is emulated and does not support clever CLI calls.
//
// Example: Run(context.Background(), "ls") returns directory listing.
func Run(ctx context.Context, cmd string) (string, error) {
	return "", nil
}
