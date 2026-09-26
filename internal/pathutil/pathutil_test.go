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

package pathutil

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path"
	"runtime"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
)

var pathTestCases = []struct {
	input          string
	wantTrimSlash  string
	wantSplitPath  []string
	wantIsCwd      bool
	wantIsDirName  bool
	wantCoerceUnix string
}{
	{
		input:          "",
		wantTrimSlash:  "",
		wantSplitPath:  []string{""},
		wantIsCwd:      true,
		wantIsDirName:  true,
		wantCoerceUnix: "",
	},
	{
		input:          CwdPath,
		wantTrimSlash:  CwdPath,
		wantSplitPath:  []string{CwdPath},
		wantIsCwd:      true,
		wantIsDirName:  true,
		wantCoerceUnix: CwdPath,
	},
	{
		input:          "/",
		wantTrimSlash:  "",
		wantSplitPath:  []string{""},
		wantIsCwd:      false,
		wantIsDirName:  true,
		wantCoerceUnix: "/",
	},
	{
		input:          "abc",
		wantTrimSlash:  "abc",
		wantSplitPath:  []string{"abc"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "abc",
	},
	{
		input:          "/abc/d/",
		wantTrimSlash:  "abc/d",
		wantSplitPath:  []string{"abc", "d"},
		wantIsCwd:      false,
		wantIsDirName:  true,
		wantCoerceUnix: "/abc/d/",
	},
	{
		input:          "abc/d/",
		wantTrimSlash:  "abc/d",
		wantSplitPath:  []string{"abc", "d"},
		wantIsCwd:      false,
		wantIsDirName:  true,
		wantCoerceUnix: "abc/d/",
	},
	{
		input:          "/abc/d",
		wantTrimSlash:  "abc/d",
		wantSplitPath:  []string{"abc", "d"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "/abc/d",
	},
	{
		input:          "abc\\d",
		wantTrimSlash:  "abc\\d",
		wantSplitPath:  []string{"abc\\d"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "abc/d",
	},
	{
		input:          "\\abc\\",
		wantTrimSlash:  "abc",
		wantSplitPath:  []string{"abc"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "/abc/",
	},
	{
		input:          "ok.tar",
		wantTrimSlash:  "ok.tar",
		wantSplitPath:  []string{"ok.tar"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.tar",
	},
	{
		input:          "ok.tar.gz",
		wantTrimSlash:  "ok.tar.gz",
		wantSplitPath:  []string{"ok.tar.gz"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.tar.gz",
	},
	{
		input:          "ok.tar.bz2",
		wantTrimSlash:  "ok.tar.bz2",
		wantSplitPath:  []string{"ok.tar.bz2"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.tar.bz2",
	},
	{
		input:          "ok.tar.xz",
		wantTrimSlash:  "ok.tar.xz",
		wantSplitPath:  []string{"ok.tar.xz"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.tar.xz",
	},
	{
		input:          "ok.tar.lz4",
		wantTrimSlash:  "ok.tar.lz4",
		wantSplitPath:  []string{"ok.tar.lz4"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.tar.lz4",
	},
	{
		input:          "ok.tar.br",
		wantTrimSlash:  "ok.tar.br",
		wantSplitPath:  []string{"ok.tar.br"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.tar.br",
	},
	{
		input:          "ok.tar.zst",
		wantTrimSlash:  "ok.tar.zst",
		wantSplitPath:  []string{"ok.tar.zst"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.tar.zst",
	},
	{
		input:          "ok.zip",
		wantTrimSlash:  "ok.zip",
		wantSplitPath:  []string{"ok.zip"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.zip",
	},
	{
		input:          "ok.tar.lzma",
		wantTrimSlash:  "ok.tar.lzma",
		wantSplitPath:  []string{"ok.tar.lzma"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.tar.lzma",
	},
	{
		input:          "ok.7z",
		wantTrimSlash:  "ok.7z",
		wantSplitPath:  []string{"ok.7z"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.7z",
	},
	{
		input:          "ok.7Z",
		wantTrimSlash:  "ok.7Z",
		wantSplitPath:  []string{"ok.7Z"},
		wantIsCwd:      false,
		wantIsDirName:  false,
		wantCoerceUnix: "ok.7Z",
	},
}

func TestRemovePrefix(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		path       string
		removePath string
		want       string
		wantOk     bool
	}{
		{
			path:       "",
			removePath: "",
			want:       CwdPath,
			wantOk:     true,
		},
		{
			path:       CwdPath,
			removePath: "",
			want:       CwdPath,
			wantOk:     true,
		},
		{
			path:       "",
			removePath: CwdPath,
			want:       CwdPath,
			wantOk:     true,
		},
		{
			path:       CwdPath,
			removePath: "abc/def",
			want:       CwdPath,
			wantOk:     false,
		},
		{
			path:       "abc/def",
			removePath: "",
			want:       "abc/def",
			wantOk:     true,
		},
		{
			path:       "abc/def",
			removePath: "abc",
			want:       "def",
			wantOk:     true,
		},
		{
			path:       "abc/def",
			removePath: "abc/d",
			want:       "abc/def",
			wantOk:     false,
		},
		{
			path:       "abc/def",
			removePath: "abc/def",
			want:       CwdPath,
			wantOk:     true,
		},
		{
			path:       "a/b/c",
			removePath: "a/b",
			want:       "c",
			wantOk:     true,
		},
		{
			path:       "a/b/c/",
			removePath: "a/b/",
			want:       "c",
			wantOk:     true,
		},
		{
			path:       "a/b/c",
			removePath: "a/b/",
			want:       "c",
			wantOk:     true,
		},
		{
			path:       "a/b/c/",
			removePath: "a/b",
			want:       "c",
			wantOk:     true,
		},
		{
			path:       "a/b",
			removePath: "a/b/c",
			want:       "a/b",
			wantOk:     false,
		},
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("%s - %s", tc.path, tc.removePath), func(t *testing.T) {
			t.Parallel()
			got, gotOk := RemovePrefix(tc.path, tc.removePath)
			if got != tc.want {
				t.Errorf("path: got: %q, want: %q", got, tc.want)
			}
			if gotOk != tc.wantOk {
				t.Errorf("ok: got: %t, want: %t", gotOk, tc.wantOk)
			}
		})
	}
}

func TestTrimSlash(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := TrimSlash(tc.input); got != tc.wantTrimSlash {
				t.Errorf("TrimSlash(%q) got: %v, want: %v", tc.input, got, tc.wantTrimSlash)
			}
		})
	}
}

func TestSplit(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := Split(tc.input)
			if diff := cmp.Diff(got, tc.wantSplitPath); diff != "" {
				t.Errorf("Split(%q) got: %v, want: %v, diff: %s", tc.input, got, tc.wantTrimSlash, diff)
			}
		})
	}
}

func TestIsCwd(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			t.Parallel()
			if got := IsCwd(tc.input); got != tc.wantIsCwd {
				t.Errorf("IsCwd(%q) got: %v, want: %v", tc.input, got, tc.wantIsCwd)
			}
		})
	}
}

func TestIsDirName(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(fmt.Sprintf("%q", tc.input), func(t *testing.T) {
			t.Parallel()
			if got := IsDirName(tc.input); got != tc.wantIsDirName {
				t.Errorf("IsDirName(%q) got: %v, want: %v", tc.input, got, tc.wantIsDirName)
			}
		})
	}
}

func TestValidate(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input   string
		wantErr bool
	}{
		{input: CwdPath, wantErr: false},
		{input: "./.", wantErr: true},
		{input: "a\\b\\.\\..\\c", wantErr: false},
		{input: "a/b/./../c", wantErr: true},
		{input: "a/b/../c", wantErr: true},
		{input: "C:/", wantErr: true},
		{input: "C:\\", wantErr: false},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			err := Validate("open", tc.input)

			if tc.wantErr {
				if err == nil || !strings.Contains(err.Error(), "is not a valid path for") {
					t.Errorf("Validate(open, %s) expected to contain 'is not a valid path for', got: %q", tc.input, err)
				}
			} else {
				if err != nil {
					t.Errorf("Validate(open, %s) returned error %q, want nil", tc.input, err)
				}
			}
		})
	}
}

