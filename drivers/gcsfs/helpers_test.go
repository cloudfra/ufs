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

package gcsfs

import (
	"slices"
	"sync"
	"testing"
	"time"

	"github.com/cloudfra/ufs"
)

// The helpers below mirror the event collector in the ufs package's
// localfs_notify_test.go, which is not importable from this package.

type notifyEvent struct {
	op   ufs.NotifyOp
	path string
}

type eventCollector struct {
	mu     sync.Mutex
	events []notifyEvent
	ch     chan struct{}
}

func newEventCollector() *eventCollector {
	return &eventCollector{ch: make(chan struct{}, 1024)}
}

func (c *eventCollector) hook(op ufs.NotifyOp, path string) {
	c.mu.Lock()
	c.events = append(c.events, notifyEvent{op: op, path: path})
	c.mu.Unlock()
	select {
	case c.ch <- struct{}{}:
	default:
	}
}

func (c *eventCollector) waitFor(t *testing.T, deadline time.Duration, match func(notifyEvent) bool) {
	t.Helper()
	timer := time.NewTimer(deadline)
	defer timer.Stop()
	for {
		c.mu.Lock()
		found := slices.ContainsFunc(c.events, match)
		c.mu.Unlock()
		if found {
			return
		}
		select {
		case <-timer.C:
			c.mu.Lock()
			events := c.events
			c.mu.Unlock()
			t.Fatalf("timed out waiting for matching event; collected: %v", events)
			return
		case <-c.ch:
		}
	}
}

func (c *eventCollector) hasEvent(match func(notifyEvent) bool) bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return slices.ContainsFunc(c.events, match)
}

const eventDeadline = 5 * time.Second
