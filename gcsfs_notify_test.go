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
	"testing"

	"cloud.google.com/go/pubsub/v2"
	pb "cloud.google.com/go/pubsub/v2/apiv1/pubsubpb"
	"cloud.google.com/go/pubsub/v2/pstest"
	"cloud.google.com/go/storage"
	"google.golang.org/api/option"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
)

func TestConvertGCSEventType(t *testing.T) {
	tests := []struct {
		eventType string
		wantOp    NotifyOp
		wantValid bool
	}{
		{storage.ObjectFinalizeEvent, NotifyCreate, true},
		{storage.ObjectDeleteEvent, NotifyRemove, true},
		{storage.ObjectMetadataUpdateEvent, NotifyChmod, true},
		{storage.ObjectArchiveEvent, NotifyRemove, true},
		{"UNKNOWN_EVENT", 0, false},
		{"", 0, false},
	}

	for _, tt := range tests {
		t.Run(tt.eventType, func(t *testing.T) {
			op, valid := convertGCSEventType(tt.eventType)
			if valid != tt.wantValid {
				t.Fatalf("convertGCSEventType(%q) valid = %v, want %v", tt.eventType, valid, tt.wantValid)
			}
			if valid && op != tt.wantOp {
				t.Fatalf("convertGCSEventType(%q) op = %d, want %d", tt.eventType, op, tt.wantOp)
			}
		})
	}
}

func TestGCSWatcherHandleMessage(t *testing.T) {
	gw := &gcsWatcher{
		fsys: &gcsFS{
			bucket:  "my-bucket",
			baseDir: "data/prefix",
		},
		watchPrefix: "data/prefix/",
	}

	type result struct {
		op   NotifyOp
		path string
	}

	tests := []struct {
		name       string
		attrs      map[string]string
		wantResult *result
	}{
		{
			name: "create_file",
			attrs: map[string]string{
				"eventType": storage.ObjectFinalizeEvent,
				"objectId":  "data/prefix/file.txt",
				"bucketId":  "my-bucket",
			},
			wantResult: &result{op: NotifyCreate, path: "file.txt"},
		},
		{
			name: "delete_nested",
			attrs: map[string]string{
				"eventType": storage.ObjectDeleteEvent,
				"objectId":  "data/prefix/sub/dir/file.txt",
				"bucketId":  "my-bucket",
			},
			wantResult: &result{op: NotifyRemove, path: "sub/dir/file.txt"},
		},
		{
			name: "wrong_bucket",
			attrs: map[string]string{
				"eventType": storage.ObjectFinalizeEvent,
				"objectId":  "data/prefix/file.txt",
				"bucketId":  "other-bucket",
			},
			wantResult: nil,
		},
		{
			name: "outside_prefix",
			attrs: map[string]string{
				"eventType": storage.ObjectFinalizeEvent,
				"objectId":  "other/path/file.txt",
				"bucketId":  "my-bucket",
			},
			wantResult: nil,
		},
		{
			name: "unknown_event",
			attrs: map[string]string{
				"eventType": "UNKNOWN",
				"objectId":  "data/prefix/file.txt",
				"bucketId":  "my-bucket",
			},
			wantResult: nil,
		},
		{
			name: "metadata_update",
			attrs: map[string]string{
				"eventType": storage.ObjectMetadataUpdateEvent,
				"objectId":  "data/prefix/meta.txt",
				"bucketId":  "my-bucket",
			},
			wantResult: &result{op: NotifyChmod, path: "meta.txt"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var got *result
			gw.hook = func(op NotifyOp, path string) {
				got = &result{op: op, path: path}
			}

			msg := &pubsub.Message{
				Attributes: tt.attrs,
			}
			gw.handleMessage(msg)

			if tt.wantResult == nil {
				if got != nil {
					t.Fatalf("expected no hook call, got op=%d path=%q", got.op, got.path)
				}
				return
			}
			if got == nil {
				t.Fatal("expected hook call, got none")
			}
			if got.op != tt.wantResult.op {
				t.Fatalf("op = %d, want %d", got.op, tt.wantResult.op)
			}
			if got.path != tt.wantResult.path {
				t.Fatalf("path = %q, want %q", got.path, tt.wantResult.path)
			}
		})
	}
}

func TestGCSWatcherHandleMessageNoBaseDir(t *testing.T) {
	gw := &gcsWatcher{
		fsys: &gcsFS{
			bucket:  "my-bucket",
			baseDir: "",
		},
		watchPrefix: "",
	}

	var got struct {
		op   NotifyOp
		path string
	}
	gw.hook = func(op NotifyOp, path string) {
		got.op = op
		got.path = path
	}

	msg := &pubsub.Message{
		Attributes: map[string]string{
			"eventType": storage.ObjectFinalizeEvent,
			"objectId":  "top-level.txt",
			"bucketId":  "my-bucket",
		},
	}
	gw.handleMessage(msg)

	if got.path != "top-level.txt" {
		t.Fatalf("path = %q, want %q", got.path, "top-level.txt")
	}
}

func TestGCSWatchNoSubscription(t *testing.T) {
	fsys := &gcsFS{
		bucket:  "my-bucket",
		baseDir: "",
	}

	_, err := fsys.Watch(t.Context(), ".", func(NotifyOp, string) {})
	if err == nil {
		t.Fatal("expected error when no subscription configured")
	}
}

