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

package grpcfs

import (
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
)

// TestWatchQueueBatchesAndBounds checks that queued changes come out in order
// as one batch, that the buffer handed back is reused, and that a backlog past
// the bound is reported rather than grown.
func TestWatchQueueBatchesAndBounds(t *testing.T) {
	t.Parallel()
	q := newWatchQueue()
	q.push(types.NotifyCreate, "a")
	q.push(types.NotifyWrite, "b")
	batch, overflow := q.take(nil)
	if overflow || len(batch) != 2 || batch[0].GetName() != "a" || batch[1].GetName() != "b" {
		t.Fatalf("take = %v, %v, want a then b and no overflow", batch, overflow)
	}
	q.push(types.NotifyRemove, "c")
	next, _ := q.take(batch[:0])
	if len(next) != 1 || next[0].GetName() != "c" {
		t.Fatalf("second take = %v, want c", next)
	}

	for range maxWatchBacklog + 1 {
		q.push(types.NotifyWrite, "x")
	}
	full, overflow := q.take(nil)
	if !overflow || len(full) != maxWatchBacklog {
		t.Errorf("after %d changes take = %d events, overflow %v, want %d and overflow", maxWatchBacklog+1, len(full), overflow, maxWatchBacklog)
	}
}

// TestNotifyOpRoundTrip checks that every op survives the wire.
func TestNotifyOpRoundTrip(t *testing.T) {
	t.Parallel()
	for _, op := range []types.NotifyOp{types.NotifyCreate, types.NotifyWrite, types.NotifyRemove, types.NotifyRename, types.NotifyChmod} {
		if got, ok := fromProtoNotifyOp(toProtoNotifyOp(op)); !ok || got != op {
			t.Errorf("op %d came back as %d, %v", op, got, ok)
		}
	}
}
