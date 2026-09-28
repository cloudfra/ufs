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

// github.com/fsouza/fake-gcs-server depends on github.com/pkg/xattr, which
// does not build on Plan 9.
//go:build !plan9

package gcsfs_test

import (
	"testing"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/drivers/gcsfs"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
)

func TestGCSFSDriver(t *testing.T) {
	ufsdriversTesting.WriteFSWithBuckets(t, func(t *testing.T) ufs.WriteFS {
		ctx := t.Context()
		name := "gs://first"
		client := gcsfs.CreateFakeGcsBackend(t)
		fsys, err := gcsfs.NewWithClient(ctx, client, name)
		if err != nil {
			t.Fatalf("cannot create gcsFS %q, %s", name, err)
		}
		return fsys
	})
}
