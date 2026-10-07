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
//   - angry: from [github.com/cloudfra/ufs/drivers/angryfs]
//   - archive:, local paths that name an archive, and the name.d archive
//     directories from [github.com/cloudfra/ufs/drivers/archivefs]
//   - bolt: from [github.com/cloudfra/ufs/drivers/boltfs]
//   - gs:// from [github.com/cloudfra/ufs/drivers/gcsfs]
//   - URIs ending in .git from [github.com/cloudfra/ufs/drivers/gitfs]
//   - archives at http: and https: URLs from
//     [github.com/cloudfra/ufs/drivers/httparchivefs]
//   - file: and bare paths from [github.com/cloudfra/ufs/drivers/localfs]
//   - memory: from [github.com/cloudfra/ufs/drivers/memfs]
//   - null: from [github.com/cloudfra/ufs/drivers/nullfs]
//
// and these decorators, which a mount selects by name in its options:
//
//   - readOnly from [github.com/cloudfra/ufs/drivers/decorators/readonlyfs],
//     also needed by the fstab ro option and the implicit read-only root
//   - fault from [github.com/cloudfra/ufs/drivers/decorators/faultfs]
//
// The base package holds no file system of its own, so without an import
// [github.com/cloudfra/ufs.New] opens nothing. Packages that register nothing,
// such as [github.com/cloudfra/ufs/drivers/embedfs] and
// [github.com/cloudfra/ufs/drivers/tempmountfs], are not included; call their
// constructors directly.
//
// Importing this package links the dependencies of every driver (the Google
// Cloud Storage client, go-git and bbolt) into the binary. Import the
// individual packages instead to keep a binary small. A driver that is
// unavailable on the target platform is still registered there but fails to
// open, see its package documentation.
package all

import (
	_ "github.com/cloudfra/ufs/drivers/angryfs"               // registers angry:
	_ "github.com/cloudfra/ufs/drivers/archivefs"             // registers archive: and name.d archive directories
	_ "github.com/cloudfra/ufs/drivers/boltfs"                // registers bolt:
	_ "github.com/cloudfra/ufs/drivers/decorators/faultfs"    // registers the fault mount option
	_ "github.com/cloudfra/ufs/drivers/decorators/readonlyfs" // registers the readOnly mount option
	_ "github.com/cloudfra/ufs/drivers/gcsfs"                 // registers gs://
	_ "github.com/cloudfra/ufs/drivers/gitfs"                 // registers git repository URIs
	_ "github.com/cloudfra/ufs/drivers/httparchivefs"         // registers archives at http: and https: URLs
	_ "github.com/cloudfra/ufs/drivers/localfs"               // registers file: and bare paths
	_ "github.com/cloudfra/ufs/drivers/memfs"                 // registers memory:
	_ "github.com/cloudfra/ufs/drivers/nullfs"                // registers null:
)
