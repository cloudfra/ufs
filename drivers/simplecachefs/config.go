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

// Package simplecachefs provides a lossy two-tier cache file system: an
// in-memory hot tier and an optional bolt warm tier. It is configured
// programmatically with a [Config]; it has no URI scheme.
package simplecachefs

import (
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/dustin/go-humanize"
)

// PolicyFIFO evicts the entries with the oldest modification time first. It is
// the only supported [Config.Policy].
const PolicyFIFO = "fifo"

// Defaults applied to empty or zero [Config] fields.
const (
	DefaultMemorySize     = "256MiB"
	DefaultStorageSize    = "1GiB"
	DefaultSoftLimit      = 0.8
	DefaultLowWater       = 0.7
	DefaultPolicy         = PolicyFIFO
	DefaultTTL            = "0"
	DefaultSweepInterval  = "30s"
	DefaultFlushThreshold = 0.05
)

// Config configures a simplecachefs file system. Every field is a
// YAML-serializable value: sizes and durations are human-readable strings and
// fractions are floats from 0 to 1. Empty strings and zero fractions take the
// Default* values. Use the conversion methods to read the parsed values.
type Config struct {
	// StoragePath is the bolt file of the warm tier. Empty means memory-only.
	StoragePath string `yaml:"storagePath"`
	// MemorySize is the hot tier's hard limit, such as "256MiB".
	MemorySize string `yaml:"memorySize"`
	// StorageSize is the warm tier's hard limit, such as "1GiB".
	StorageSize string `yaml:"storageSize"`
	// MaxFileSize is the largest file the cache accepts. The default is half of
	// the smallest tier's soft-to-hard gap.
	MaxFileSize string `yaml:"maxFileSize"`
	// SoftLimit is the fraction of a tier's size that starts a sweep.
	SoftLimit float64 `yaml:"softLimit"`
	// LowWater is the fraction of a tier's size a sweep evicts down to.
	LowWater float64 `yaml:"lowWater"`
	// Policy is the eviction policy. Only "fifo" is supported.
	Policy string `yaml:"policy"`
	// TTL is how long after its last write an entry expires. "0" disables it.
	TTL string `yaml:"ttl"`
	// SweepInterval is the period of the maintenance goroutine.
	SweepInterval string `yaml:"sweepInterval"`
	// FlushThreshold flushes the hot tier once dirty bytes reach this fraction
	// of MemorySize.
	FlushThreshold float64 `yaml:"flushThreshold"`
	// SyncLog records writes synchronously instead of on a background
	// goroutine.
	SyncLog bool `yaml:"syncLog"`

	// now is the clock; nil means time.Now. Tests inject their own.
	now func() time.Time
}

// MemoryOnly reports whether the cache has no warm tier.
func (c Config) MemoryOnly() bool {
	return c.StoragePath == ""
}

// MemoryBytes returns MemorySize in bytes.
func (c Config) MemoryBytes() (int64, error) {
	return parseSize("memorySize", c.MemorySize, DefaultMemorySize)
}

// StorageBytes returns StorageSize in bytes.
func (c Config) StorageBytes() (int64, error) {
	return parseSize("storageSize", c.StorageSize, DefaultStorageSize)
}

// MaxFileBytes returns MaxFileSize in bytes. When MaxFileSize is empty it
// returns half of the smallest tier's soft-to-hard gap.
func (c Config) MaxFileBytes() (int64, error) {
	if c.MaxFileSize != "" {
		return parseSize("maxFileSize", c.MaxFileSize, "")
	}
	smallest, err := c.MemoryBytes()
	if err != nil {
		return 0, err
	}
	if !c.MemoryOnly() {
		storage, err := c.StorageBytes()
		if err != nil {
			return 0, err
		}
		smallest = min(smallest, storage)
	}
	return int64((1 - c.SoftLimitFraction()) * float64(smallest) / 2), nil
}

// TTLDuration returns TTL. Zero means entries never expire.
func (c Config) TTLDuration() (time.Duration, error) {
	return parseDuration("ttl", c.TTL, DefaultTTL)
}

// SweepIntervalDuration returns SweepInterval.
func (c Config) SweepIntervalDuration() (time.Duration, error) {
	return parseDuration("sweepInterval", c.SweepInterval, DefaultSweepInterval)
}

// SoftLimitFraction returns SoftLimit, or its default when zero.
func (c Config) SoftLimitFraction() float64 {
	return orDefault(c.SoftLimit, DefaultSoftLimit)
}

// LowWaterFraction returns LowWater, or its default when zero.
func (c Config) LowWaterFraction() float64 {
	return orDefault(c.LowWater, DefaultLowWater)
}

// FlushThresholdFraction returns FlushThreshold, or its default when zero.
func (c Config) FlushThresholdFraction() float64 {
	return orDefault(c.FlushThreshold, DefaultFlushThreshold)
}

// PolicyName returns Policy, or its default when empty.
func (c Config) PolicyName() string {
	if c.Policy == "" {
		return DefaultPolicy
	}
	return c.Policy
}

// settings is a validated, parsed Config.
type settings struct {
	memoryOnly     bool
	storagePath    string
	memorySize     int64
	storageSize    int64
	maxFileSize    int64
	softLimit      float64
	lowWater       float64
	ttl            time.Duration
	sweepInterval  time.Duration
	flushThreshold float64
	syncLog        bool
	now            func() time.Time
}

// memorySoft returns the hot tier's soft limit in bytes.
func (s *settings) memorySoft() int64 { return fraction(s.memorySize, s.softLimit) }

// memoryLow returns the hot tier's low-water mark in bytes.
func (s *settings) memoryLow() int64 { return fraction(s.memorySize, s.lowWater) }