func TestGCSWatchInvalidSubscription(t *testing.T) {
	fsys := &gcsFS{
		bucket:       "my-bucket",
		baseDir:      "",
		subscription: "bad-format",
	}

	_, err := fsys.Watch(t.Context(), ".", func(NotifyOp, string) {})
	if err == nil {
		t.Fatal("expected error for invalid subscription format")
	}
}

func TestGCSSubscriptionFromURI(t *testing.T) {
	fsys := &gcsFS{
		bucket:       "test-bucket",
		baseDir:      "some/prefix",
		subscription: "projects/my-proj/subscriptions/my-sub",
	}

	u := fsys.URI()
	if got := u.Query().Get("subscription"); got != "projects/my-proj/subscriptions/my-sub" {
		t.Fatalf("URI subscription = %q, want %q", got, "projects/my-proj/subscriptions/my-sub")
	}

	bucket, dir, err := parseGCSPath("gs://test-bucket/some/prefix?subscription=projects/my-proj/subscriptions/my-sub", "test")
	if err != nil {
		t.Fatal(err)
	}
	if bucket != "test-bucket" {
		t.Fatalf("bucket = %q, want %q", bucket, "test-bucket")
	}
	if dir != "some/prefix" {
		t.Fatalf("dir = %q, want %q", dir, "some/prefix")
	}
}

const (
	testProject      = "test-project"
	testTopic        = "projects/test-project/topics/gcs-events"
	testSubscription = "projects/test-project/subscriptions/gcs-events-sub"
)

func newPstestGCSFS(tb testing.TB, bucket, baseDir string) (*gcsFS, *pstest.Server) {
	tb.Helper()
	srv := pstest.NewServer()
	tb.Cleanup(func() { srv.Close() })

	conn, err := grpc.NewClient(srv.Addr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		tb.Fatal(err)
	}
	tb.Cleanup(func() {
		if err := conn.Close(); err != nil {
			tb.Logf("failed to close gRPC connection: %v", err)
		}
	})

	ctx := tb.Context()
	psClient, err := pubsub.NewClient(ctx, testProject, option.WithGRPCConn(conn))
	if err != nil {
		tb.Fatal(err)
	}
	// Keep the admin client open so the shared gRPC connection stays alive.
	tb.Cleanup(func() { psClient.Close() })

	_, err = psClient.TopicAdminClient.CreateTopic(ctx, &pb.Topic{
		Name: testTopic,
	})
	if err != nil {
		tb.Fatal(err)
	}

	_, err = psClient.SubscriptionAdminClient.CreateSubscription(ctx, &pb.Subscription{
		Name:  testSubscription,
		Topic: testTopic,
	})
	if err != nil {
		tb.Fatal(err)
	}

	fsys := &gcsFS{
		bucket:       bucket,
		baseDir:      baseDir,
		subscription: testSubscription,
		psClientOpts: []option.ClientOption{
			option.WithGRPCConn(conn),
		},
	}
	return fsys, srv
}

func TestGCSWatchPubSub(t *testing.T) {
	fsys, srv := newPstestGCSFS(t, "my-bucket", "data")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	srv.Publish(testTopic, nil, map[string]string{
		"eventType": storage.ObjectFinalizeEvent,
		"objectId":  "data/hello.txt",
		"bucketId":  "my-bucket",
	})

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "hello.txt"
	})
}

func TestGCSWatchPubSubDelete(t *testing.T) {
	fsys, srv := newPstestGCSFS(t, "my-bucket", "")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	srv.Publish(testTopic, nil, map[string]string{
		"eventType": storage.ObjectDeleteEvent,
		"objectId":  "removed.txt",
		"bucketId":  "my-bucket",
	})

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyRemove && ev.path == "removed.txt"
	})
}

func TestGCSWatchPubSubFiltersBucket(t *testing.T) {
	fsys, srv := newPstestGCSFS(t, "my-bucket", "")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	// Wrong bucket — should be filtered out.
	srv.Publish(testTopic, nil, map[string]string{
		"eventType": storage.ObjectFinalizeEvent,
		"objectId":  "file.txt",
		"bucketId":  "wrong-bucket",
	})

	// Right bucket — should arrive.
	srv.Publish(testTopic, nil, map[string]string{
		"eventType": storage.ObjectFinalizeEvent,
		"objectId":  "expected.txt",
		"bucketId":  "my-bucket",
	})

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "expected.txt"
	})

	if ec.hasEvent(func(ev notifyEvent) bool {
		return ev.path == "file.txt"
	}) {
		t.Error("received event for wrong bucket")
	}
}

func TestGCSWatchPubSubCloseStopsDelivery(t *testing.T) {
	fsys, srv := newPstestGCSFS(t, "my-bucket", "")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}

	// Verify the watcher works first.
	srv.Publish(testTopic, nil, map[string]string{
		"eventType": storage.ObjectFinalizeEvent,
		"objectId":  "before.txt",
		"bucketId":  "my-bucket",
	})
	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.path == "before.txt"
	})

	if err := closer.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestGCSWatchPubSubNestedPath(t *testing.T) {
	fsys, srv := newPstestGCSFS(t, "my-bucket", "root/dir")

	ec := newEventCollector()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()

	closer, err := fsys.Watch(ctx, ".", ec.hook)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = closer.Close() }()

	srv.Publish(testTopic, nil, map[string]string{
		"eventType": storage.ObjectFinalizeEvent,
		"objectId":  "root/dir/sub/file.txt",
		"bucketId":  "my-bucket",
	})

	ec.waitFor(t, eventDeadline, func(ev notifyEvent) bool {
		return ev.op == NotifyCreate && ev.path == "sub/file.txt"
	})
}
