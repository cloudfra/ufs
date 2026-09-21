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

package ufs

import (
	"errors"
	"testing"
)

func TestJoinErrors(t *testing.T) {
	t.Parallel()

	errA := errors.New("error A")
	errB := errors.New("error B")

	testCases := []struct {
		name       string
		errs       []error
		wantNil    bool
		wantSameAs error // non-nil: result must be this exact value (no wrapper)
		wantIsA    bool
		wantIsB    bool
	}{
		{name: "no args", errs: nil, wantNil: true},
		{name: "single nil", errs: []error{nil}, wantNil: true},
		{name: "multiple nils", errs: []error{nil, nil, nil}, wantNil: true},
		{name: "single error", errs: []error{errA}, wantSameAs: errA, wantIsA: true},
		{name: "nil then error", errs: []error{nil, errA}, wantSameAs: errA, wantIsA: true},
		{name: "error then nil", errs: []error{errA, nil}, wantSameAs: errA, wantIsA: true},
		{name: "two errors", errs: []error{errA, errB}, wantIsA: true, wantIsB: true},
		{name: "nil two errors nil", errs: []error{nil, errA, errB, nil}, wantIsA: true, wantIsB: true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got := joinErrors(tc.errs...)
			if tc.wantNil {
				if got != nil {
					t.Errorf("joinErrors() = %v, want nil", got)
				}
				return
			}
			if got == nil {
				t.Fatalf("joinErrors() = nil, want non-nil")
			}
			if tc.wantSameAs != nil && got != tc.wantSameAs {
				t.Errorf("joinErrors() returned a wrapped error; want the identical error value, got %v", got)
			}
			if tc.wantIsA && !errors.Is(got, errA) {
				t.Errorf("joinErrors(): errors.Is(result, errA) = false, want true")
			}
			if tc.wantIsB && !errors.Is(got, errB) {
				t.Errorf("joinErrors(): errors.Is(result, errB) = false, want true")
			}
		})
	}
}

func BenchmarkJoin(b *testing.B) {
	errA := errors.New("error A")
	errB := errors.New("error B")
	errC := errors.New("error C")

	benchCases := []struct {
		name string
		errs []error
	}{
		{name: "none", errs: []error{nil, nil}},
		{name: "one", errs: []error{nil, errA, nil}},
		{name: "two", errs: []error{errA, errB}},
		{name: "three", errs: []error{errA, errB, errC}},
	}

	for _, bc := range benchCases {
		b.Run(bc.name, func(b *testing.B) {
			b.ReportAllocs()
			for range b.N {
				joinErrors(bc.errs...) //nolint:errcheck,gosec // The response is not important; this benchmark tracks allocations.
			}
		})
	}
}
