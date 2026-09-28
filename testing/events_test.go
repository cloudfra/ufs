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

package testing_test

import (
	"strings"
	"sync"
	"testing"
	"time"

	utesting "github.com/cloudfra/ufs/testing"
	"github.com/google/go-cmp/cmp"
)

// testOp stands in for ufs.NotifyOp, which this package cannot import.
type testOp int

const (
	opCreate testOp = iota + 1
	opRemove
)

// testHook has the same shape as ufs.NotifyHook.
type testHook func(op testOp, path string)

func TestEventCollectorHookSatisfiesNotifyHookShape(t *testing.T) {
	ec := utesting.NewEventCollector[testOp]()
	var hook testHook = ec.Hook
	hook(opCreate, "a.txt")
	if !ec.HasEvent(func(ev utesting.Event[testOp]) bool { return ev.Op == opCreate && ev.Path == "a.txt" }) {
		t.Errorf("HasEvent(create a.txt) = false after Hook, want true; events: %v", ec.Events())
	}
}

func TestEventCollectorWaitForRecorded(t *testing.T) {
	ec := utesting.NewEventCollector[testOp]()
	ec.Hook(opCreate, "a.txt")
	ec.Hook(opRemove, "b.txt")
	ec.WaitFor(t, time.Second, func(ev utesting.Event[testOp]) bool { return ev.Op == opRemove && ev.Path == "b.txt" })
}

func TestEventCollectorWaitForLaterEvent(t *testing.T) {
	ec := utesting.NewEventCollector[testOp]()
	go func() {
		time.Sleep(10 * time.Millisecond)
		ec.Hook(opCreate, "unrelated.txt")
		ec.Hook(opCreate, "later.txt")
	}()
	ec.WaitFor(t, utesting.EventDeadline, func(ev utesting.Event[testOp]) bool { return ev.Path == "later.txt" })
}

func TestEventCollectorWaitForTimeout(t *testing.T) {
	ec := utesting.NewEventCollector[testOp]()
	ec.Hook(opCreate, "a.txt")
	m := runFatal(t, func(tb testing.TB) {
		ec.WaitFor(tb, 10*time.Millisecond, func(ev utesting.Event[testOp]) bool { return ev.Path == "missing.txt" })
	})
	if len(m.fatals) != 1 {
		t.Fatalf("WaitFor timeout recorded %d fatals, want 1: %v", len(m.fatals), m.fatals)
	}
	if !strings.Contains(m.fatals[0], "a.txt") {
		t.Errorf("WaitFor timeout message = %q, want it to list the collected events", m.fatals[0])
	}
}

func TestEventCollectorHasEvent(t *testing.T) {
	ec := utesting.NewEventCollector[testOp]()
	match := func(ev utesting.Event[testOp]) bool { return ev.Op == opRemove }
	if ec.HasEvent(match) {
		t.Error("HasEvent() = true on an empty collector, want false")
	}
	ec.Hook(opCreate, "a.txt")
	if ec.HasEvent(match) {
		t.Error("HasEvent(remove) = true with only a create event, want false")
	}
	ec.Hook(opRemove, "a.txt")
	if !ec.HasEvent(match) {
		t.Error("HasEvent(remove) = false after a remove event, want true")
	}
}

func TestEventCollectorEventsIsACopy(t *testing.T) {
	ec := utesting.NewEventCollector[testOp]()
	ec.Hook(opCreate, "a.txt")
	ec.Hook(opRemove, "b.txt")
	got := ec.Events()
	want := []utesting.Event[testOp]{{Op: opCreate, Path: "a.txt"}, {Op: opRemove, Path: "b.txt"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("Events() mismatch (-want +got):\n%s", diff)
	}
	got[0].Path = "changed"
	if ec.Events()[0].Path != "a.txt" {
		t.Error("modifying the slice returned by Events() changed the collector")
	}
}

func TestEventCollectorConcurrentHooks(t *testing.T) {
	ec := utesting.NewEventCollector[testOp]()
	const n = 2000 // more than the notification channel's buffer
	var wg sync.WaitGroup
	for range n {
		wg.Go(func() { ec.Hook(opCreate, "f") })
	}
	wg.Wait()
	if got := len(ec.Events()); got != n {
		t.Errorf("len(Events()) = %d after %d concurrent Hook calls, want %d", got, n, n)
	}
}
