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
	"context"
	"errors"
	"io"
	"io/fs"
	"log/slog"
	"sync"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/ufserrors"
	"github.com/cloudfra/ufs/proto"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// Watch reports the changes below the directory name through hook, over one
// Watch stream. It returns once the server says the watch is in place, so a
// change made after Watch returns is reported, and a name the served file
// system refuses is refused here. The server batches changes that arrive while
// a response is on the wire, and hook is called for each of them in order from
// one goroutine, as [types.Watcher] asks.
//
// Watching stops when ctx ends, when the returned closer is closed, or when
// this file system is. A stream that the server ends on its own — it shut down,
// or this client fell too far behind (codes.ResourceExhausted) — stops the
// watch as well, and is logged, since the hook has no way to be told.
func (fsys *grpcFS) Watch(ctx context.Context, name string, hook types.NotifyHook) (io.Closer, error) {
	if err := fsys.closedErr("watch", name); err != nil {
		return nil, err
	}
	if !fs.ValidPath(name) {
		return nil, ufserrors.NewPathError("watch", name, fs.ErrInvalid)
	}
	watchCtx, cancel := context.WithCancel(ctx)
	// Closing the file system ends its watches too, which a stream bound only
	// to the caller's context would outlive.
	stopWithFS := context.AfterFunc(fsys.ctx, cancel)
	stream, err := fsys.client.Watch(watchCtx, &proto.WatchRequest{Name: name})
	if err == nil {
		_, err = stream.Recv()
	}
	if err != nil {
		stopWithFS()
		cancel()
		return nil, pathError("watch", name, err)
	}

	w := &grpcWatch{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(w.done)
		defer stopWithFS()
		for {
			resp, err := stream.Recv()
			if err != nil {
				if watchCtx.Err() == nil && !errors.Is(err, io.EOF) && status.Code(err) != codes.Canceled {
					slog.Warn("grpcfs: watch ended by the server", "name", name, "error", err)
				}
				return
			}
			for _, event := range resp.GetEvents() {
				if op, ok := fromProtoNotifyOp(event.GetOp()); ok {
					hook(op, event.GetName())
				}
			}
		}
	}()
	return w, nil
}

// grpcWatch is the closer of one watch: closing it cancels the stream and
// waits until the goroutine calling the hook has returned, so no hook call
// happens after Close does.
type grpcWatch struct {
	cancel context.CancelFunc
	done   chan struct{}
	once   sync.Once
}

func (w *grpcWatch) Close() error {
	w.once.Do(w.cancel)
	<-w.done
	return nil
}
