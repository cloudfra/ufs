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

//go:build aix || wasip1

package gitfs

import (
	"context"
	"fmt"

	"github.com/cloudfra/ufs"
)

// The driver is registered on aix and wasip1 too, but isGitFSUri never matches
// there, so ufs.New treats git URIs as it would with no gitfs driver.
func init() {
	ufs.Register(ufs.NewDriver("git", New, isGitFSUri, 1, true, true))
}

// New reports that gitfs is unavailable on GOOS=aix and GOOS=wasip1, where
// go-git does not build.
func New(_ context.Context, name string) (ufs.FS, error) {
	return nil, fmt.Errorf("cannot mount %q, gitfs is not supported on this operating system", name)
}

// isGitFSUri returns false: gitfs is not supported on this platform.
func isGitFSUri(string) bool {
	return false
}
