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
	"sync"
)

var (
	globalDriverRegistrar = newRegistrar()
	emptyRegistration     = Driver{}
)

type registrar struct {
	sync.RWMutex
	m map[string]Driver
}

// Driver is the driver configuration for a file system driver.
type Driver struct {
	// Name of the file system driver.
	Name string
	// CreateFunc is invoked when creating an instance of the file system driver.
	CreateFunc func(context.Context, string) (FS, error)
	// MatchFunc returns true if the URI in the string matches a pattern that the driver can handle.
	MatchFunc func(string) bool
	// Priority indicates the priority of the matcher.
	// This will be used to disambiguate
	Priority int
}

func newRegistrar() *registrar {
	return &registrar{
		m: map[string]Driver{},
	}
}

// Register a new file system type.
//
// This method should be called from your package's init()
func Register(reg Driver) {
	if err := globalDriverRegistrar.register(reg); err != nil {
		panic(err)
	}
}

func getRegistrar() *registrar {
	return globalDriverRegistrar
}

func (r *registrar) register(reg Driver) error {
	if reg.Name == "" {
		return errors.New("cannot register a file system driver with an empty name")
	}
	if reg.CreateFunc == nil {
		return fmt.Errorf("file system driver %q cannot have an empty CreateFunc", reg.Name)
	}
	if reg.MatchFunc == nil {
		return fmt.Errorf("file system driver %q cannot have an empty MatchFunc", reg.Name)
	}
	var err error
	r.Lock()
	if _, ok := r.m[reg.Name]; !ok {
		r.m[reg.Name] = reg
	} else {
		err = fmt.Errorf("file system driver %q is already registered", reg.Name)
	}
	r.Unlock()
	return err
}

func (r *registrar) match(name string) (Driver, error) {
	r.RLock()
	result := emptyRegistration
	for _, reg := range r.m {
		if reg.MatchFunc(name) {
			switch {
			case result.Name == emptyRegistration.Name:
				result = reg
			case reg.Priority < result.Priority:
				result = reg
			case reg.Priority == result.Priority:
				r.RUnlock()
				return emptyRegistration, fmt.Errorf("ambiguous driver match for %q, both %q and %q both have a priority %d ", name, reg.Name, result.Name, reg.Priority)
			}
		}
	}
	r.RUnlock()
	if result.Name == emptyRegistration.Name {
		return emptyRegistration, fmt.Errorf("cannot find a ufs file system driver for %q", name)
	}
	return result, nil
}

func (r *registrar) create(ctx context.Context, name string) (FS, error) {
	reg, err := r.match(name)
	if err != nil {
		return nil, err
	}
	if reg.Name == "" {
		return nil, fmt.Errorf("%q is not supported", name)
	}
	if reg.CreateFunc == nil {
		return nil, fmt.Errorf("FS %q is not configured", reg.Name)
	}
	return reg.CreateFunc(ctx, name)
}