func TestCoerceUnix(t *testing.T) {
	t.Parallel()
	for _, tc := range pathTestCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			got := CoerceUnix(tc.input)
			if got != tc.wantCoerceUnix {
				t.Errorf("coerceUnix(%q) got: %q, want: %q", tc.input, got, tc.wantCoerceUnix)
			}
		})
	}
}

func TestTempDirUsesForwardSlashes(t *testing.T) {
	t.Parallel()
	got := TempDir()
	if strings.Contains(got, WindowsSeparator) {
		t.Errorf("TempDir() = %q, want no %q", got, WindowsSeparator)
	}
	if want := CoerceUnix(os.TempDir()); got != want {
		t.Errorf("TempDir() = %q, want %q", got, want)
	}
}

func TestRemovePrefixEdgeCases(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		name       string
		path       string
		removePath string
		want       string
		wantOk     bool
	}{
		{"sibling with shared prefix", "abc", "ab", "abc", false},
		{"sibling directory with shared prefix", "abc/def", "ab", "abc/def", false},
		{"unclean path", "a/./b/../b/c", "a/b", "c", true},
		{"unclean prefix", "a/b/c", "a/./x/../b", "c", true},
		{"leading dot slash", "./a/b", "a", "b", true},
		{"double slash", "a//b", "a", "b", true},
		{"deep remainder", "a/b/c/d/e", "a", "b/c/d/e", true},
		{"prefix longer than path", "a", "a/b", "a", false},
		{"unrelated", "x/y", "a", "x/y", false},
		{"dotdot prefix", "../a", "..", "a", true},
		{"absolute", "/a/b", "/a", "b", true},
		{"absolute root equal", "/", "/", CwdPath, true},
		{"cwd prefix unclean path", "a/../b", CwdPath, "b", true},
		{"empty prefix keeps path clean", "a/b/", "", "a/b", true},
		{"case sensitive", "A/b", "a", "A/b", false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, gotOk := RemovePrefix(tc.path, tc.removePath)
			if got != tc.want || gotOk != tc.wantOk {
				t.Errorf("RemovePrefix(%q, %q) = %q, %t, want %q, %t", tc.path, tc.removePath, got, gotOk, tc.want, tc.wantOk)
			}
		})
	}
}

