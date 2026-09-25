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

// Package notify provides a prefix-matching change-event broadcaster for
// in-process file system backends. A backend holds one [Bus], calls
// [Bus.Publish] whenever it mutates a path, and hands out a [Subscription]
// per [Bus.Subscribe] call to satisfy its own Watch method.
package notify

import (
	"context"
	"io"
	"strings"
	"sync"

	"github.com/cloudfra/ufs/internal/pathutil"
)

var (
	_ io.Closer = (*Bus)(nil)
	_ io.Closer = (*Subscription)(nil)
)

// QueueSize is the number of undelivered events a [Subscription] buffers.
// While its hook is busy and the queue is full, further events for that
// subscription are dropped rather than blocking [Bus.Publish].
const QueueSize = 256

// Op describes the kind of change observed on a path. It mirrors ufs.NotifyOp
// (a plain int conversion at the caller) so this package has no dependency on
// ufs itself.
type Op int

// Hook is invoked for each change delivered to a [Subscription].
type Hook func(op Op, path string)

type event struct {
	op   Op
	path string
}

// Subscription is one watcher registered with a [Bus]. Its Close is
// idempotent and safe to call from any goroutine.
type Subscription struct {
	bus    *Bus
	prefix string
	hook   Hook
	cancel context.CancelFunc

	closeOnce sync.Once
	done      chan struct{}
	events    chan event
}

// Close stops delivery to this subscription and waits for its background
// goroutine to exit before removing it from the bus.
func (s *Subscription) Close() error {
	s.closeOnce.Do(func() {
		s.cancel()
		<-s.done
		s.bus.remove(s)
	})
	return nil
}

func (s *Subscription) loop(ctx context.Context) {
	defer close(s.done)
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-s.events:
			if !ok {
				return
			}
			s.hook(ev.op, ev.path)
		}
	}
}

// matches reports whether path falls under this subscription's prefix.
func (s *Subscription) matches(path string) bool {
	if s.prefix == pathutil.CwdPath {
		return path != pathutil.CwdPath
	}
	return path == s.prefix || strings.HasPrefix(path, s.prefix+pathutil.UnixSeparator)
}

// send enqueues an event if the path matches. Non-blocking: drops the event
// if the subscription's queue is full (best-effort, same as OS watchers).
func (s *Subscription) send(op Op, path string) {
	if !s.matches(path) {
		return
	}
	select {
	case s.events <- event{op: op, path: path}:
	default:
	}
}

// Bus broadcasts published events to every active, matching [Subscription].
// The zero value is not usable; construct one with [New].
type Bus struct {
	mu   sync.RWMutex
	subs []*Subscription
}

// New returns an empty, ready-to-use Bus.
func New() *Bus {
	return &Bus{}
}

// Subscribe registers hook to receive every published event whose path falls
// under prefix ("." watches everything except the root path itself), and
// starts the background goroutine that delivers them. Delivery stops when
// ctx is canceled or the returned Subscription is closed, whichever comes
// first.
func (b *Bus) Subscribe(ctx context.Context, prefix string, hook Hook) *Subscription {
	ctx, cancel := context.WithCancel(ctx)
	sub := &Subscription{
		bus:    b,
		prefix: prefix,
		hook:   hook,
		cancel: cancel,
		done:   make(chan struct{}),
		events: make(chan event, QueueSize),
	}
	b.mu.Lock()
	b.subs = append(b.subs, sub)
	b.mu.Unlock()
	go sub.loop(ctx)
	return sub
}

// Publish delivers op/path to every active subscription whose prefix
// matches path.
func (b *Bus) Publish(op Op, path string) {
	b.mu.RLock()
	subs := b.subs
	b.mu.RUnlock()
	for _, s := range subs {
		s.send(op, path)
	}
}

// Close cancels every active subscription, without waiting for their
// goroutines to drain, and clears the bus. Intended for use when the owning
// FS itself is closed; individual Subscription.Close calls that race with it
// remain safe (idempotent) but redundant. It always returns nil.
func (b *Bus) Close() error {
	b.mu.Lock()
	for _, s := range b.subs {
		s.cancel()
	}
	b.subs = nil
	b.mu.Unlock()
	return nil
}

func (b *Bus) remove(s *Subscription) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i, sub := range b.subs {
		if sub == s {
			b.subs = append(b.subs[:i], b.subs[i+1:]...)
			return
		}
	}
}
