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

package notify

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	ufsTesting "github.com/cloudfra/ufs/testing"
)

const waitTimeout = 2 * time.Second

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

func TestPublishDeliversToMatchingSubscription(t *testing.T) {
	t.Parallel()
	b := New()
	var c collector
	sub := b.Subscribe(t.Context(), ".", c.hook)
	defer ufsTesting.ValidateClose(t, sub)()

	b.Publish(opCreate, "a.txt")
	b.Publish(opWrite, "dir/b.txt")

	ufsTesting.WaitFor(t, waitTimeout, func() bool { return len(c.snapshot()) == 2 })
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
	defer ufsTesting.ValidateClose(t, sub)()

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
	defer ufsTesting.ValidateClose(t, sub)()

	b.Publish(opCreate, ".")
	b.Publish(opCreate, "a.txt")

	ufsTesting.WaitFor(t, waitTimeout, func() bool { return len(c.snapshot()) == 1 })
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
	ufsTesting.WaitFor(t, waitTimeout, func() bool { return len(c.snapshot()) == 1 })

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
	ufsTesting.ValidateClose(t, sub)()

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

func TestBusCloseStopsEverySubscription(t *testing.T) {
	t.Parallel()
	b := New()
	var c1, c2 collector
	b.Subscribe(t.Context(), ".", c1.hook)
	b.Subscribe(t.Context(), ".", c2.hook)

	if err := b.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	time.Sleep(20 * time.Millisecond)
	b.Publish(opCreate, "a.txt")
	time.Sleep(20 * time.Millisecond)

	if got := c1.snapshot(); len(got) != 0 {
		t.Errorf("c1 events after Close() = %+v, want none", got)
	}
	if got := c2.snapshot(); len(got) != 0 {
		t.Errorf("c2 events after Close() = %+v, want none", got)
	}
	b.mu.RLock()
	n := len(b.subs)
	b.mu.RUnlock()
	if n != 0 {
		t.Errorf("bus has %d subscriptions after Close(), want 0", n)
	}
}

// TestPublishDropsWhenQueueFull verifies that while a subscription's hook is
// busy, exactly QueueSize further events are buffered, every event after
// that is dropped, and Publish never blocks.
func TestPublishDropsWhenQueueFull(t *testing.T) {
	t.Parallel()
	b := New()
	var c collector
	started := make(chan struct{})
	release := make(chan struct{})
	first := true // only touched by the subscription's delivery goroutine
	sub := b.Subscribe(t.Context(), ".", func(op Op, path string) {
		c.hook(op, path)
		if first {
			first = false
			close(started)
			<-release
		}
	})
	defer ufsTesting.ValidateClose(t, sub)()
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	// Deferred after the Close above so it runs first; Close waits for the
	// delivery goroutine, which is parked in the hook until released.
	defer unblock()

	// Park the delivery goroutine in the hook so the queue starts empty.
	b.Publish(opWrite, "held")
	select {
	case <-started:
	case <-time.After(waitTimeout):
		t.Fatal("first event was never delivered")
	}

	const dropped = 10
	done := make(chan struct{})
	go func() {
		for i := range QueueSize + dropped {
			b.Publish(opWrite, fmt.Sprintf("q%d", i))
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(waitTimeout):
		t.Fatal("Publish blocked on a full subscription queue")
	}

	unblock()
	ufsTesting.WaitFor(t, waitTimeout, func() bool { return len(c.snapshot()) >= 1+QueueSize })
	// Give any wrongly-queued extra events a chance to arrive.
	time.Sleep(20 * time.Millisecond)

	got := c.snapshot()
	if len(got) != 1+QueueSize {
		t.Fatalf("delivered %d events, want %d (1 held + %d queued; %d dropped)", len(got), 1+QueueSize, QueueSize, dropped)
	}
	for i, ev := range got[1:] {
		if want := fmt.Sprintf("q%d", i); ev.path != want {
			t.Fatalf("event[%d].path = %q, want %q (the first %d queued in order)", i+1, ev.path, want, QueueSize)
		}
	}
}

func TestConcurrentCloseIsSafe(t *testing.T) {
	t.Parallel()
	b := New()
	sub := b.Subscribe(t.Context(), ".", func(Op, string) {})

	var wg sync.WaitGroup
	for range 10 {
		wg.Go(func() {
			ufsTesting.ValidateClose(t, sub)()
		})
	}
	wg.Wait()
}
