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
	"io/fs"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestParseYAMLMountSpec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		wantCount int
	}{
		{
			name:      "single root",
			input:     `- source: "memory://"`,
			wantCount: 1,
		},
		{
			name: "root read-only",
			input: `- source: "memory://"
  options:
    readOnly: true`,
			wantCount: 1,
		},
		{
			name: "multiple entries",
			input: `- source: "memory://"
  mountPoint: "."
- source: "null://"
  mountPoint: "cache"
  options:
    readOnly: true
- source: "memory://"
  mountPoint: "data"`,
			wantCount: 3,
		},
		{
			name:    "plain URI not YAML",
			input:   "memory://",
			wantErr: true,
		},
		{
			name:    "entry missing source",
			input:   `- mountPoint: "cache"`,
			wantErr: true,
		},
		{
			name:    "invalid YAML",
			input:   "{{{{invalid",
			wantErr: true,
		},
		{
			name:    "empty list",
			input:   "[]",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			specs, err := parseYAMLMountSpec(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseYAMLMountSpec() = %+v, want error", specs)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseYAMLMountSpec() error: %v", err)
			}
			if len(specs) != tc.wantCount {
				t.Errorf("len(specs) = %d, want %d", len(specs), tc.wantCount)
			}
		})
	}
}

func TestParseYAMLMountSpecFaultConfig(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
  options:
    fault:
      latency: 100ms
      latencyJitter: 50ms
      errorRate: 0.25
      log: true`
	specs, err := parseYAMLMountSpec(input)
	if err != nil {
		t.Fatalf("parseYAMLMountSpec() error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d, want 1", len(specs))
	}
	fault := specs[0].Options.Fault
	if fault == nil {
		t.Fatal("Options.Fault is nil, want non-nil")
	}
	if fault.Latency != 100*time.Millisecond {
		t.Errorf("Latency = %v, want 100ms", fault.Latency)
	}
	if fault.LatencyJitter != 50*time.Millisecond {
		t.Errorf("LatencyJitter = %v, want 50ms", fault.LatencyJitter)
	}
	if fault.ErrorRate != 0.25 {
		t.Errorf("ErrorRate = %v, want 0.25", fault.ErrorRate)
	}
	if !fault.Log {
		t.Error("Log = false, want true")
	}
}

func TestParseYAMLMountSpecEntries(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
- source: "null://"
  mountPoint: "cache"
- source: "memory://"
  mountPoint: "/data/files"
  options:
    readOnly: true`
	specs, err := parseYAMLMountSpec(input)
	if err != nil {
		t.Fatalf("parseYAMLMountSpec() error: %v", err)
	}
	if len(specs) != 3 {
		t.Fatalf("len(specs) = %d, want 3", len(specs))
	}

	if specs[0].Source != "memory://" {
		t.Errorf("specs[0].Source = %q, want %q", specs[0].Source, "memory://")
	}
	if specs[0].MountPoint != "." {
		t.Errorf("specs[0].MountPoint = %q, want %q", specs[0].MountPoint, ".")
	}

	if specs[1].Source != "null://" {
		t.Errorf("specs[1].Source = %q, want %q", specs[1].Source, "null://")
	}
	if specs[1].MountPoint != "cache" {
		t.Errorf("specs[1].MountPoint = %q, want %q", specs[1].MountPoint, "cache")
	}

	if specs[2].Source != "memory://" {
		t.Errorf("specs[2].Source = %q, want %q", specs[2].Source, "memory://")
	}
	if specs[2].MountPoint != "data/files" {
		t.Errorf("specs[2].MountPoint = %q, want %q (leading slash stripped)", specs[2].MountPoint, "data/files")
	}
	if !specs[2].Options.ReadOnly {
		t.Error("specs[2].Options.ReadOnly = false, want true")
	}
}

