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

//go:build !wasm

package boltfs

import "time"

const (
	boltFSScheme = "bolt"
	boltFSPrefix = boltFSScheme + ":"
)

const (
	// noSyncParam is the URI query parameter for bolt.Options.NoSync.
	noSyncParam = "ufs.nosync"

	// noFreelistSyncParam is the URI query parameter for
	// bolt.Options.NoFreelistSync.
	noFreelistSyncParam = "ufs.nofreelistsync"

	// noStatisticsParam is the URI query parameter for
	// bolt.Options.NoStatistics.
	noStatisticsParam = "ufs.nostatistics"

	// readOnlyParam is the URI query parameter for bolt.Options.ReadOnly.
	readOnlyParam = "ufs.readonly"

	// timeoutParam is the URI query parameter for bolt.Options.Timeout.
	timeoutParam = "ufs.timeout"

	// batchCreateOp is the op of the errors of BatchCreate, as in ufs.
	batchCreateOp = "batchcreate"

	// defaultTimeout is how long opening waits for the file lock when the
	// URI does not set timeoutParam.
	defaultTimeout = time.Minute
)
