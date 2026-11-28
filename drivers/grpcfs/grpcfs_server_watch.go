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
	"sync"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// maxWatchBacklog is how many changes one watch may have waiting for the
// client before the server gives up on it. The hook of a [types.Watcher] must
// not block, so changes are queued while a response is on the wire, and a
// client that stops reading would otherwise make the queue grow without bound.
// At about a hundred bytes a change it bounds one watch to a few megabytes.
const maxWatchBacklog = 1 << 16

// Watch answers [types.Watcher.Watch] as a stream of batches. The served file
// system's hook only queues the change, so a burst of changes is sent as a few
// responses carrying many events each rather than one response per change, and
// a slow client never stalls the file system's own watcher. The stream ends
// when the client cancels it, when the server shuts down, or with
// ResourceExhausted when the client has fallen [maxWatchBacklog] changes
// behind.
func (s *Server) Watch(req *proto.WatchRequest, stream proto.FS_WatchServer) error {
	if !s.life.Enter() {
		return errShutdown
	}
	defer s.life.Exit()
	watcher, ok := s.inner.(types.Watcher)
	if !ok {
		return status.Error(codes.Unimplemented, "the served file system cannot watch for changes")
	}
	ctx := stream.Context()
	queue := newWatchQueue()
	closer, err := watcher.Watch(ctx, req.GetName(), queue.push)
	if err != nil {
		return toStatus(err)
	}
	defer closer.Close() //nolint:errcheck // the stream is over either way; the watch has nothing left to report.

	// The empty first response says the watch is in place, so a client that
	// makes a change after Watch returns to it knows the change is reported.
	if err := stream.Send(&proto.WatchResponse{}); err != nil {
		return err
	}
	var batch []*proto.WatchEvent
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-s.quit:
			return errShutdown
		case <-queue.ready:
		}
		var overflow bool
		batch, overflow = queue.take(batch[:0])
		if overflow {
			return status.Errorf(codes.ResourceExhausted,
				"the watch of %q fell %d changes behind and was ended; watch again and re-read what it covers",
				req.GetName(), maxWatchBacklog)
		}
		if len(batch) == 0 {
			continue
		}
		if err := stream.Send(&proto.WatchResponse{Events: batch}); err != nil {
			return err
		}
	}
}

// watchQueue holds the changes a watch has observed and not yet sent. push is
// the hook the served file system calls; take hands the queued changes to the
// sender and keeps the slice it was given back for the next round, so a busy
// watch alternates between two buffers rather than allocating one per batch.
type watchQueue struct {
	// ready has room for one signal, which is all the sender needs: it takes
	// everything queued whenever it wakes.
	ready chan struct{}

	mu       sync.Mutex
	pending  []*proto.WatchEvent
	overflow bool
}

func newWatchQueue() *watchQueue {
	return &watchQueue{ready: make(chan struct{}, 1)}
}

func (q *watchQueue) push(op types.NotifyOp, name string) {
	q.mu.Lock()
	if len(q.pending) >= maxWatchBacklog {
		q.overflow = true
	} else {
		q.pending = append(q.pending, &proto.WatchEvent{Op: toProtoNotifyOp(op), Name: name})
	}
	q.mu.Unlock()
	select {
	case q.ready <- struct{}{}:
	default:
	}
}

// take returns the queued changes and whether any were dropped, and keeps
// spare, emptied, as the buffer the next changes are queued into.
func (q *watchQueue) take(spare []*proto.WatchEvent) ([]*proto.WatchEvent, bool) {
	q.mu.Lock()
	events, overflow := q.pending, q.overflow
	clear(spare[:cap(spare)])
	q.pending = spare[:0]
	q.mu.Unlock()
	return events, overflow
}

func toProtoNotifyOp(op types.NotifyOp) proto.NotifyOp {
	switch op {
	case types.NotifyCreate:
		return proto.NotifyOp_NOTIFY_OP_CREATE
	case types.NotifyWrite:
		return proto.NotifyOp_NOTIFY_OP_WRITE
	case types.NotifyRemove:
		return proto.NotifyOp_NOTIFY_OP_REMOVE
	case types.NotifyRename:
		return proto.NotifyOp_NOTIFY_OP_RENAME
	case types.NotifyChmod:
		return proto.NotifyOp_NOTIFY_OP_CHMOD
	default:
		return proto.NotifyOp_NOTIFY_OP_UNSPECIFIED
	}
}

// fromProtoNotifyOp converts an op back, reporting false for one this side
// does not know, which a newer server could send.
func fromProtoNotifyOp(op proto.NotifyOp) (types.NotifyOp, bool) {
	switch op {
	case proto.NotifyOp_NOTIFY_OP_CREATE:
		return types.NotifyCreate, true
	case proto.NotifyOp_NOTIFY_OP_WRITE:
		return types.NotifyWrite, true
	case proto.NotifyOp_NOTIFY_OP_REMOVE:
		return types.NotifyRemove, true
	case proto.NotifyOp_NOTIFY_OP_RENAME:
		return types.NotifyRename, true
	case proto.NotifyOp_NOTIFY_OP_CHMOD:
		return types.NotifyChmod, true
	default:
		return 0, false
	}
}
