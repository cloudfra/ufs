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

package syncfs

import (
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
)

// TestLifetimeRefusesAfterShutdown checks that Shutdown with nothing in
// progress returns at once and that every later Enter is refused.
func TestLifetimeRefusesAfterShutdown(t *testing.T) {
	t.Parallel()
	var l Lifetime
	if !l.Enter() {
		t.Fatal("Enter() before Shutdown = false, want true")
	}
	l.Exit()
	l.Shutdown()
	if l.Enter() {
		t.Error("Enter() after Shutdown = true, want false")
	}
	l.Shutdown()
}

// TestLifetimeShutdownWaitsForExit checks that Shutdown does not return while
// an admitted operation is still in progress, and that the operation can enter
// again (and is refused) without deadlocking against the waiting Shutdown.
func TestLifetimeShutdownWaitsForExit(t *testing.T) {
	t.Parallel()
	var l Lifetime
	if !l.Enter() {
		t.Fatal("Enter() = false, want true")
	}

	var exited atomic.Bool
	done := make(chan bool)
	go func() {
		l.Shutdown()
		done <- exited.Load()
	}()

	// Wait until Shutdown has begun refusing, without depending on timing.
	for l.Enter() {
		l.Exit()
		runtime.Gosched()
	}
	// The admitted operation entering again is refused rather than blocked.
	if l.Enter() {
		t.Error("nested Enter() during Shutdown = true, want false")
	}

	exited.Store(true)
	l.Exit()
	if !<-done {
		t.Error("Shutdown returned before the admitted operation exited")
	}
}

// BenchmarkLifetime measures one Enter and Exit pair.
func BenchmarkLifetime(b *testing.B) {
	var l Lifetime
	b.ReportAllocs()
	b.RunParallel(func(pb *testing.PB) {
		for pb.Next() {
			if l.Enter() {
				l.Exit()
			}
		}
	})
}

// TestLifetimeConcurrentShutdown runs operations, some of which enter again
// from inside, from several goroutines while several Shutdown calls race each
// other. No admitted operation may still be running, or start, once any
// Shutdown has returned.
func TestLifetimeConcurrentShutdown(t *testing.T) {
	t.Parallel()
	var (
		l        Lifetime
		inFlight atomic.Int32
		shutDown atomic.Bool
		started  sync.WaitGroup
		workers  sync.WaitGroup
	)
	const goroutines = 8
	started.Add(goroutines)
	for g := range goroutines {
		workers.Go(func() {
			first := true
			for i := 0; ; i++ {
				if !l.Enter() {
					if first {
						started.Done()
					}
					return
				}
				inFlight.Add(1)
				if shutDown.Load() {
					t.Error("an operation was admitted after Shutdown returned")
				}
				if (g+i)%4 == 0 && l.Enter() {
					// A nested Enter may be admitted or refused, but
					// must not deadlock against a waiting Shutdown.
					runtime.Gosched()
					l.Exit()
				}
				runtime.Gosched()
				inFlight.Add(-1)
				l.Exit()
				if first {
					started.Done()
					first = false
				}
			}
		})
	}
	started.Wait()

	var shutdowns sync.WaitGroup
	for range 3 {
		shutdowns.Go(func() {
			l.Shutdown()
			if n := inFlight.Load(); n != 0 {
				t.Errorf("Shutdown returned with %d operations in progress", n)
			}
			shutDown.Store(true)
		})
	}
	shutdowns.Wait()
	workers.Wait()
	if l.Enter() {
		t.Error("Enter() after Shutdown = true, want false")
	}
}
