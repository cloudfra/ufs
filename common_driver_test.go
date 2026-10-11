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

package ufs_test

import (
	"context"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/core"
)

// init registers the driver for the URI "test-tempmount:", which opens an
// empty tempMountFS. The tests of package ufs cannot import drivers/core
// themselves, because it imports ufs.
func init() {
	const name = "test-tempmount"
	ufs.Register(ufs.NewDriver(name, func(ctx context.Context, _ string) (ufs.WriteFS, error) {
		return core.NewTempMountFS(ctx, "test://", func(string) error { return nil })
	}, func(uri string) bool {
		return uri == name+":"
	}, 1, false, true))
}
