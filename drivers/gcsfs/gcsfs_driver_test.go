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

package gcsfs_test

import (
	"net/url"
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

// TestNewKeepsSubscription checks that ufs.New passes the subscription query
// parameter to gcsFS instead of treating it as a nested mount point.
func TestNewKeepsSubscription(t *testing.T) {
	// Point the client at an unreachable emulator: New makes no requests, and
	// this keeps the test from looking for real credentials.
	t.Setenv("STORAGE_EMULATOR_HOST", "127.0.0.1:1")

	const subscription = "projects/my-proj/subscriptions/my-sub"
	name := "gs://first/dir?subscription=" + url.QueryEscape(subscription)
	fsys, err := ufs.New(t.Context(), name)
	if err != nil {
		t.Fatalf("ufs.New(%q) = %v, want nil", name, err)
	}
	t.Cleanup(func() {
		if err := fsys.Close(); err != nil {
			t.Errorf("Close() = %v, want nil", err)
		}
	})

	u, err := fsys.URI()
	if err != nil {
		t.Fatalf("URI() = %v, want nil", err)
	}
	if got := u.Query().Get("subscription"); got != subscription {
		t.Errorf("URI() subscription = %q, want %q", got, subscription)
	}
}