// storageSoft returns the warm tier's soft limit in bytes.
func (s *settings) storageSoft() int64 { return fraction(s.storageSize, s.softLimit) }

// storageLow returns the warm tier's low-water mark in bytes.
func (s *settings) storageLow() int64 { return fraction(s.storageSize, s.lowWater) }

// flushBytes returns the dirty byte count that triggers a flush.
func (s *settings) flushBytes() int64 { return fraction(s.memorySize, s.flushThreshold) }

// resolve parses and validates c. It rejects any nonsensical or ambiguous
// value, so that a Config that resolves always describes a cache that can make
// progress.
func (c Config) resolve() (*settings, error) {
	s := &settings{
		memoryOnly:     c.MemoryOnly(),
		storagePath:    c.StoragePath,
		softLimit:      c.SoftLimitFraction(),
		lowWater:       c.LowWaterFraction(),
		flushThreshold: c.FlushThresholdFraction(),
		syncLog:        c.SyncLog,
		now:            c.now,
	}
	if s.now == nil {
		s.now = time.Now
	}
	var err error
	if s.memorySize, err = c.MemoryBytes(); err != nil {
		return nil, err
	}
	if s.maxFileSize, err = c.MaxFileBytes(); err != nil {
		return nil, err
	}
	if s.ttl, err = c.TTLDuration(); err != nil {
		return nil, err
	}
	if s.sweepInterval, err = c.SweepIntervalDuration(); err != nil {
		return nil, err
	}
	if s.sweepInterval == 0 {
		return nil, errors.New("simplecachefs: sweepInterval must be greater than 0")
	}
	if policy := c.PolicyName(); policy != PolicyFIFO {
		return nil, fmt.Errorf("simplecachefs: policy %q is not supported, want %q", policy, PolicyFIFO)
	}
	if err := checkFraction("softLimit", s.softLimit, false); err != nil {
		return nil, err
	}
	if err := checkFraction("lowWater", s.lowWater, false); err != nil {
		return nil, err
	}
	if s.lowWater >= s.softLimit {
		return nil, fmt.Errorf("simplecachefs: lowWater (%v) must be less than softLimit (%v)", s.lowWater, s.softLimit)
	}
	if s.maxFileSize == 0 {
		return nil, errors.New("simplecachefs: maxFileSize must be greater than 0")
	}
	if err := checkMaxFile("memorySize", s.memorySize, s.maxFileSize, s.softLimit); err != nil {
		return nil, err
	}

	if s.memoryOnly {
		switch {
		case c.StorageSize != "":
			return nil, errors.New("simplecachefs: storageSize requires storagePath")
		case c.FlushThreshold != 0:
			return nil, errors.New("simplecachefs: flushThreshold requires storagePath")
		case c.SyncLog:
			return nil, errors.New("simplecachefs: syncLog requires storagePath")
		}
		return s, nil
	}

	if s.storageSize, err = c.StorageBytes(); err != nil {
		return nil, err
	}
	if err := checkFraction("flushThreshold", s.flushThreshold, true); err != nil {
		return nil, err
	}
	// A flush of a full hot tier must fit in the warm tier after a sweep. This
	// also makes the warm tier larger than the hot tier, so its soft-to-hard
	// gap is wider and the maxFileSize check above covers both tiers.
	if s.memorySize > s.storageLow() {
		return nil, fmt.Errorf("simplecachefs: memorySize (%d bytes) must not exceed lowWater × storageSize (%d bytes)", s.memorySize, s.storageLow())
	}
	return s, nil
}

// parseSize parses a human-readable size such as "256MiB" or "1GB" (see
// humanize.ParseBytes). An empty value uses def. The result must be greater
// than zero and fit in an int64.
func parseSize(field, value, def string) (int64, error) {
	if value == "" {
		value = def
	}
	n, err := humanize.ParseBytes(value)
	if err != nil {
		return 0, fmt.Errorf("simplecachefs: %s %q: %w", field, value, err)
	}
	if n == 0 {
		return 0, fmt.Errorf("simplecachefs: %s %q must be greater than 0", field, value)
	}
	if n > math.MaxInt64 {
		return 0, fmt.Errorf("simplecachefs: %s %q is too large", field, value)
	}
	return int64(n), nil
}

// parseDuration parses a duration with time.ParseDuration. An empty value uses
// def. Negative durations are rejected.
func parseDuration(field, value, def string) (time.Duration, error) {
	if value == "" {
		value = def
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("simplecachefs: %s %q: %w", field, value, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("simplecachefs: %s %q must not be negative", field, value)
	}
	return d, nil
}

// checkFraction reports an error unless v is in (0, 1), or (0, 1] when
// allowOne is set.
func checkFraction(field string, v float64, allowOne bool) error {
	if v > 0 && (v < 1 || (allowOne && v == 1)) {
		return nil
	}
	if allowOne {
		return fmt.Errorf("simplecachefs: %s (%v) must be in (0, 1]", field, v)
	}
	return fmt.Errorf("simplecachefs: %s (%v) must be in (0, 1)", field, v)
}

// checkMaxFile reports an error unless maxFile is strictly smaller than the
// tier's soft-to-hard gap, so a single write that crosses the soft limit can't
// also cross the hard limit.
func checkMaxFile(field string, size, maxFile int64, softLimit float64) error {
	if gap := (1 - softLimit) * float64(size); float64(maxFile) >= gap {
		return fmt.Errorf("simplecachefs: maxFileSize (%d bytes) must be less than (1 - softLimit) × %s (%d bytes)", maxFile, field, int64(gap))
	}
	return nil
}

// fraction returns f × n, rounded down.
func fraction(n int64, f float64) int64 {
	return int64(f * float64(n))
}

func orDefault(v, def float64) float64 {
	if v == 0 {
		return def
	}
	return v
}