func TestTrimSlashEdgeCases(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input string
		want  string
	}{
		{"//a//", "a"},
		{"\\\\a\\\\", "a"},
		{"/\\/a\\/\\", "a"},
		{"a/b", "a/b"},
		{"a\\b", "a\\b"},
		{"/a/b/", "a/b"},
		{"///", ""},
		{" /a/ ", " /a/ "},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := TrimSlash(tc.input); got != tc.want {
				t.Errorf("TrimSlash(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestSplitEdgeCases(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input string
		want  []string
	}{
		{"a/b/c", []string{"a", "b", "c"}},
		{"/a/b/c/", []string{"a", "b", "c"}},
		{"a//b", []string{"a", "", "b"}},
		{"a\\b", []string{"a\\b"}},
		{"\\a/b\\", []string{"a", "b"}},
		{"///", []string{""}},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if diff := cmp.Diff(tc.want, Split(tc.input)); diff != "" {
				t.Errorf("Split(%q) mismatch (-want +got):\n%s", tc.input, diff)
			}
		})
	}
}

func TestIsCwdEdgeCases(t *testing.T) {
	t.Parallel()
	for _, name := range []string{"./", "/", "..", "./.", " ", "\\"} {
		if IsCwd(name) {
			t.Errorf("IsCwd(%q) = true, want false", name)
		}
	}
}

