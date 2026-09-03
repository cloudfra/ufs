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
	"io/fs"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestFaultInjectorDelegatesWhenNoFaults(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := FaultInjector(inner, FaultConfig{})
	defer validateClose(t, fsys)()

	if _, err := fsys.Open("."); err != nil {
		t.Errorf("Open(.) = %v, want nil", err)
	}
	if _, err := fsys.Stat("."); err != nil {
		t.Errorf("Stat(.) = %v, want nil", err)
	}
	if _, err := fsys.ReadDir("."); err != nil {
		t.Errorf("ReadDir(.) = %v, want nil", err)
	}
	if _, err := fsys.ReadFile("a.txt"); err != nil {
		t.Errorf("ReadFile(a.txt) = %v, want nil", err)
	}
	if _, err := fsys.Create("a.txt"); err != nil {
		t.Errorf("Create(a.txt) = %v, want nil", err)
	}
	if err := fsys.MkdirAll("subdir", fs.ModePerm); err != nil {
		t.Errorf("MkdirAll(subdir) = %v, want nil", err)
	}
	if err := fsys.Remove("a.txt"); err != nil {
		t.Errorf("Remove(a.txt) = %v, want nil", err)
	}
	if err := fsys.RemoveAll("subdir"); err != nil {
		t.Errorf("RemoveAll(subdir) = %v, want nil", err)
	}
}

func TestFaultInjectorAlwaysErrors(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := FaultInjector(inner, FaultConfig{
		ErrorRate: 1.0,
	})
	defer validateClose(t, fsys)()

	tests := []struct {
		name string
		op   func() error
	}{
		{"Open", func() error { _, err := fsys.Open("."); return err }},
		{"Stat", func() error { _, err := fsys.Stat("."); return err }},
		{"Lstat", func() error { _, err := fsys.Lstat("."); return err }},
		{"ReadDir", func() error { _, err := fsys.ReadDir("."); return err }},
		{"ReadFile", func() error { _, err := fsys.ReadFile("a.txt"); return err }},
		{"ReadLink", func() error { _, err := fsys.ReadLink("a.txt"); return err }},
		{"Create", func() error { _, err := fsys.Create("a.txt"); return err }},
		{"MkdirAll", func() error { return fsys.MkdirAll("subdir", fs.ModePerm) }},
		{"Remove", func() error { return fsys.Remove("a.txt") }},
		{"RemoveAll", func() error { return fsys.RemoveAll("subdir") }},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if err := tc.op(); err == nil {
				t.Errorf("%s returned nil error, want fault injected error", tc.name)
			}
		})
	}
}

func TestFaultInjectorReturnsRealisticErrors(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := FaultInjector(inner, FaultConfig{
		ErrorRate: 1.0,
	})
	defer validateClose(t, fsys)()

	seen := map[syscall.Errno]bool{}
	for range 200 {
		_, err := fsys.Stat(".")
		if err == nil {
			t.Fatal("Stat(.) returned nil, want error")
		}
		for _, want := range faultErrors {
			if errors.Is(err, want) {
				seen[want.(syscall.Errno)] = true
			}
		}
	}

	if len(seen) < 2 {
		t.Errorf("expected multiple distinct error types, got %d: %v", len(seen), seen)
	}
}

func TestFaultInjectorErrorRate(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := FaultInjector(inner, FaultConfig{
		ErrorRate: 0.5,
	})
	defer validateClose(t, fsys)()

	var errors, successes int
	for range 1000 {
		_, err := fsys.Stat(".")
		if err != nil {
			errors++
		} else {
			successes++
		}
	}

	if errors == 0 {
		t.Error("expected some errors with 50% error rate, got 0")
	}
	if successes == 0 {
		t.Error("expected some successes with 50% error rate, got 0")
	}
}

func TestFaultInjectorErrorRateClamping(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)

	t.Run("above_one", func(t *testing.T) {
		t.Parallel()
		fsys := FaultInjector(inner, FaultConfig{ErrorRate: 5.0})
		defer validateClose(t, fsys)()
		for range 10 {
			if _, err := fsys.Stat("."); err == nil {
				t.Fatal("Stat(.) returned nil, want error with clamped rate 1.0")
			}
		}
	})

	t.Run("negative", func(t *testing.T) {
		t.Parallel()
		fsys := FaultInjector(inner, FaultConfig{ErrorRate: -1.0})
		defer validateClose(t, fsys)()
		for range 10 {
			if _, err := fsys.Stat("."); err != nil {
				t.Fatalf("Stat(.) = %v, want nil with clamped rate 0.0", err)
			}
		}
	})
}

