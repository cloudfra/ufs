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
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
)

func nopCreate(context.Context, string) (FS, error) {
	return nil, nil
}

// registerTestCounter keeps names passed to the package-level Register()
// unique across repeated test runs (e.g. `go test -count N`), since
// Register uses the shared globalDriverRegistrar and panics on collision.
var registerTestCounter atomic.Int64

func uniqueDriverName(prefix string) string {
	return fmt.Sprintf("%s-%d", prefix, registerTestCounter.Add(1))
}

func alwaysMatch(string) bool {
	return true
}

func neverMatch(string) bool {
	return false
}

func TestRegistrarRegister(t *testing.T) {
	tests := []struct {
		name      string
		driver    Driver
		wantError string
	}{
		{
			name: "empty name",
			driver: Driver{
				Name:       "",
				MatchFunc:  nil,
				CreateFunc: nil,
			},
			wantError: "empty name",
		},
		{
			name: "nil CreateFunc",
			driver: Driver{
				Name:      "test-driver",
				MatchFunc: neverMatch,
			},
			wantError: "empty CreateFunc",
		},
		{
			name: "nil MatchFunc",
			driver: Driver{
				Name:       "test-driver",
				CreateFunc: nopCreate,
			},
			wantError: "empty MatchFunc",
		},
		{
			name: "valid driver",
			driver: Driver{
				Name:       "test-driver",
				MatchFunc:  neverMatch,
				CreateFunc: nopCreate,
			},
			wantError: "",
		},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprintf("create(%s)", tc.name), func(t *testing.T) {
			t.Parallel()
			r := newRegistrar()
			err := r.register(tc.driver)
			if tc.wantError == "" {
				if err != nil {
					t.Errorf("got: %q, want: nil", err)
				}
			} else {
				if err == nil {
					t.Errorf("got: nil, want: %q (substring)", tc.wantError)
				} else if !strings.Contains(err.Error(), tc.wantError) {
					t.Errorf("got: %q, want: %q (substring)", err, tc.wantError)
				}
			}
		})
	}
}

func TestRegistrarRegisterDuplicate(t *testing.T) {
	r := newRegistrar()
	driver := Driver{
		Name:       "dup-driver",
		MatchFunc:  neverMatch,
		CreateFunc: nopCreate,
	}
	if err := r.register(driver); err != nil {
		t.Fatalf("first register() = %v, want nil", err)
	}
	err := r.register(driver)
	if err == nil {
		t.Fatal("second register() = nil error, want error")
	}
	if !strings.Contains(err.Error(), "already registered") {
		t.Errorf("got: %q, want substring %q", err, "already registered")
	}
}

func TestRegistrarConcurrentRegister(t *testing.T) {
	r := newRegistrar()
	const n = 50
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := range n {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			errs[i] = r.register(Driver{
				Name:       fmt.Sprintf("concurrent-%d", i),
				MatchFunc:  neverMatch,
				CreateFunc: nopCreate,
			})
		}(i)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			t.Errorf("register(%d) = %v, want nil", i, err)
		}
	}
	if got := len(r.m); got != n {
		t.Errorf("registrar has %d entries, want %d", got, n)
	}
}

func TestRegistrarMatch(t *testing.T) {
	t.Run("empty registrar", func(t *testing.T) {
		r := newRegistrar()
		_, err := r.match("anything")
		if err == nil {
			t.Fatal("match() = nil error, want error")
		}
		if !strings.Contains(err.Error(), "cannot find a ufs file system driver") {
			t.Errorf("got: %q, want substring %q", err, "cannot find a ufs file system driver")
		}
	})

	t.Run("no matching driver", func(t *testing.T) {
		r := newRegistrar()
		if err := r.register(Driver{Name: "non-matcher", MatchFunc: neverMatch, CreateFunc: nopCreate}); err != nil {
			t.Fatal(err)
		}
		_, err := r.match("anything")
		if err == nil {
			t.Fatal("match() = nil error, want error")
		}
	})

	t.Run("single match", func(t *testing.T) {
		r := newRegistrar()
		driver := Driver{
			Name:       "matcher",
			MatchFunc:  func(s string) bool { return s == "match-me" },
			CreateFunc: nopCreate,
		}
		if err := r.register(driver); err != nil {
			t.Fatal(err)
		}
		got, err := r.match("match-me")
		if err != nil {
			t.Fatalf("match() = %v, want nil", err)
		}
		if got.Name != driver.Name {
			t.Errorf("match() = %q, want %q", got.Name, driver.Name)
		}
	})

	t.Run("lower priority value wins", func(t *testing.T) {
		r := newRegistrar()
		low := Driver{Name: "low", MatchFunc: alwaysMatch, CreateFunc: nopCreate, Priority: 1}
		high := Driver{Name: "high", MatchFunc: alwaysMatch, CreateFunc: nopCreate, Priority: 5}
		if err := r.register(low); err != nil {
			t.Fatal(err)
		}
		if err := r.register(high); err != nil {
			t.Fatal(err)
		}
		got, err := r.match("anything")
		if err != nil {
			t.Fatalf("match() = %v, want nil", err)
		}
		if got.Name != low.Name {
			t.Errorf("match() = %q, want %q (lower Priority value should win)", got.Name, low.Name)
		}
	})

	t.Run("ambiguous same priority", func(t *testing.T) {
		r := newRegistrar()
		a := Driver{Name: "a", MatchFunc: alwaysMatch, CreateFunc: nopCreate, Priority: 1}
		b := Driver{Name: "b", MatchFunc: alwaysMatch, CreateFunc: nopCreate, Priority: 1}
		if err := r.register(a); err != nil {
			t.Fatal(err)
		}
		if err := r.register(b); err != nil {
			t.Fatal(err)
		}
		_, err := r.match("anything")
		if err == nil {
			t.Fatal("match() = nil error, want ambiguous error")
		}
		if !strings.Contains(err.Error(), "ambiguous") {
			t.Errorf("got: %q, want substring %q", err, "ambiguous")
		}
	})
}

