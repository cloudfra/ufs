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

package testing

import (
	"slices"
	"sync"
	"testing"
	"time"
)

// EventDeadline is the default time to wait for a change notification.
const EventDeadline = 5 * time.Second

// Event is one change notification recorded by an [EventCollector].
type Event[Op any] struct {
	Op   Op
	Path string
}

// EventCollector records change notifications delivered to [EventCollector.Hook]
// so tests can wait for and assert on them. It is safe for concurrent use.
//
// Op is the notification operation type (ufs.NotifyOp). It is a type
// parameter because this package is imported by the ufs package's own tests
// and so cannot import ufs.
type EventCollector[Op any] struct {
	mu     sync.Mutex
	events []Event[Op]
	ch     chan struct{}
}

// NewEventCollector returns an empty [EventCollector].
func NewEventCollector[Op any]() *EventCollector[Op] {
	return &EventCollector[Op]{ch: make(chan struct{}, 1024)}
}

// Hook records a notification. Its method value has the signature of a
// ufs.NotifyHook, so it can be passed directly to Watch.
func (c *EventCollector[Op]) Hook(op Op, path string) {
	c.mu.Lock()
	c.events = append(c.events, Event[Op]{Op: op, Path: path})
	c.mu.Unlock()
	select {
	case c.ch <- struct{}{}:
	default:
	}
}

// WaitFor waits until a recorded event satisfies match, failing the test via
// tb.Fatalf with every collected event if none does within deadline.
func (c *EventCollector[Op]) WaitFor(tb testing.TB, deadline time.Duration, match func(Event[Op]) bool) {
	tb.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		if c.HasEvent(match) {
			return
		}
		select {
		case <-timer.C:
			tb.Fatalf("timed out waiting for matching event; collected: %v", c.Events())
			return
		case <-c.ch:
		}
	}
}

// HasEvent reports whether any recorded event satisfies match.
func (c *EventCollector[Op]) HasEvent(match func(Event[Op]) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.events, match)
}

// Events returns a copy of the recorded events in arrival order.
func (c *EventCollector[Op]) Events() []Event[Op] {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.Clone(c.events)
}
