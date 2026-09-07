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
	crand "crypto/rand"
	"fmt"
	"io/fs"
	"log/slog"
	"math/rand/v2"
	"net/url"
	"sync"
	"syscall"
	"time"
)

var _ FS = (*faultFS)(nil)

// faultErrors is a set of realistic errors that applications commonly
// encounter from file system operations.
//
// TODO: allow callers to supply a custom error set via FaultConfig.
var faultErrors = []error{
	syscall.EIO,
	syscall.ENOSPC,
	syscall.EACCES,
	syscall.EDQUOT,
	syscall.ECONNRESET,
}

// FaultConfig controls fault injection behavior for a [faultFS] wrapper.
type FaultConfig struct {
	// Latency is a fixed delay added before each operation.
	Latency time.Duration `yaml:"latency,omitempty"`

	// LatencyJitter is the maximum additional random delay added on top of
	// Latency. The actual jitter for each call is drawn uniformly from
	// [0, LatencyJitter).
	LatencyJitter time.Duration `yaml:"latencyJitter,omitempty"`

	// ErrorRate is the probability [0.0, 1.0] that an operation returns an
	// injected error instead of delegating to the inner FS. Values above
	// 1.0 are clamped to 1.0; negative values are clamped to 0.0.
	ErrorRate float64 `yaml:"errorRate,omitempty"`

	// Log enables structured logging each time a fault is injected.
	Log bool `yaml:"log,omitempty"`
}

func (c *FaultConfig) isZero() bool {
	return c == nil || (c.Latency == 0 && c.LatencyJitter == 0 && c.ErrorRate == 0)
}

func (c *FaultConfig) clampedErrorRate() float64 {
	switch {
	case c.ErrorRate > 1.0:
		return 1.0
	case c.ErrorRate < 0:
		return 0
	default:
		return c.ErrorRate
	}
}

func newCryptoRand() *rand.Rand {
	var seed [32]byte
	_, _ = crand.Read(seed[:])
	return rand.New(rand.NewChaCha8(seed))
}

type faultFS struct {
	inner     FS
	cfg       FaultConfig
	errorRate float64
	// mu guards rng, which is not safe for concurrent use.
	mu  sync.Mutex
	rng *rand.Rand
}

// FaultInjector wraps inner as an [FS] that injects configurable latency and
// errors. Close always delegates to inner without fault injection.
func FaultInjector(inner FS, cfg FaultConfig) FS {
	return &faultFS{
		inner:     inner,
		cfg:       cfg,
		errorRate: cfg.clampedErrorRate(),
		rng:       newCryptoRand(),
	}
}

func (fsys *faultFS) getDeviceInfo() map[string]deviceInfo {
	return fsys.inner.getDeviceInfo()
}

func (fsys *faultFS) URI() *url.URL {
	return fsys.inner.URI()
}

func (fsys *faultFS) String() string {
	return fmt.Sprintf("faultFS(%s)", fsys.inner)
}

func (fsys *faultFS) readOnlyAt(name string) bool {
	return isReadOnlyAt(fsys.inner, name)
}

func (fsys *faultFS) maybeInjectFault(op, name string) error {
	if err := validPath(op, name); err != nil {
		return err
	}

	fsys.mu.Lock()
	var jitter time.Duration
	if fsys.cfg.LatencyJitter > 0 {
		jitter = time.Duration(fsys.rng.Int64N(int64(fsys.cfg.LatencyJitter)))
	}
	var fail bool
	if fsys.errorRate > 0 {
		fail = fsys.rng.Float64() < fsys.errorRate
	}
	var faultErr error
	if fail {
		faultErr = faultErrors[fsys.rng.IntN(len(faultErrors))]
	}
	fsys.mu.Unlock()

	if d := fsys.cfg.Latency + jitter; d > 0 {
		time.Sleep(d)
	}
	if fail {
		if fsys.cfg.Log {
			slog.Info("fault injected", "op", op, "path", name, "error", faultErr)
		}
		return pathError(op, name, faultErr)
	}
	return nil
}

func (fsys *faultFS) Open(name string) (fs.File, error) {
	if err := fsys.maybeInjectFault("open", name); err != nil {
		return nil, err
	}
	return fsys.inner.Open(name)
}

func (fsys *faultFS) Close() error {
	return fsys.inner.Close()
}

func (fsys *faultFS) Stat(name string) (fs.FileInfo, error) {
	if err := fsys.maybeInjectFault("stat", name); err != nil {
		return nil, err
	}
	return fsys.inner.Stat(name)
}

func (fsys *faultFS) Lstat(name string) (fs.FileInfo, error) {
	if err := fsys.maybeInjectFault("lstat", name); err != nil {
		return nil, err
	}
	return fsys.inner.Lstat(name)
}

func (fsys *faultFS) ReadDir(name string) ([]fs.DirEntry, error) {
	if err := fsys.maybeInjectFault("readdir", name); err != nil {
		return nil, err
	}
	return fsys.inner.ReadDir(name)
}

func (fsys *faultFS) ReadFile(name string) ([]byte, error) {
	if err := fsys.maybeInjectFault("readfile", name); err != nil {
		return nil, err
	}
	return fsys.inner.ReadFile(name)
}

func (fsys *faultFS) ReadLink(name string) (string, error) {
	if err := fsys.maybeInjectFault("readlink", name); err != nil {
		return "", err
	}
	return fsys.inner.ReadLink(name)
}

func (fsys *faultFS) Create(name string) (File, error) {
	if err := fsys.maybeInjectFault("create", name); err != nil {
		return nil, err
	}
	return fsys.inner.Create(name)
}

func (fsys *faultFS) MkdirAll(name string, perm fs.FileMode) error {
	if err := fsys.maybeInjectFault("mkdir", name); err != nil {
		return err
	}
	return fsys.inner.MkdirAll(name, perm)
}

func (fsys *faultFS) Remove(name string) error {
	if err := fsys.maybeInjectFault("remove", name); err != nil {
		return err
	}
	return fsys.inner.Remove(name)
}

func (fsys *faultFS) RemoveAll(name string) error {
	if err := fsys.maybeInjectFault("removeall", name); err != nil {
		return err
	}
	return fsys.inner.RemoveAll(name)
}
