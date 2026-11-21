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
	"fmt"
	"os"
	"strings"
	"sync/atomic"
	"testing"
)

// The priorities the built-in drivers register with, as they are on this
// branch. matchDriver reads them highest first.
const (
	benchPriorityFallback    = PriorityFallback
	benchPriorityScheme      = PriorityDefault
	benchPrioritySpecialized = PrioritySpecialized
)

// registerBenchDrivers fills r with the drivers a program gets by importing
// the built-ins: the local driver, whose matcher looks at the disk, and the
// scheme drivers, whose matchers only inspect the name. The names are unique
// per call so a benchmark may register into a registrar more than once.
func registerBenchDrivers(b *testing.B, r *registrar) {
	b.Helper()
	var n atomic.Int64
	unique := func(name string) string { return fmt.Sprintf("%s-%d", name, n.Add(1)) }
	drivers := []Driver{
		{
			Name:      unique("local"),
			MatchFunc: isLocalBenchURI,
			// Unreachable: matchDriver must not create a file system.
			CreateFunc: nopCreate,
			Priority:   benchPriorityFallback,
		},
		{Name: unique("memory"), MatchFunc: prefixMatcher("memory://"), CreateFunc: nopCreate, Priority: benchPriorityScheme},
		{Name: unique("null"), MatchFunc: prefixMatcher("null://"), CreateFunc: nopCreate, Priority: benchPriorityScheme},
		{Name: unique("angry"), MatchFunc: prefixMatcher("angry://"), CreateFunc: nopCreate, Priority: benchPriorityScheme},
		{Name: unique("archive"), MatchFunc: prefixMatcher("archive://"), CreateFunc: nopCreate, Priority: benchPriorityScheme},
		{Name: unique("http-archive"), MatchFunc: httpBenchMatcher, CreateFunc: nopCreate, Priority: benchPriorityScheme},
		{Name: unique("gcs"), MatchFunc: prefixMatcher("gs://"), CreateFunc: nopCreate, Priority: benchPriorityScheme},
		{Name: unique("git"), MatchFunc: gitBenchMatcher, CreateFunc: nopCreate, Priority: benchPrioritySpecialized},
	}
	for _, driver := range drivers {
		if err := r.registerDriver(driver); err != nil {
			b.Fatal(err)
		}
	}
}

func prefixMatcher(prefix string) func(string) bool {
	return func(name string) bool { return strings.HasPrefix(name, prefix) }
}

func httpBenchMatcher(name string) bool {
	return strings.HasPrefix(name, "http://") || strings.HasPrefix(name, "https://")
}

func gitBenchMatcher(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".git")
}

// isLocalBenchURI is drivers/core.isLocalFSUri, which answers for a host path
// only after looking at the disk.
func isLocalBenchURI(name string) bool {
	if strings.HasPrefix(name, "local://") || !strings.Contains(name, "://") {
		return true
	}
	stat, err := os.Stat(name)
	return err == nil && stat != nil
}

// BenchmarkMatchDriver measures the driver lookup every ufs.New and mount does
// for a name it has not resolved before.
func BenchmarkMatchDriver(b *testing.B) {
	for _, uri := range []string{"memory://dir/file", "dir/file"} {
		kind := "scheme"
		if !strings.Contains(uri, "://") {
			kind = "host-path"
		}
		b.Run(kind, func(b *testing.B) {
			r := newRegistrar()
			registerBenchDrivers(b, r)
			for b.Loop() {
				if _, err := r.matchDriver(uri); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

// BenchmarkRegisterDriver measures the cost added at startup: matchDriver
// walks a sorted list, so registering a driver maintains that list as well as
// the map. The set goes into a fresh registrar each iteration, as it does once
// at init for a program that imports the built-in drivers.
func BenchmarkRegisterDriver(b *testing.B) {
	for b.Loop() {
		r := newRegistrar()
		registerBenchDrivers(b, r)
	}
}
