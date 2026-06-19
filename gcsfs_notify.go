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

package ufs

import (
	"context"
	"fmt"
	"io"
	"io/fs"
	"path"
	"strings"
	"sync"

	"cloud.google.com/go/pubsub/v2"
	"cloud.google.com/go/storage"
)

var _ Watcher = (*gcsFS)(nil)

// Watch implements [Watcher] for GCS file systems using Cloud Pub/Sub
// notifications. The gcsFS must be constructed with a subscription query
// parameter (e.g. gs://bucket/prefix?subscription=projects/P/subscriptions/S)
// so that Watch knows which Pub/Sub subscription to pull from.
//
// GCS bucket notifications must be configured separately (via gsutil or the
// GCS API) to publish object-change events to the Pub/Sub topic that the
// subscription is attached to.
func (fsys *gcsFS) Watch(ctx context.Context, name string, hook NotifyHook) (io.Closer, error) {
	if err := validPath("watch", name); err != nil {
		return nil, err
	}
	if fsys.subscription == "" {
		return nil, pathError("watch", name, fmt.Errorf("gcsFS has no pubsub subscription configured; pass subscription=projects/PROJECT/subscriptions/NAME as a query parameter: %w", fs.ErrInvalid))
	}

	parts := strings.SplitN(strings.TrimPrefix(fsys.subscription, "projects/"), "/subscriptions/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return nil, pathError("watch", name, fmt.Errorf("invalid subscription %q; expected projects/PROJECT/subscriptions/NAME: %w", fsys.subscription, fs.ErrInvalid))
	}
	projectID := parts[0]

	psClient, err := pubsub.NewClient(ctx, projectID)
	if err != nil {
		return nil, pathError("watch", name, err)
	}

	var watchPrefix string
	if name == cwdPath {
		watchPrefix = fsys.baseDir
	} else {
		watchPrefix = path.Join(fsys.baseDir, name)
	}
	if watchPrefix != "" && !strings.HasSuffix(watchPrefix, "/") {
		watchPrefix += "/"
	}

	ctx, cancel := context.WithCancel(ctx)
	gw := &gcsWatcher{
		fsys:        fsys,
		psClient:    psClient,
		hook:        hook,
		cancel:      cancel,
		watchPrefix: watchPrefix,
		done:        make(chan struct{}),
	}

	sub := psClient.Subscriber(fsys.subscription)
	gw.wg.Add(1)
	go gw.loop(ctx, sub)

	return gw, nil
}

type gcsWatcher struct {
	fsys        *gcsFS
	psClient    *pubsub.Client
	hook        NotifyHook
	cancel      context.CancelFunc
	watchPrefix string

	closeOnce sync.Once
	wg        sync.WaitGroup
	done      chan struct{}
}

func (gw *gcsWatcher) Close() error {
	var err error
	gw.closeOnce.Do(func() {
		gw.cancel()
		gw.wg.Wait()
		err = gw.psClient.Close()
		close(gw.done)
	})
	return err
}

func (gw *gcsWatcher) loop(ctx context.Context, sub *pubsub.Subscriber) {
	defer gw.wg.Done()
	// Receive blocks until ctx is canceled.
	_ = sub.Receive(ctx, func(_ context.Context, msg *pubsub.Message) {
		defer msg.Ack()
		gw.handleMessage(msg)
	})
}

func (gw *gcsWatcher) handleMessage(msg *pubsub.Message) {
	eventType := msg.Attributes["eventType"]
	objectID := msg.Attributes["objectId"]
	bucket := msg.Attributes["bucketId"]

	if bucket != gw.fsys.bucket {
		return
	}

	if gw.watchPrefix != "" && !strings.HasPrefix(objectID, gw.watchPrefix) {
		return
	}

	var rel string
	if gw.fsys.baseDir == "" {
		rel = objectID
	} else {
		base := gw.fsys.baseDir
		if !strings.HasSuffix(base, "/") {
			base += "/"
		}
		var ok bool
		rel, ok = strings.CutPrefix(objectID, base)
		if !ok {
			return
		}
	}

	if rel == "" || !fs.ValidPath(rel) {
		return
	}

	op, valid := convertGCSEventType(eventType)
	if !valid {
		return
	}

	gw.hook(op, rel)
}

func convertGCSEventType(eventType string) (NotifyOp, bool) {
	switch eventType {
	case storage.ObjectFinalizeEvent:
		return NotifyCreate, true
	case storage.ObjectDeleteEvent:
		return NotifyRemove, true
	case storage.ObjectMetadataUpdateEvent:
		return NotifyChmod, true
	case storage.ObjectArchiveEvent:
		return NotifyRemove, true
	default:
		return 0, false
	}
}
