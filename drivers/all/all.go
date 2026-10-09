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

// Package all registers every ufs driver and decorator that lives outside the
// base package, so that a single blank import enables all of them:
//
//	import _ "github.com/cloudfra/ufs/drivers/all"
//
// It registers these drivers, which [github.com/cloudfra/ufs.New] selects by
// URI:
//
//   - null: and angry: from [github.com/cloudfra/ufs/drivers/core]
//   - bolt: from [github.com/cloudfra/ufs/drivers/boltfs]
//   - gs:// from [github.com/cloudfra/ufs/drivers/gcsfs]
//   - URIs ending in .git from [github.com/cloudfra/ufs/drivers/gitfs]
//
// and these decorators, which a mount selects by name in its options:
//
//   - readOnly from [github.com/cloudfra/ufs/drivers/decorators/readonlyfs],
//     also needed by the fstab ro option and the implicit read-only root
//   - fault from [github.com/cloudfra/ufs/drivers/decorators/faultfs]
//
// The memory:, file:// and archive:// file systems are part of the
// base package and need no import. They are moving to
// [github.com/cloudfra/ufs/drivers/core], which is imported here already so
// that programs keep them when they do. Packages that register nothing, such as
// [github.com/cloudfra/ufs/drivers/embedfs], are not included; call their
// constructors directly.
//
// Importing this package links the dependencies of every driver (the Google
// Cloud Storage client, go-git and bbolt) into the binary. Import the
// individual packages instead to keep a binary small. A driver that is
// unavailable on the target platform is still registered there but fails to
// open, see its package documentation.
package all

import (
	_ "github.com/cloudfra/ufs/drivers/boltfs"                // registers bolt:
	_ "github.com/cloudfra/ufs/drivers/core"                  // registers null: and angry:
	_ "github.com/cloudfra/ufs/drivers/decorators/faultfs"    // registers the fault mount option
	_ "github.com/cloudfra/ufs/drivers/decorators/readonlyfs" // registers the readOnly mount option
	_ "github.com/cloudfra/ufs/drivers/gcsfs"                 // registers gs://
	_ "github.com/cloudfra/ufs/drivers/gitfs"                 // registers git repository URIs
)