func TestParseFstabMountSpec(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name      string
		input     string
		wantErr   bool
		wantCount int
	}{
		{
			name:      "single root",
			input:     "memory:// . auto rw 0 0",
			wantCount: 1,
		},
		{
			name:      "root with slash",
			input:     "memory:// / auto rw 0 0",
			wantCount: 1,
		},
		{
			name:      "root with none",
			input:     "memory:// none auto rw 0 0",
			wantCount: 1,
		},
		{
			name:      "read-only",
			input:     "memory:// . auto ro 0 0",
			wantCount: 1,
		},
		{
			name:      "read-only in comma options",
			input:     "memory:// . auto ro,noexec 0 0",
			wantCount: 1,
		},
		{
			name:      "defaults option",
			input:     "memory:// . auto defaults 0 0",
			wantCount: 1,
		},
		{
			name: "with nested mount",
			input: `# root filesystem
memory:// . auto rw 0 0
# cache mount
null:// cache auto rw 0 0`,
			wantCount: 2,
		},
		{
			name: "multiple mounts",
			input: `memory:// / auto rw 0 0
null:// /cache auto ro 0 0
memory:// /data auto rw 0 0`,
			wantCount: 3,
		},
		{
			name:      "minimal fields",
			input:     "memory:// . auto rw",
			wantCount: 1,
		},
		{
			name:    "plain URI",
			input:   "memory://",
			wantErr: true,
		},
		{
			name:    "too few fields",
			input:   "memory:// . auto",
			wantErr: true,
		},
		{
			name:    "invalid options",
			input:   "memory:// . auto notanoption 0 0",
			wantErr: true,
		},
		{
			name:    "only comments",
			input:   "# just a comment\n# another comment",
			wantErr: true,
		},
		{
			name:    "empty",
			input:   "",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			specs, err := parseFstabMountSpec(tc.input)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("parseFstabMountSpec() = %+v, want error", specs)
				}
				return
			}
			if err != nil {
				t.Fatalf("parseFstabMountSpec() error: %v", err)
			}
			if len(specs) != tc.wantCount {
				t.Errorf("len(specs) = %d, want %d", len(specs), tc.wantCount)
			}
		})
	}
}

func TestParseFstabMountSpecFields(t *testing.T) {
	t.Parallel()
	input := "memory:// / auto rw 0 0\nnull:// /data/cache auto ro 0 0"
	specs, err := parseFstabMountSpec(input)
	if err != nil {
		t.Fatalf("parseFstabMountSpec() error: %v", err)
	}
	if len(specs) != 2 {
		t.Fatalf("len(specs) = %d, want 2", len(specs))
	}

	if specs[0].Source != "memory://" {
		t.Errorf("specs[0].Source = %q, want %q", specs[0].Source, "memory://")
	}
	if specs[0].MountPoint != "." {
		t.Errorf("specs[0].MountPoint = %q, want %q", specs[0].MountPoint, ".")
	}
	if specs[0].Options.ReadOnly {
		t.Error("specs[0].Options.ReadOnly = true, want false")
	}

	if specs[1].MountPoint != "data/cache" {
		t.Errorf("specs[1].MountPoint = %q, want %q (leading slash stripped)", specs[1].MountPoint, "data/cache")
	}
	if !specs[1].Options.ReadOnly {
		t.Error("specs[1].Options.ReadOnly = false, want true")
	}
}

func TestParseMountSpecDetection(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		input   string
		wantNil bool
		format  string
	}{
		{"YAML", "- source: \"memory://\"", false, "yaml"},
		{"fstab", "memory:// . auto rw 0 0", false, "fstab"},
		{"URI", "memory://", true, ""},
		{"bare path", "/tmp", true, ""},
		{"empty", "", true, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			specs := parseMountSpec(tc.input)
			if tc.wantNil {
				if specs != nil {
					t.Errorf("parseMountSpec(%q) = %+v, want nil", tc.input, specs)
				}
			} else {
				if specs == nil {
					t.Errorf("parseMountSpec(%q) = nil, want non-nil (%s)", tc.input, tc.format)
				}
			}
		})
	}
}

