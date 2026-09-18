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

package notifybus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

const (
	opCreate Op = iota
	opWrite
	opRemove
)

// collector accumulates delivered events under a mutex so tests can safely
// inspect them from the test goroutine while the bus delivers concurrently.
type collector struct {
	mu     sync.Mutex
	events []event
}

func (c *collector) hook(op Op, path string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, event{op: op, path: path})
}

func (c *collector) snapshot() []event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]event(nil), c.events...)
}

func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("timed out waiting for condition")
}

func TestPublishDeliversToMatchingSubscription(t *testing.T) {
	t.Parallel()
	b := New()
	var c collector
	sub := b.Subscribe(t.Context(), ".", c.hook)
	defer func() { _ = sub.Close() }()

	b.Publish(opCreate, "a.txt")
	b.Publish(opWrite, "dir/b.txt")

	waitFor(t, func() bool { return len(c.snapshot()) == 2 })
	got := c.snapshot()
	if got[0] != (event{opCreate, "a.txt"}) {
		t.Errorf("event[0] = %+v, want {opCreate a.txt}", got[0])
	}
	if got[1] != (event{opWrite, "dir/b.txt"}) {
		t.Errorf("event[1] = %+v, want {opWrite dir/b.txt}", got[1])
	}
}

func TestPublishRespectsPrefix(t *testing.T) {
	t.Parallel()
	b := New()
	var c collector
	sub := b.Subscribe(t.Context(), "dir", c.hook)
	defer func() { _ = sub.Close() }()

	b.Publish(opCreate, "dir/a.txt")
	b.Publish(opCreate, "other/b.txt")
	b.Publish(opCreate, "dirsibling/c.txt")

	// Give the non-matching events a chance to (wrongly) arrive.
	time.Sleep(20 * time.Millisecond)
	got := c.snapshot()
	if len(got) != 1 || got[0].path != "dir/a.txt" {
		t.Errorf("events = %+v, want only [{.. dir/a.txt}]", got)
	}
}

func TestRootPrefixExcludesRootItself(t *testing.T) {
	t.Parallel()
	b := New()
	var c collector
	sub := b.Subscribe(t.Context(), ".", c.hook)
	defer func() { _ = sub.Close() }()

	b.Publish(opCreate, ".")
	b.Publish(opCreate, "a.txt")

	waitFor(t, func() bool { return len(c.snapshot()) == 1 })
	if got := c.snapshot(); got[0].path != "a.txt" {
		t.Errorf("events = %+v, want only the non-root path", got)
	}
}

func TestCloseStopsDelivery(t *testing.T) {
	t.Parallel()
	b := New()
	var c collector
	sub := b.Subscribe(t.Context(), ".", c.hook)

	b.Publish(opCreate, "a.txt")
	waitFor(t, func() bool { return len(c.snapshot()) == 1 })

	if err := sub.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	b.Publish(opCreate, "b.txt")
	time.Sleep(20 * time.Millisecond)
	if got := c.snapshot(); len(got) != 1 {
		t.Errorf("events after Close() = %+v, want no new events", got)
	}
}

func TestCloseIsIdempotent(t *testing.T) {
	t.Parallel()
	b := New()
	sub := b.Subscribe(t.Context(), ".", func(Op, string) {})
	for range 3 {
		if err := sub.Close(); err != nil {
			t.Fatalf("Close() = %v, want nil", err)
		}
	}
}

func TestCloseRemovesSubscriptionFromBus(t *testing.T) {
	t.Parallel()
	b := New()
	sub := b.Subscribe(t.Context(), ".", func(Op, string) {})
	_ = sub.Close()

	b.mu.RLock()
	n := len(b.subs)
	b.mu.RUnlock()
	if n != 0 {
		t.Errorf("bus has %d subscriptions after Close(), want 0", n)
	}
}

func TestContextCancellationStopsDelivery(t *testing.T) {
	t.Parallel()
	b := New()
	ctx, cancel := context.WithCancel(t.Context())
	var c collector
	b.Subscribe(ctx, ".", c.hook)

	cancel()
	time.Sleep(20 * time.Millisecond)
	b.Publish(opCreate, "a.txt")
	time.Sleep(20 * time.Millisecond)
	if got := c.snapshot(); len(got) != 0 {
		t.Errorf("events after ctx cancel = %+v, want none", got)
	}
}

func TestCloseAllStopsEverySubscription(t *testing.T) {
	t.Parallel()
	b := New()
	var c1, c2 collector
	b.Subscribe(t.Context(), ".", c1.hook)
	b.Subscribe(t.Context(), ".", c2.hook)

	b.CloseAll()
	time.Sleep(20 * time.Millisecond)
	b.Publish(opCreate, "a.txt")
	time.Sleep(20 * time.Millisecond)

	if got := c1.snapshot(); len(got) != 0 {
		t.Errorf("c1 events after CloseAll() = %+v, want none", got)
	}
	if got := c2.snapshot(); len(got) != 0 {
		t.Errorf("c2 events after CloseAll() = %+v, want none", got)
	}
	b.mu.RLock()
	n := len(b.subs)
	b.mu.RUnlock()
	if n != 0 {
		t.Errorf("bus has %d subscriptions after CloseAll(), want 0", n)
	}
}

func TestPublishFullQueueDropsWithoutBlocking(t *testing.T) {
	t.Parallel()
	b := New()
	block := make(chan struct{})
	var unblocked atomic.Bool
	sub := b.Subscribe(t.Context(), ".", func(Op, string) {
		if !unblocked.Load() {
			<-block
		}
	})
	defer func() {
		unblocked.Store(true)
		close(block)
		_ = sub.Close()
	}()

	// The first event is picked up immediately by loop() and blocks on
	// <-block; the rest queue up. Publish must never block even once the
	// 256-deep channel is full.
	done := make(chan struct{})
	go func() {
		for range 300 {
			b.Publish(opWrite, "a.txt")
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Publish blocked on a full subscription queue")
	}
}

func TestConcurrentCloseIsSafe(t *testing.T) {
	t.Parallel()
	b := New()
	sub := b.Subscribe(t.Context(), ".", func(Op, string) {})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			_ = sub.Close()
		})
	}
	wg.Wait()
}