func TestRegistrarCreate(t *testing.T) {
	t.Run("no match", func(t *testing.T) {
		r := newRegistrar()
		_, err := r.create(t.Context(), "anything")
		if err == nil {
			t.Fatal("create() = nil error, want error")
		}
	})

	t.Run("propagates CreateFunc error", func(t *testing.T) {
		r := newRegistrar()
		wantErr := errors.New("boom")
		driver := Driver{
			Name:      "err-driver",
			MatchFunc: alwaysMatch,
			CreateFunc: func(context.Context, string) (FS, error) {
				return nil, wantErr
			},
		}
		if err := r.register(driver); err != nil {
			t.Fatal(err)
		}
		_, err := r.create(t.Context(), "whatever")
		if !errors.Is(err, wantErr) {
			t.Errorf("create() error = %v, want %v", err, wantErr)
		}
	})

	t.Run("passes context and name through to CreateFunc", func(t *testing.T) {
		r := newRegistrar()
		type ctxKey struct{}
		wantCtx := context.WithValue(t.Context(), ctxKey{}, "value")
		wantName := "my-name"
		var gotCtx context.Context
		var gotName string
		driver := Driver{
			Name:      "capture-driver",
			MatchFunc: alwaysMatch,
			CreateFunc: func(ctx context.Context, name string) (FS, error) {
				gotCtx = ctx
				gotName = name
				return nil, nil
			},
		}
		if err := r.register(driver); err != nil {
			t.Fatal(err)
		}
		if _, err := r.create(wantCtx, wantName); err != nil {
			t.Fatal(err)
		}
		if gotCtx.Value(ctxKey{}) != "value" {
			t.Error("create() did not pass the given context through to CreateFunc")
		}
		if gotName != wantName {
			t.Errorf("create() passed name = %q to CreateFunc, want %q", gotName, wantName)
		}
	})

	t.Run("registrar entry missing CreateFunc", func(t *testing.T) {
		// register() rejects a nil CreateFunc, so reach this defensive branch
		// by inserting directly into the map.
		r := newRegistrar()
		r.m["broken"] = Driver{
			Name:      "broken",
			MatchFunc: alwaysMatch,
		}
		_, err := r.create(t.Context(), "anything")
		if err == nil {
			t.Fatal("create() = nil error, want error")
		}
		if !strings.Contains(err.Error(), "not configured") {
			t.Errorf("got: %q, want substring %q", err, "not configured")
		}
	})
}

func TestRegisterSuccess(t *testing.T) {
	name := uniqueDriverName("register-test-success-driver")
	Register(Driver{
		Name:       name,
		MatchFunc:  neverMatch,
		CreateFunc: nopCreate,
	})
	if _, ok := globalDriverRegistrar.m[name]; !ok {
		t.Errorf("Register() did not add driver %q to the global registrar", name)
	}
}

func TestRegisterPanicsOnDuplicate(t *testing.T) {
	name := uniqueDriverName("register-test-panic-driver")
	driver := Driver{
		Name:       name,
		MatchFunc:  neverMatch,
		CreateFunc: nopCreate,
	}
	Register(driver)

	defer func() {
		if r := recover(); r == nil {
			t.Error("Register() did not panic on duplicate registration")
		}
	}()
	Register(driver)
}

func TestGetRegistrar(t *testing.T) {
	if got := getRegistrar(); got != globalDriverRegistrar {
		t.Errorf("getRegistrar() = %p, want %p (the global registrar)", got, globalDriverRegistrar)
	}
}
