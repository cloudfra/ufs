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
	"sort"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
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

// Decorator is the configuration for a file system decorator: a wrapper that
// changes the behavior of an existing file system. A decorator is selected by
// the name of a section under a [MountSpec]'s Options.
type Decorator struct {
	// Name of the file system decorator. It is the key of the decorator's
	// section under a [MountSpec]'s Options and is matched case-insensitively.
	Name string

	// CreateFunc wraps the given file system. The last argument is the raw
	// value of the decorator's Options section, see [DecodeOptions].
	CreateFunc func(context.Context, WriteFS, any) (WriteFS, error)

	// Priority orders the decorators of a single mount. Decorators with a
	// lower value are applied first, so they sit closer to the wrapped file
	// system. Ties are broken by Name.
	Priority int
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

// NewDecorator builds a Decorator configuration for a file system decorator,
// to be passed to RegisterDecorator. T is the decorator's options type; the
// decorator's Options section is decoded into it with [DecodeOptions] before
// createFunc is invoked.
func NewDecorator[T any](name string, priority int, createFunc func(context.Context, WriteFS, T) (WriteFS, error)) Decorator {
	return Decorator{
		Name:     name,
		Priority: priority,
		CreateFunc: func(ctx context.Context, inner WriteFS, raw any) (WriteFS, error) {
			opts, err := DecodeOptions[T](raw)
			if err != nil {
				return nil, fmt.Errorf("invalid options for file system decorator %q, %w", name, err)
			}
			return createFunc(ctx, inner, opts)
		},
	}
}

// DecodeOptions converts the raw value of a decorator's Options section into
// the decorator's options type T. A T or non-nil *T is used as is. Any other
// value, such as the map[string]any or scalar produced by parsing a YAML mount
// spec, is decoded using T's yaml struct tags, so T may implement
// [yaml.Unmarshaler] to accept more than one shape (e.g. a bare bool as well
// as a mapping). A nil value yields the zero T.
func DecodeOptions[T any](raw any) (T, error) {
	var opts T
	switch v := raw.(type) {
	case *T:
		if v != nil {
			return *v, nil
		}
		return opts, nil
	case T:
		return v, nil
	}
	data, err := encodeOptions(raw)
	if err != nil {
		return opts, fmt.Errorf("cannot encode %T, %w", raw, err)
	}
	if err := yaml.Unmarshal(data, &opts); err != nil {
		return opts, fmt.Errorf("cannot decode %T as %T, %w", raw, opts, err)
	}
	return opts, nil
}

// encodeOptions marshals raw to YAML. yaml.Marshal panics on values it cannot
// represent (e.g. channels and funcs), which is reported as an error instead.
func encodeOptions(raw any) (data []byte, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("%v", r)
		}
	}()
	return yaml.Marshal(raw)
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
		return errors.New("cannot register a file system decorator with an empty name")
	}
	if reg.CreateFunc == nil {
		return fmt.Errorf("file system decorator %q cannot have an empty CreateFunc", reg.Name)
	}
	key := strings.ToLower(reg.Name)
	var err error
	r.Lock()
	if _, ok := r.decoratorMap[key]; !ok {
		r.decoratorMap[key] = reg
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
		// Decorators outside this package (drivers/decorators/...) register
		// only when imported, so a missing blank import looks like an unknown
		// option.
		return emptyDecoratorRegistration, fmt.Errorf("cannot find a ufs file system decorator for option %q; if it needs a decorator from github.com/cloudfra/ufs/drivers/decorators, check that the decorator package is imported", name)
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

// decorate wraps inner with the decorator of every section in opts, which
// maps a decorator name to its raw options. Every section must match a
// registered decorator. Decorators are applied in ascending Priority order,
// then by Name, so the result does not depend on map iteration order. On error
// inner is left open; closing it is up to the caller.
func (r *registrar) decorate(ctx context.Context, inner WriteFS, opts map[string]any) (WriteFS, error) {
	if len(opts) == 0 {
		return inner, nil
	}
	type section struct {
		reg  Decorator
		args any
	}
	sections := make([]section, 0, len(opts))
	for name, args := range opts {
		reg, err := r.matchDecorator(name)
		if err != nil {
			return nil, err
		}
		for _, s := range sections {
			if s.reg.Name == reg.Name {
				return nil, fmt.Errorf("file system decorator %q is configured more than once", reg.Name)
			}
		}
		sections = append(sections, section{reg: reg, args: args})
	}
	sort.Slice(sections, func(i, j int) bool {
		if sections[i].reg.Priority != sections[j].reg.Priority {
			return sections[i].reg.Priority < sections[j].reg.Priority
		}
		return sections[i].reg.Name < sections[j].reg.Name
	})
	fsys := inner
	for _, s := range sections {
		var err error
		fsys, err = s.reg.CreateFunc(ctx, fsys, s.args)
		if err != nil {
			return nil, err
		}
	}
	return fsys, nil
}
