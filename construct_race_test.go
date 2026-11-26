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

package ufs

import (
	"errors"
	"strconv"
	"strings"
	"sync"
	"testing"
)

// FSBuilder (construct.go:304-306) keeps its mounts in one slice, and every
// mount method appends to it without a guard: Mount at :324 and MountFS at :331.
// Build reads that slice at :346 and BuildURI reads it at :371 and :372, also
// unguarded. The "Safe for concurrent use" sentence in the package doc at :151
// describes the FS that New returns, not the builder, so nothing in the
// documented contract promises a builder can be filled from more than one
// goroutine - but nothing says it cannot either, and this is the one component
// in #748 whose window is reachable purely in-process.
//
// Two things can go wrong and the tests below separate them. Two appends racing
// each other lose one of the mounts: the slice header is written twice, so a
// caller can add a mount and have it vanish. A Build or BuildURI that runs while
// an append is in flight can read past the elements that were actually written,
// which serializes a mount that nobody asked for.
//
// The tests are red on purpose. The fix that turns them green is an owner call:
// guard the slice, or state on FSBuilder that a builder belongs to one
// goroutine. That decision is open question 1 in #748.

// raceBuilderRoot and raceBuilderMountURI are URI strings, not file systems:
// BuildURI only serializes, so no driver is opened by these tests.
const (
	raceBuilderRoot     = "memory://builder-race-root"
	raceBuilderMountURI = "memory://builder-race-mount"
)

// raceMountPath builds the mount path for one worker's one mount. Paths are
// single-segment because BuildURI serializes them as URL query keys, where a
// slash comes back percent-encoded and cannot be matched literally.
func raceMountPath(worker, index int) string {
	return "m" + strconv.Itoa(worker) + "-" + strconv.Itoa(index)
}

// TestConcurrentMountOnOneBuilder fills one builder from eight goroutines. The
// builder must end up holding every mount that was added, each exactly once -
// which is all Mount is documented to do.
func TestConcurrentMountOnOneBuilder(t *testing.T) {
	t.Parallel()

	builder := NewFSBuilder(raceBuilderRoot)

	const workers = 8
	const perWorker = 25

	var running sync.WaitGroup
	for worker := range workers {
		running.Go(func() {
			for index := range perWorker {
				builder.Mount(raceMountPath(worker, index), raceBuilderMountURI)
			}
		})
	}
	running.Wait()

	if got := len(builder.mounts); got != workers*perWorker {
		t.Errorf("builder recorded %d of the %d mounts added by %d goroutines, want every Mount call kept: an append that races another append loses one of them", got, workers*perWorker, workers)
	}

	uri, err := builder.BuildURI()
	if err != nil {
		t.Fatalf("BuildURI() = %v, want nil", err)
	}
	for worker := range workers {
		for index := range perWorker {
			path := raceMountPath(worker, index)
			if !strings.Contains(uri, path+"=") {
				t.Errorf("BuildURI() = %q, missing the mount %q that was added, want every added mount serialized", uri, path)
			}
		}
	}
}

// TestBuildURIWhileMounting serializes a builder that is still being filled: one
// goroutine calls BuildURI while four others add mounts. BuildURI must never
// report an error here - every mount in this test is URI-based - and must never
// crash. A torn read of the slice hands it an element that was never written, so
// an error or a panic is the corruption showing up.
func TestBuildURIWhileMounting(t *testing.T) {
	t.Parallel()

	builder := NewFSBuilder(raceBuilderRoot)

	const mountWorkers = 4
	const perWorker = 50
	const serializations = 100

	var mu sync.Mutex
	var errs []error
	var panics []any
	recordError := func(err error) {
		if err == nil {
			return
		}
		mu.Lock()
		if len(errs) < 3 {
			errs = append(errs, err)
		}
		mu.Unlock()
	}
	recordPanic := func(recovered any) {
		if recovered == nil {
			return
		}
		mu.Lock()
		if len(panics) < 3 {
			panics = append(panics, recovered)
		}
		mu.Unlock()
	}

	var running sync.WaitGroup
	for worker := range mountWorkers {
		running.Go(func() {
			for index := range perWorker {
				builder.Mount(raceMountPath(worker, index), raceBuilderMountURI)
			}
		})
	}
	running.Go(func() {
		for range serializations {
			func() {
				defer func() { recordPanic(recover()) }()
				uri, err := builder.BuildURI()
				recordError(err)
				if err == nil && uri == "" {
					recordError(errors.New("BuildURI returned an empty URI with a nil error"))
				}
			}()
		}
	})
	running.Wait()

	if len(panics) > 0 {
		t.Errorf("BuildURI racing Mount panicked %d time(s), first was %v, want no panic", len(panics), panics[0])
	}
	if len(errs) > 0 {
		t.Errorf("BuildURI reported %v while URI-based mounts were being added, want no error: a read that races an append can see past the elements that were written and serialize a mount nobody asked for", errs[0])
	}
	if got := len(builder.mounts); got != mountWorkers*perWorker {
		t.Errorf("builder recorded %d of the %d mounts added by %d goroutines, want every Mount call kept", got, mountWorkers*perWorker, mountWorkers)
	}
}