func TestIsDirNameEdgeCases(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input string
		want  bool
	}{
		{"a/", true},
		{"a/b/", true},
		{"/", true},
		{"./", true},
		{"a", false},
		{"a\\", false},
		{"..", false},
		{"a/.", false},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			if got := IsDirName(tc.input); got != tc.want {
				t.Errorf("IsDirName(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

func TestValidateEdgeCases(t *testing.T) {
	t.Parallel()
	testCases := []struct {
		input   string
		wantErr bool
	}{
		{input: "a", wantErr: false},
		{input: "a/b/c", wantErr: false},
		{input: "a.b/c.d", wantErr: false},
		{input: "..a/b..", wantErr: false},
		{input: "a b/c d", wantErr: false},
		{input: "", wantErr: true},
		{input: "/", wantErr: true},
		{input: "/a", wantErr: true},
		{input: "a/", wantErr: true},
		{input: "a//b", wantErr: true},
		{input: "..", wantErr: true},
		{input: "../a", wantErr: true},
		{input: "a/..", wantErr: true},
		{input: "a/.", wantErr: true},
		{input: "./a", wantErr: true},
	}
	for _, tc := range testCases {
		t.Run(tc.input, func(t *testing.T) {
			t.Parallel()
			err := Validate("stat", tc.input)
			if (err != nil) != tc.wantErr {
				t.Errorf("Validate(stat, %q) = %v, wantErr = %v", tc.input, err, tc.wantErr)
			}
		})
	}
}

func TestValidateErrorType(t *testing.T) {
	t.Parallel()
	err := Validate("readdir", "a/../b")
	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Fatalf("Validate() error = %T, want *fs.PathError", err)
	}
	if pathErr.Op != "readdir" {
		t.Errorf("Op = %q, want %q", pathErr.Op, "readdir")
	}
	if pathErr.Path != "a/../b" {
		t.Errorf("Path = %q, want %q", pathErr.Path, "a/../b")
	}
	if !errors.Is(err, fs.ErrInvalid) {
		t.Errorf("errors.Is(%v, fs.ErrInvalid) = false, want true", err)
	}
	if !strings.Contains(err.Error(), runtime.GOOS) {
		t.Errorf("error %q does not name the OS %q", err, runtime.GOOS)
	}
}

func TestSeparators(t *testing.T) {
	t.Parallel()
	if UnixSeparator != "/" {
		t.Errorf("UnixSeparator = %q, want %q", UnixSeparator, "/")
	}
	if WindowsSeparator != `\` {
		t.Errorf("WindowsSeparator = %q, want %q", WindowsSeparator, `\`)
	}
	if !fs.ValidPath(CwdPath) {
		t.Errorf("fs.ValidPath(CwdPath) = false, want true")
	}
}

// fuzzSeeds are shared by the fuzz targets below.
var fuzzSeeds = []string{
	"", ".", "..", "/", "\\", "a", "a/b", "a\\b", "/a/", "\\a\\", "a//b",
	"a/./b", "a/../b", "./a", "C:\\x", "ok.tar.gz", "a/b/", " a ", "é/ü",
}

func FuzzCoerceUnix(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		got := CoerceUnix(name)
		if strings.Contains(got, WindowsSeparator) {
			t.Errorf("CoerceUnix(%q) = %q still contains a backslash", name, got)
		}
		if len(got) != len(name) {
			t.Errorf("CoerceUnix(%q) changed the length from %d to %d", name, len(name), len(got))
		}
		if strings.Count(got, UnixSeparator) != strings.Count(name, UnixSeparator)+strings.Count(name, WindowsSeparator) {
			t.Errorf("CoerceUnix(%q) = %q, separator count changed", name, got)
		}
		if again := CoerceUnix(got); again != got {
			t.Errorf("CoerceUnix is not idempotent: %q -> %q -> %q", name, got, again)
		}
	})
}

func FuzzTrimSlash(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		got := TrimSlash(name)
		if strings.HasPrefix(got, UnixSeparator) || strings.HasPrefix(got, WindowsSeparator) ||
			strings.HasSuffix(got, UnixSeparator) || strings.HasSuffix(got, WindowsSeparator) {
			t.Errorf("TrimSlash(%q) = %q still has a leading or trailing separator", name, got)
		}
		if !strings.Contains(name, got) {
			t.Errorf("TrimSlash(%q) = %q is not a substring of the input", name, got)
		}
		if again := TrimSlash(got); again != got {
			t.Errorf("TrimSlash is not idempotent: %q -> %q -> %q", name, got, again)
		}
	})
}

func FuzzSplit(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		parts := Split(name)
		if len(parts) == 0 {
			t.Fatalf("Split(%q) returned no components", name)
		}
		if joined := strings.Join(parts, UnixSeparator); joined != TrimSlash(name) {
			t.Errorf("Join(Split(%q)) = %q, want TrimSlash = %q", name, joined, TrimSlash(name))
		}
		for _, p := range parts {
			if strings.Contains(p, UnixSeparator) {
				t.Errorf("Split(%q) component %q contains a separator", name, p)
			}
		}
		// Backslashes are ordinary characters to fs.ValidPath but TrimSlash
		// strips them from the ends, so only check names without them.
		if fs.ValidPath(name) && !IsCwd(name) && !strings.Contains(name, WindowsSeparator) {
			for _, p := range parts {
				if p == "" || p == "." || p == ".." {
					t.Errorf("Split(valid %q) produced component %q", name, p)
				}
			}
		}
	})
}

func FuzzRemovePrefix(f *testing.F) {
	for _, a := range fuzzSeeds {
		for _, b := range []string{"", ".", "a", "a/b", "/", ".."} {
			f.Add(a, b)
		}
	}
	f.Fuzz(func(t *testing.T, name, removePath string) {
		got, ok := RemovePrefix(name, removePath)
		cleanName := path.Clean(name)
		if !ok {
			if got != cleanName {
				t.Errorf("RemovePrefix(%q, %q) = %q, false, want the cleaned name %q", name, removePath, got, cleanName)
			}
			return
		}
		if got == "" {
			t.Fatalf("RemovePrefix(%q, %q) = \"\", true, want a non-empty remainder", name, removePath)
		}
		if IsCwd(path.Clean(removePath)) {
			if got != cleanName {
				t.Errorf("RemovePrefix(%q, %q) = %q, want the cleaned name %q", name, removePath, got, cleanName)
			}
			return
		}
		if rebuilt := path.Join(path.Clean(removePath), got); rebuilt != cleanName {
			t.Errorf("RemovePrefix(%q, %q) = %q, but Join(prefix, rest) = %q, want %q", name, removePath, got, rebuilt, cleanName)
		}
	})
}

func FuzzRemovePrefixRoundTrip(f *testing.F) {
	f.Add("a", "b")
	f.Add("a/b", "c/d")
	f.Add("x", "y/z")
	f.Fuzz(func(t *testing.T, prefix, rest string) {
		if !fs.ValidPath(prefix) || !fs.ValidPath(rest) || IsCwd(prefix) || IsCwd(rest) {
			t.Skip()
		}
		got, ok := RemovePrefix(prefix+UnixSeparator+rest, prefix)
		if !ok || got != rest {
			t.Errorf("RemovePrefix(%q, %q) = %q, %t, want %q, true", prefix+UnixSeparator+rest, prefix, got, ok, rest)
		}
		if got, ok := RemovePrefix(prefix, prefix); !ok || got != CwdPath {
			t.Errorf("RemovePrefix(%q, %q) = %q, %t, want %q, true", prefix, prefix, got, ok, CwdPath)
		}
	})
}

func FuzzValidate(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		err := Validate("op", name)
		if (err == nil) != fs.ValidPath(name) {
			t.Fatalf("Validate(%q) = %v, but fs.ValidPath = %v", name, err, fs.ValidPath(name))
		}
		if err == nil {
			return
		}
		var pathErr *fs.PathError
		if !errors.As(err, &pathErr) || pathErr.Op != "op" || pathErr.Path != name {
			t.Errorf("Validate(%q) = %#v, want *fs.PathError{Op: op, Path: %q}", name, err, name)
		}
		if !errors.Is(err, fs.ErrInvalid) {
			t.Errorf("Validate(%q) = %v, want fs.ErrInvalid", name, err)
		}
	})
}

func FuzzIsCwdImpliesIsDirName(f *testing.F) {
	for _, s := range fuzzSeeds {
		f.Add(s)
	}
	f.Fuzz(func(t *testing.T, name string) {
		if IsCwd(name) && !IsDirName(name) {
			t.Errorf("IsCwd(%q) = true but IsDirName = false", name)
		}
		if IsCwd(name) && name != "" && name != CwdPath {
			t.Errorf("IsCwd(%q) = true, want only \"\" and %q", name, CwdPath)
		}
	})
}
