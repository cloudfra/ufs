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
	"path/filepath"
	"reflect"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
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
    - readOnly: true`,
			wantCount: 1,
		},
		{
			name: "multiple entries",
			input: `- source: "memory://"
  mountPoint: "."
- source: "null://"
  mountPoint: "cache"
  options:
    - readOnly: true
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

func TestParseYAMLMountSpecOptionSections(t *testing.T) {
	t.Parallel()
	input := `- source: "memory://"
  mountPoint: "."
  options:
    - readOnly: true
    - fault:
        latency: 100ms
        errorRate: 0.25
        log: true`
	specs, err := parseYAMLMountSpec(input)
	if err != nil {
		t.Fatalf("parseYAMLMountSpec() error: %v", err)
	}
	if len(specs) != 1 {
		t.Fatalf("len(specs) = %d, want 1", len(specs))
	}
	// Options keep the order they are listed in.
	want := []MountOption{
		{Name: "readOnly", Config: true},
		{Name: "fault", Config: map[string]any{
			"latency":   "100ms",
			"errorRate": 0.25,
			"log":       true,
		}},
	}
	if got := specs[0].Options; !reflect.DeepEqual(got, want) {
		t.Errorf("Options = %#v, want %#v", got, want)
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
    - readOnly: true`
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
	if got, want := specs[2].Options, []MountOption{{Name: readOnlyOption, Config: true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("specs[2].Options = %v, want %v", got, want)
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
	if len(specs[0].Options) != 0 {
		t.Errorf("specs[0].Options = %v, want none for rw", specs[0].Options)
	}

	if specs[1].MountPoint != "data/cache" {
		t.Errorf("specs[1].MountPoint = %q, want %q (leading slash stripped)", specs[1].MountPoint, "data/cache")
	}
	if got, want := specs[1].Options, []MountOption{{Name: readOnlyOption, Config: true}}; !reflect.DeepEqual(got, want) {
		t.Errorf("specs[1].Options = %v, want %v", got, want)
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
	defer ufsTesting.ValidateClose(t, fsys)()

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

func TestNewFromFstab(t *testing.T) {
	t.Parallel()
	input := "memory:// . auto rw 0 0\nnull:// cache auto rw 0 0"
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.Create("hello.txt"); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if _, err := fsys.Stat("cache"); err != nil {
		t.Fatalf("Stat(cache): %v", err)
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
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.Stat("scratch"); err != nil {
		t.Fatalf("Stat(scratch): %v", err)
	}
}

func TestNewFromFstabLocalFS(t *testing.T) {
	t.Parallel()
	srcDir := t.TempDir()
	if err := osutil.WriteFile(filepath.Join(srcDir, "test.txt"), []byte("hello")); err != nil {
		t.Fatal(err)
	}

	input := srcDir + " . auto rw 0 0"
	fsys, err := New(t.Context(), input)
	if err != nil {
		t.Fatal(err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	data, err := fs.ReadFile(fsys, "test.txt")
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if string(data) != "hello" {
		t.Errorf("content = %q, want %q", data, "hello")
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
	defer ufsTesting.ValidateClose(t, fsys)()

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
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.Create("test.txt"); err != nil {
		t.Fatalf("Create: %v", err)
	}
}
