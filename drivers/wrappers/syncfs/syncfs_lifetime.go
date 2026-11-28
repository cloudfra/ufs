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

import "sync"

// Lifetime tracks the operations a server has in progress against a file
// system, so that Shutdown can wait for them before the file system is closed.
// The zero value is ready to use.
//
// An operation calls Enter before it touches the file system and Exit when it
// is done. Enter refuses once Shutdown has begun, and Shutdown returns once
// every operation that was admitted has exited. Enter holds nothing while the
// operation runs, so an admitted operation may enter again, which a lock held
// across the operation could not allow while Shutdown waits.
type Lifetime struct {
	// mu orders Enter against Shutdown: an Enter that sees closed false has
	// added itself to active before Shutdown can start waiting on it.
	mu     sync.RWMutex
	closed bool
	active sync.WaitGroup
}

// Enter admits one operation and reports true, or reports false once Shutdown
// has begun. Every Enter that reports true must be matched by one Exit.
func (l *Lifetime) Enter() bool {
	l.mu.RLock()
	if l.closed {
		l.mu.RUnlock()
		return false
	}
	l.active.Add(1)
	l.mu.RUnlock()
	return true
}

// Exit ends an operation that Enter admitted.
func (l *Lifetime) Exit() {
	l.active.Done()
}

// Shutdown refuses every later Enter and waits until every admitted operation
// has exited. Calling it again waits the same way and is not an error.
func (l *Lifetime) Shutdown() {
	l.mu.Lock()
	l.closed = true
	l.mu.Unlock()
	l.active.Wait()
}
