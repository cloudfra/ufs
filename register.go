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
)

var (
	globalDriverRegistrar      = newRegistrar()
	emptyDriverRegistration    = Driver{}
	emptyDecoratorRegistration = Decorator{}
)

type registrar struct {
	sync.RWMutex
	driverMap    map[string]Driver
	decoratorMap map[string]Decorator
}

// Decorator is the driver configuration for a file system decorator.
type Decorator struct {
	// Name of the file system decorator.
	Name string

	// CreateFunc is invoked when creating an instance of the file system driver.
	CreateFunc func(context.Context, WriteFS, any) (WriteFS, error)
}

// Driver is the driver configuration for a file system driver.
type Driver struct {
	// Name of the file system driver.
	Name string

	// CreateFunc is invoked when creating an instance of the file system driver.
	CreateFunc func(context.Context, string) (WriteFS, error)

	// MatchFunc returns true if the URI in the string matches a pattern that the driver can handle.
	MatchFunc func(string) bool

	// Priority indicates the priority of the matcher.
	// This will be used to disambiguate
	Priority int

	// Standard indicates that the driver should be verified by conformance tests.
	Standard bool

	// ReadWrite indicates that the driver supports read-write operations.
	ReadWrite bool
}

// NewDriver builds a Driver configuration for a file system driver, to be passed to Register.
func NewDriver(name string, createFunc func(context.Context, string) (WriteFS, error), matchFunc func(string) bool, priority int, standard bool, readWrite bool) Driver {
	return Driver{
		Name:       name,
		CreateFunc: createFunc,
		MatchFunc:  matchFunc,
		Priority:   priority,
		Standard:   standard,
		ReadWrite:  readWrite,
	}
}

// NewDecorator builds a Decorator configuration for a file system decorator, to be passed to RegisterDecorator.
func NewDecorator(name string, createFunc func(context.Context, WriteFS, any) (WriteFS, error)) Decorator {
	return Decorator{
		Name:       name,
		CreateFunc: createFunc,
	}
}

func newRegistrar() *registrar {
	return &registrar{
		driverMap:    map[string]Driver{},
		decoratorMap: map[string]Decorator{},
	}
}

// Register a new file system type.
//
// This method should be called from your package's init()
func Register(reg Driver) {
	if err := globalDriverRegistrar.registerDriver(reg); err != nil {
		panic(err)
	}
}

// RegisterDecorator a new file system decorator type.
//
// This method should be called from your package's init()
func RegisterDecorator(reg Decorator) {
	if err := globalDriverRegistrar.registerDecorator(reg); err != nil {
		panic(err)
	}
}

func getRegistrar() *registrar {
	return globalDriverRegistrar
}

func (r *registrar) registerDriver(reg Driver) error {
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
	if _, ok := r.driverMap[reg.Name]; !ok {
		r.driverMap[reg.Name] = reg
	} else {
		err = fmt.Errorf("file system driver %q is already registered", reg.Name)
	}
	r.Unlock()
	return err
}

func (r *registrar) registerDecorator(reg Decorator) error {
	if reg.Name == "" {
		return errors.New("cannot register a file system driver with an empty name")
	}
	if reg.CreateFunc == nil {
		return fmt.Errorf("file system driver %q cannot have an empty CreateFunc", reg.Name)
	}
	var err error
	r.Lock()
	if _, ok := r.decoratorMap[reg.Name]; !ok {
		r.decoratorMap[reg.Name] = reg
	} else {
		err = fmt.Errorf("file system decorator %q is already registered", reg.Name)
	}
	r.Unlock()
	return err
}

func (r *registrar) matchDriver(name string) (Driver, error) {
	r.RLock()
	result := emptyDriverRegistration
	for _, reg := range r.driverMap {
		if reg.MatchFunc(name) {
			switch {
			case result.Name == emptyDriverRegistration.Name:
				result = reg
			case reg.Priority < result.Priority:
				result = reg
			case reg.Priority == result.Priority:
				r.RUnlock()
				return emptyDriverRegistration, fmt.Errorf("ambiguous driver match for %q, both %q and %q both have a priority %d ", name, reg.Name, result.Name, reg.Priority)
			}
		}
	}
	r.RUnlock()
	if result.Name == emptyDriverRegistration.Name {
		return emptyDriverRegistration, fmt.Errorf("cannot find a ufs file system driver for %q", name)
	}
	return result, nil
}

func (r *registrar) matchDecorator(name string) (Decorator, error) {
	r.RLock()
	decorator, ok := r.decoratorMap[strings.ToLower(name)]
	r.RUnlock()
	if !ok {
		return emptyDecoratorRegistration, fmt.Errorf("cannot find a ufs file system decorator for %q", name)
	}
	return decorator, nil
}

func (r *registrar) create(ctx context.Context, name string) (WriteFS, error) {
	reg, err := r.matchDriver(name)
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

func (r *registrar) decorate(ctx context.Context, inner WriteFS, name string, args any) (WriteFS, error) {
	reg, err := r.matchDecorator(name)
	if err != nil {
		return nil, err
	}
	return reg.CreateFunc(ctx, inner, args)
}
