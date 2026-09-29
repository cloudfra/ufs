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

package simplecachefs

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

const (
	kib = 1 << 10
	mib = 1 << 20
	gib = 1 << 30
)

func TestConfigDefaults(t *testing.T) {
	c := Config{StoragePath: "cache.db"}
	s, err := c.resolve()
	if err != nil {
		t.Fatalf("resolve() = %v", err)
	}
	want := settings{
		storagePath:    "cache.db",
		memorySize:     256 * mib,
		storageSize:    gib,
		maxFileSize:    26_843_545, // 0.2 × 256MiB / 2
		softLimit:      0.8,
		lowWater:       0.7,
		ttl:            0,
		sweepInterval:  30 * time.Second,
		flushThreshold: 0.05,
	}
	if s.now == nil {
		t.Error("now = nil, want time.Now")
	}
	s.now = nil
	if !reflect.DeepEqual(*s, want) {
		t.Errorf("resolve() = %+v, want %+v", *s, want)
	}
	if got, want := s.memorySoft(), int64(214_748_364); got != want {
		t.Errorf("memorySoft() = %d, want %d", got, want)
	}
	if got, want := s.memoryLow(), int64(187_904_819); got != want {
		t.Errorf("memoryLow() = %d, want %d", got, want)
	}
	if got, want := s.storageSoft(), int64(858_993_459); got != want {
		t.Errorf("storageSoft() = %d, want %d", got, want)
	}
	if got, want := s.storageLow(), int64(751_619_276); got != want {
		t.Errorf("storageLow() = %d, want %d", got, want)
	}
	if got, want := s.flushBytes(), int64(13_421_772); got != want {
		t.Errorf("flushBytes() = %d, want %d", got, want)
	}
}

func TestConfigMemoryOnlyDefaults(t *testing.T) {
	s, err := Config{}.resolve()
	if err != nil {
		t.Fatalf("resolve() = %v", err)
	}
	if !s.memoryOnly {
		t.Error("memoryOnly = false, want true")
	}
	if s.storageSize != 0 {
		t.Errorf("storageSize = %d, want 0", s.storageSize)
	}
	if got, want := s.maxFileSize, int64(26_843_545); got != want {
		t.Errorf("maxFileSize = %d, want %d", got, want)
	}
}

func TestConfigExplicitValues(t *testing.T) {
	c := Config{
		StoragePath:    "cache.db",
		MemorySize:     "64MB",
		StorageSize:    "2GiB",
		MaxFileSize:    "1MiB",
		SoftLimit:      0.9,
		LowWater:       0.5,
		Policy:         "fifo",
		TTL:            "24h",
		SweepInterval:  "5s",
		FlushThreshold: 1,
		SyncLog:        true,
	}
	s, err := c.resolve()
	if err != nil {
		t.Fatalf("resolve() = %v", err)
	}
	s.now = nil
	want := settings{
		storagePath:    "cache.db",
		memorySize:     64_000_000,
		storageSize:    2 * gib,
		maxFileSize:    mib,
		softLimit:      0.9,
		lowWater:       0.5,
		ttl:            24 * time.Hour,
		sweepInterval:  5 * time.Second,
		flushThreshold: 1,
		syncLog:        true,
	}
	if !reflect.DeepEqual(*s, want) {
		t.Errorf("resolve() = %+v, want %+v", *s, want)
	}
}

func TestConfigParseSizes(t *testing.T) {
	tests := []struct {
		in   string
		want int64
	}{
		{"256MiB", 256 * mib},
		{"256mib", 256 * mib},
		{"256MB", 256_000_000},
		{"256mb", 256_000_000},
		{"1GiB", gib},
		{"1 GiB", gib},
		{"4096", 4096},
		{"4KiB", 4 * kib},
	}
	for _, tc := range tests {
		got, err := Config{MemorySize: tc.in}.MemoryBytes()
		if err != nil || got != tc.want {
			t.Errorf("MemoryBytes(%q) = (%d, %v), want (%d, nil)", tc.in, got, err, tc.want)
		}
	}
}

