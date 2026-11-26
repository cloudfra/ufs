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

// SPDX-License-Identifier: Apache-2.0

package keyguardfs

import (
	"errors"
	"testing"

	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/internal/patternutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// benchReads are the read operations that pay for the policy check, with the
// name each reads. A protected name is measured too, because a refusal costs
// less than the read it prevents.
var benchReads = []struct {
	name string
	call func(fsys types.ReadFS) error
}{
	{"Stat notes.txt", func(fsys types.ReadFS) error { _, err := fsys.Stat("notes.txt"); return err }},
	{"ReadFile notes.txt", func(fsys types.ReadFS) error { _, err := fsys.ReadFile("notes.txt"); return err }},
	{"ReadDir dir", func(fsys types.ReadFS) error { _, err := fsys.ReadDir("dir"); return err }},
	{"ReadFile keys/server.key", func(fsys types.ReadFS) error { _, err := fsys.ReadFile("keys/server.key"); return err }},
}

// BenchmarkGuardCost measures what the policy check adds to a read, against the
// same read done directly on the guarded file system. The fixture is memFS, so
// the difference is the guard and not the storage below it.
func BenchmarkGuardCost(b *testing.B) {
	inner := makeFixture(b)

	fsys, err := New(inner, Options{})
	if err != nil {
		b.Fatalf("New() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(b, fsys)()

	for _, test := range benchReads {
		b.Run(test.name+"/inner", func(b *testing.B) {
			benchRead(b, inner, test.call)
		})
		b.Run(test.name+"/guarded", func(b *testing.B) {
			benchRead(b, fsys, test.call)
		})
	}
}

// BenchmarkGuardPolicySize measures the same read against the default policy and
// against the smaller one the tests use, because a name the policy does not name
// is checked against every pattern it holds.
func BenchmarkGuardPolicySize(b *testing.B) {
	stat := func(fsys types.ReadFS) error { _, err := fsys.Stat("notes.txt"); return err }
	deep := func(fsys types.ReadFS) error {
		_, err := fsys.Stat("checkout/deploy/sub/pass")

		return err
	}
	for _, policy := range []struct {
		name string
		opts Options
		call func(fsys types.ReadFS) error
	}{
		{name: "4 patterns", opts: Options{Patterns: testPatterns}, call: stat},
		{name: "default policy", opts: Options{}, call: stat},
		{name: "default policy 4 components", opts: Options{}, call: deep},
	} {
		b.Run(policy.name, func(b *testing.B) {
			// Each guard closes the file system it guards, so each needs its own.
			inner := makeFixture(b)
			fsys, err := New(inner, policy.opts)
			if err != nil {
				b.Fatalf("New() = %v, want nil", err)
			}
			defer ufsTesting.ValidateClose(b, fsys)()

			b.Run("inner", func(b *testing.B) {
				benchRead(b, inner, policy.call)
			})
			b.Run("guarded", func(b *testing.B) {
				benchRead(b, fsys, policy.call)
			})
		})
	}
}

// BenchmarkPolicyMatch measures the policy check on its own, which is what a
// guard adds to a read of a name it leaves alone.
func BenchmarkPolicyMatch(b *testing.B) {
	policies := []struct {
		name     string
		patterns []string
	}{
		{name: "4 patterns", patterns: testPatterns},
		{name: "default policy", patterns: DefaultPatterns},
	}
	names := []struct {
		name string
		path string
	}{
		{name: "single component", path: "notes.txt"},
		{name: "4 components", path: "checkout/deploy/sub/pass"},
	}
	for _, policy := range policies {
		for _, entry := range names {
			b.Run(policy.name+"/"+entry.name, func(b *testing.B) {
				b.ReportAllocs()
				for b.Loop() {
					if patternutil.Match(policy.patterns, entry.path) {
						b.Fatalf("%q is protected by %q", entry.path, policy.name)
					}
				}
			})
		}
	}
}

// BenchmarkHideReadDirCost measures hide mode, where the policy check runs again
// for every entry of a listing.
func BenchmarkHideReadDirCost(b *testing.B) {
	inner := makeFixture(b)

	fsys, err := New(inner, Options{Mode: ModeHide})
	if err != nil {
		b.Fatalf("New() = %v, want nil", err)
	}
	defer ufsTesting.ValidateClose(b, fsys)()

	readDir := func(fsys types.ReadFS) error { _, err := fsys.ReadDir("."); return err }
	b.Run("inner", func(b *testing.B) {
		benchRead(b, inner, readDir)
	})
	b.Run("guarded", func(b *testing.B) {
		benchRead(b, fsys, readDir)
	})
}

// benchRead runs call over fsys, allowing only the refusal of a protected name
// to come back as an error.
func benchRead(b *testing.B, fsys types.ReadFS, call func(fsys types.ReadFS) error) {
	b.ReportAllocs()
	for b.Loop() {
		if err := call(fsys); err != nil && !errors.Is(err, ErrProtected) {
			b.Fatal(err)
		}
	}
}