func TestFaultInjectorLatency(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	latency := 50 * time.Millisecond
	fsys := FaultInjector(inner, FaultConfig{
		Latency: latency,
	})
	defer validateClose(t, fsys)()

	start := time.Now()
	if _, err := fsys.Stat("."); err != nil {
		t.Fatalf("Stat(.) = %v, want nil", err)
	}
	elapsed := time.Since(start)

	if elapsed < latency {
		t.Errorf("operation took %v, want at least %v", elapsed, latency)
	}
}

func TestFaultInjectorLatencyJitter(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping timing-sensitive test in short mode")
	}
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := FaultInjector(inner, FaultConfig{
		LatencyJitter: 100 * time.Millisecond,
	})
	defer validateClose(t, fsys)()

	start := time.Now()
	if _, err := fsys.Stat("."); err != nil {
		t.Fatalf("Stat(.) = %v, want nil", err)
	}
	elapsed := time.Since(start)

	if elapsed >= 100*time.Millisecond {
		t.Errorf("operation took %v, jitter should be less than 100ms", elapsed)
	}
}

func TestFaultInjectorCloseAlwaysDelegates(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := FaultInjector(inner, FaultConfig{
		ErrorRate: 1.0,
	})

	if err := fsys.Close(); err != nil {
		t.Errorf("Close() = %v, want nil (should not inject faults)", err)
	}
}

func TestFaultInjectorInvalidPaths(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := FaultInjector(inner, FaultConfig{})
	defer validateClose(t, fsys)()

	for _, badPath := range []string{"/absolute", "../parent", "bad/../path"} {
		t.Run(badPath, func(t *testing.T) {
			t.Parallel()
			if _, err := fsys.Open(badPath); err == nil {
				t.Errorf("Open(%q) returned nil error, want error", badPath)
			}
			if _, err := fsys.Stat(badPath); err == nil {
				t.Errorf("Stat(%q) returned nil error, want error", badPath)
			}
			if _, err := fsys.Lstat(badPath); err == nil {
				t.Errorf("Lstat(%q) returned nil error, want error", badPath)
			}
			if _, err := fsys.ReadDir(badPath); err == nil {
				t.Errorf("ReadDir(%q) returned nil error, want error", badPath)
			}
			if _, err := fsys.ReadFile(badPath); err == nil {
				t.Errorf("ReadFile(%q) returned nil error, want error", badPath)
			}
			if _, err := fsys.ReadLink(badPath); err == nil {
				t.Errorf("ReadLink(%q) returned nil error, want error", badPath)
			}
			if _, err := fsys.Create(badPath); err == nil {
				t.Errorf("Create(%q) returned nil error, want error", badPath)
			}
			if err := fsys.MkdirAll(badPath, fs.ModePerm); err == nil {
				t.Errorf("MkdirAll(%q) returned nil error, want error", badPath)
			}
			if err := fsys.Remove(badPath); err == nil {
				t.Errorf("Remove(%q) returned nil error, want error", badPath)
			}
			if err := fsys.RemoveAll(badPath); err == nil {
				t.Errorf("RemoveAll(%q) returned nil error, want error", badPath)
			}
		})
	}
}

func TestFaultInjectorString(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	fsys := FaultInjector(inner, FaultConfig{})

	got := fsys.String()
	if !strings.Contains(got, "faultFS(") {
		t.Errorf("String() = %q, want to contain %q", got, "faultFS(")
	}
	if !strings.Contains(got, nullFSPrefix) {
		t.Errorf("String() = %q, want to contain %q", got, nullFSPrefix)
	}
}

func TestFaultInjectorZeroValueConfig(t *testing.T) {
	t.Parallel()

	inner := makeNullFS(nullFSPrefix)
	cfg := FaultConfig{}
	if !cfg.isZero() {
		t.Error("zero-value FaultConfig.isZero() = false, want true")
	}

	nonZero := FaultConfig{ErrorRate: 0.1}
	if nonZero.isZero() {
		t.Error("non-zero FaultConfig.isZero() = true, want false")
	}

	fsys := applyWrappers(inner, MountSpecOptions{Fault: &cfg})
	if _, ok := fsys.(*faultFS); ok {
		t.Error("applyWrappers with zero-value FaultConfig should not wrap in faultFS")
	}

	fsys = applyWrappers(inner, MountSpecOptions{Fault: &nonZero})
	if _, ok := fsys.(*faultFS); !ok {
		t.Error("applyWrappers with non-zero FaultConfig should wrap in faultFS")
	}
}