func TestConfigConversionErrorsNameTheField(t *testing.T) {
	tests := []struct {
		name  string
		field string
		call  func() error
	}{
		{"memorySize", "memorySize", func() error { _, err := Config{MemorySize: "lots"}.MemoryBytes(); return err }},
		{"storageSize", "storageSize", func() error { _, err := Config{StorageSize: "1XB"}.StorageBytes(); return err }},
		{"maxFileSize", "maxFileSize", func() error { _, err := Config{MaxFileSize: "-1"}.MaxFileBytes(); return err }},
		{"maxFileSize memory default", "memorySize", func() error { _, err := Config{MemorySize: "x"}.MaxFileBytes(); return err }},
		{"maxFileSize storage default", "storageSize", func() error {
			_, err := Config{StoragePath: "a.db", StorageSize: "x"}.MaxFileBytes()
			return err
		}},
		{"ttl", "ttl", func() error { _, err := Config{TTL: "soon"}.TTLDuration(); return err }},
		{"ttl negative", "ttl", func() error { _, err := Config{TTL: "-1s"}.TTLDuration(); return err }},
		{"sweepInterval", "sweepInterval", func() error { _, err := Config{SweepInterval: "10"}.SweepIntervalDuration(); return err }},
		{"zero size", "memorySize", func() error { _, err := Config{MemorySize: "0"}.MemoryBytes(); return err }},
		{"too large", "storageSize", func() error { _, err := Config{StorageSize: "9EiB"}.StorageBytes(); return err }},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.call()
			if err == nil {
				t.Fatal("err = nil, want an error")
			}
			if !strings.Contains(err.Error(), tc.field) {
				t.Errorf("err = %q, want it to name %q", err, tc.field)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	valid := func() Config { return Config{StoragePath: "cache.db"} }
	tests := []struct {
		name    string
		modify  func(c *Config)
		wantErr string
	}{
		{"unknown policy", func(c *Config) { c.Policy = "random" }, "policy"},
		{"lru not implemented", func(c *Config) { c.Policy = "lru" }, "policy"},
		{"zero sweep interval", func(c *Config) { c.SweepInterval = "0s" }, "sweepInterval"},
		{"soft limit one", func(c *Config) { c.SoftLimit = 1 }, "softLimit"},
		{"soft limit above one", func(c *Config) { c.SoftLimit = 1.5 }, "softLimit"},
		{"soft limit negative", func(c *Config) { c.SoftLimit = -0.5 }, "softLimit"},
		{"low water one", func(c *Config) { c.LowWater = 1 }, "lowWater"},
		{"low water equals soft", func(c *Config) { c.SoftLimit, c.LowWater = 0.6, 0.6 }, "lowWater"},
		{"low water above soft", func(c *Config) { c.SoftLimit, c.LowWater = 0.6, 0.7 }, "lowWater"},
		{"flush threshold above one", func(c *Config) { c.FlushThreshold = 1.1 }, "flushThreshold"},
		{"flush threshold negative", func(c *Config) { c.FlushThreshold = -1 }, "flushThreshold"},
		{"max file equals memory gap", func(c *Config) {
			c.MemorySize, c.StorageSize, c.MaxFileSize = "1000", "10000", "200"
		}, "maxFileSize"},
		{"memory above storage low water", func(c *Config) { c.MemorySize, c.StorageSize = "800MiB", "1GiB" }, "memorySize"},
		{"bad storage size", func(c *Config) { c.StorageSize = "big" }, "storageSize"},
		{"bad ttl", func(c *Config) { c.TTL = "forever" }, "ttl"},
		{"zero max file", func(c *Config) { c.MaxFileSize = "0" }, "maxFileSize"},
		{"default max file rounds to zero", func(c *Config) { c.MemorySize, c.StorageSize = "4", "100" }, "maxFileSize"},
		{"bad memory size", func(c *Config) { c.MemorySize = "big" }, "memorySize"},
		{"bad storage size with explicit max file", func(c *Config) { c.StorageSize, c.MaxFileSize = "big", "1KiB" }, "storageSize"},
		{"bad sweep interval", func(c *Config) { c.SweepInterval = "often" }, "sweepInterval"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			c := valid()
			tc.modify(&c)
			_, err := c.resolve()
			if err == nil {
				t.Fatalf("resolve(%+v) = nil, want an error", c)
			}
			if !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("resolve() = %q, want it to mention %q", err, tc.wantErr)
			}
		})
	}
}

func TestConfigMemoryOnlyRejectsWarmFields(t *testing.T) {
	tests := []struct {
		name    string
		c       Config
		wantErr string
	}{
		{"storageSize", Config{StorageSize: "1GiB"}, "storageSize"},
		{"flushThreshold", Config{FlushThreshold: 0.1}, "flushThreshold"},
		{"syncLog", Config{SyncLog: true}, "syncLog"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := tc.c.resolve()
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Errorf("resolve() = %v, want an error mentioning %q", err, tc.wantErr)
			}
		})
	}
}

func TestConfigYAML(t *testing.T) {
	in := `
storagePath: /var/cache/ufs.db
memorySize: 128MiB
storageSize: 2GiB
maxFileSize: 4MiB
softLimit: 0.75
lowWater: 0.5
policy: fifo
ttl: 1h
sweepInterval: 10s
flushThreshold: 0.1
syncLog: true
`
	var c Config
	if err := yaml.Unmarshal([]byte(in), &c); err != nil {
		t.Fatalf("yaml.Unmarshal() = %v", err)
	}
	want := Config{
		StoragePath:    "/var/cache/ufs.db",
		MemorySize:     "128MiB",
		StorageSize:    "2GiB",
		MaxFileSize:    "4MiB",
		SoftLimit:      0.75,
		LowWater:       0.5,
		Policy:         "fifo",
		TTL:            "1h",
		SweepInterval:  "10s",
		FlushThreshold: 0.1,
		SyncLog:        true,
	}
	if !reflect.DeepEqual(c, want) {
		t.Errorf("yaml.Unmarshal() = %+v, want %+v", c, want)
	}
	if _, err := c.resolve(); err != nil {
		t.Errorf("resolve() = %v", err)
	}

	out, err := yaml.Marshal(want)
	if err != nil {
		t.Fatalf("yaml.Marshal() = %v", err)
	}
	var round Config
	if err := yaml.Unmarshal(out, &round); err != nil {
		t.Fatalf("yaml.Unmarshal(Marshal()) = %v", err)
	}
	if !reflect.DeepEqual(round, want) {
		t.Errorf("YAML round trip = %+v, want %+v", round, want)
	}
}

func TestConfigInjectedClock(t *testing.T) {
	fixed := time.Date(2026, 9, 29, 0, 0, 0, 0, time.UTC)
	s, err := Config{now: func() time.Time { return fixed }}.resolve()
	if err != nil {
		t.Fatalf("resolve() = %v", err)
	}
	if got := s.now(); !got.Equal(fixed) {
		t.Errorf("now() = %v, want %v", got, fixed)
	}
}