func TestNormalizeMountPoint(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input string
		want  string
	}{
		{"/", "."},
		{".", "."},
		{"none", "."},
		{"", "."},
		{"/data", "data"},
		{"/data/cache", "data/cache"},
		{"data", "data"},
		{"data/cache", "data/cache"},
	}
	for _, tc := range tests {
		got := normalizeMountPoint(tc.input)
		if got != tc.want {
			t.Errorf("normalizeMountPoint(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestNewFromYAML(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
- source: "null://"
  mountPoint: "cache"`
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if _, err := fsys.Create("hello.txt"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := fsys.Stat("hello.txt"); err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if _, err := fsys.Stat("cache"); err != nil {
		t.Fatalf("Stat(cache): %v", err)
	}
}

func TestNewFromYAMLReadOnly(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  options:
    readOnly: true`
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on read-only FS succeeded, want error")
	}
}

func TestNewFromYAMLReadOnlyMount(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
- source: "memory://"
  mountPoint: "ro-data"
  options:
    readOnly: true`
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("Create at root: %v", err)
	}

	_, err = fsys.Create("ro-data/file.txt")
	if err == nil {
		t.Fatal("Create on read-only mount succeeded, want error")
	}
}

func TestNewFromYAMLNoRoot(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "data"`
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on implicit read-only null root succeeded, want error")
	}

	if _, err := fsys.Create("data/file.txt"); err != nil {
		t.Fatalf("Create in mount: %v", err)
	}
}

func TestNewFromFstab(t *testing.T) {
	t.Parallel()
	input := "memory:// . auto rw 0 0\nnull:// cache auto rw 0 0"
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if _, err := fsys.Create("hello.txt"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := fsys.Stat("cache"); err != nil {
		t.Fatalf("Stat(cache): %v", err)
	}
}

func TestNewFromFstabReadOnly(t *testing.T) {
	t.Parallel()
	input := "memory:// . auto ro 0 0"
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on read-only FS succeeded, want error")
	}
}

func TestNewFromFstabReadOnlyMount(t *testing.T) {
	t.Parallel()
	input := "memory:// . auto rw 0 0\nmemory:// /data auto ro 0 0"
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("Create at root: %v", err)
	}

	_, err = fsys.Create("data/file.txt")
	if err == nil {
		t.Fatal("Create on read-only mount succeeded, want error")
	}
}

func TestNewFromFstabWithComments(t *testing.T) {
	t.Parallel()
	input := `# UFS mount configuration
# Root filesystem
memory:// . auto rw 0 0

# Scratch space
null:// scratch auto rw 0 0
`
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if _, err := fsys.Stat("scratch"); err != nil {
		t.Fatalf("Stat(scratch): %v", err)
	}
}

func TestNewFromFstabNoRoot(t *testing.T) {
	t.Parallel()
	input := "memory:// data auto rw 0 0"
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on implicit read-only null root succeeded, want error")
	}

	if _, err := fsys.Create("data/file.txt"); err != nil {
		t.Fatalf("Create in mount: %v", err)
	}
}

func TestNewFromFstabLocalFS(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(srcDir, "test.txt"), []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	input := srcDir + " . auto rw 0 0"
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	data, err := fs.ReadFile(fsys, "test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
	}
}

func TestNewFromYAMLFaultInjector(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
  options:
    fault:
      errorRate: 1.0
      errorMessage: "disk on fire"`
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on fault-injected FS succeeded, want error")
	}
}

func TestNewFromYAMLFaultInjectorMount(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
- source: "memory://"
  mountPoint: "unstable"
  options:
    fault:
      errorRate: 1.0`
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("Create at root: %v", err)
	}

	_, err = fsys.Create("unstable/file.txt")
	if err == nil {
		t.Fatal("Create on fault-injected mount succeeded, want error")
	}
}

func TestNewFromYAMLFaultAndReadOnly(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
  options:
    readOnly: true
    fault:
      latency: 1ms`
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	_, err = fsys.Create("file.txt")
	if err == nil {
		t.Fatal("Create on read-only + fault FS succeeded, want error")
	}
}

func TestNewFromFstabFaultInjector(t *testing.T) {
	t.Parallel()
	// fstab doesn't support fault config — only YAML does. This test verifies
	// fstab continues to work without fault config.
	input := "memory:// . auto rw 0 0"
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if _, err := fsys.Create("file.txt"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}

func TestNewURIStillWorks(t *testing.T) {
	t.Parallel()
	fsys, err := New(t.Context(), "memory://")
	if err != nil {
		t.Fatal(err)
	}
	defer fsys.Close()

	if _, err := fsys.Create("test.txt"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
