// Copyright 2026 Cloudfra
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

package types

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// comparableFS holds only comparable fields, so == on two file systems holding
// it compares the values.
type comparableFS struct {
	id int
}

func (comparableFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

// wrapperFS holds a func field, so == on two file systems holding it panics
// rather than reporting a result. The value is stored by value; a pointer to it
// would be comparable.
type wrapperFS struct {
	hook func()
}

func (wrapperFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

// sliceFS holds a slice field, which is not comparable either.
type sliceFS struct {
	entries []string
}

func (sliceFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

// thinFS is a wrapper that adds nothing of its own, so it is equivalent to the
// file system it holds and Equals looks through it.
type thinFS struct {
	in fs.FS
}

func (fsys thinFS) Open(name string) (fs.File, error) {
	return fsys.in.Open(name)
}

func (fsys thinFS) UnwrapFS() fs.FS {
	return fsys.in
}

// mountedFS is a wrapper that holds more than what it wraps, so it reports nil
// and keeps its own identity.
type mountedFS struct {
	in fs.FS
}

func (fsys mountedFS) Open(name string) (fs.File, error) {
	return fsys.in.Open(name)
}

func (mountedFS) UnwrapFS() fs.FS {
	return nil
}

// ifaceFS holds a file system in an interface field. reflect reports the struct
// as comparable, but == panics when the interface holds a slice, so Equals has
// to refuse the type. It does not implement [FSUnwrapper], which is how the
// comparison survives unwrapping.
type ifaceFS struct {
	in fs.FS
}

func (ifaceFS) Open(string) (fs.File, error) {
	return nil, fs.ErrNotExist
}

func TestEquals(t *testing.T) {
	t.Parallel()

	hook := func() {}

	testCases := []struct {
		name string
		a    fs.FS
		b    fs.FS
		want bool
	}{
		{
			name: "the same value",
			a:    comparableFS{id: 1},
			b:    comparableFS{id: 1},
			want: true,
		},
		{
			name: "different values of the same type",
			a:    comparableFS{id: 1},
			b:    comparableFS{id: 2},
			want: false,
		},
		{
			name: "different types",
			a:    comparableFS{id: 1},
			b:    sliceFS{},
			want: false,
		},
		{
			name: "a func field is not comparable",
			a:    wrapperFS{hook: hook},
			b:    wrapperFS{hook: hook},
			want: false,
		},
		{
			name: "a slice field is not comparable",
			a:    sliceFS{entries: []string{"a"}},
			b:    sliceFS{entries: []string{"a"}},
			want: false,
		},
		{
			name: "an interface field may hold an incomparable value",
			a:    ifaceFS{in: sliceFS{entries: []string{"a"}}},
			b:    ifaceFS{in: sliceFS{entries: []string{"a"}}},
			want: false,
		},
		{
			name: "two nil file systems",
			a:    nil,
			b:    nil,
			want: true,
		},
		{
			name: "one nil file system",
			a:    nil,
			b:    comparableFS{id: 1},
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Equals(tc.a, tc.b); got != tc.want {
				t.Errorf("Equals(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// TestEqualsPointer checks that a file system passed as a pointer keeps pointer
// identity, which is how drivers and decorators are usually passed to the ops
// helpers.
func TestEqualsPointer(t *testing.T) {
	t.Parallel()

	mem := &comparableFS{id: 1}
	if !Equals(mem, mem) {
		t.Error("Equals(same pointer) = false, want true")
	}
	if Equals(mem, &comparableFS{id: 1}) {
		t.Error("Equals(two pointers to equal values) = true, want false")
	}
}

// TestEqualsUnwraps checks that a wrapper is compared through the file system it
// holds, which is what lets an operation spanning two file systems notice that
// they are the same one.
func TestEqualsUnwraps(t *testing.T) {
	t.Parallel()

	inner := &comparableFS{id: 1}
	mounted := &mountedFS{in: inner}
	testCases := []struct {
		name string
		a    fs.FS
		b    fs.FS
		want bool
	}{
		{
			name: "a wrapper and what it wraps",
			a:    thinFS{in: inner},
			b:    inner,
			want: true,
		},
		{
			name: "two wrappers over the same file system",
			a:    thinFS{in: inner},
			b:    thinFS{in: inner},
			want: true,
		},
		{
			name: "a chain of wrappers",
			a:    thinFS{in: thinFS{in: inner}},
			b:    inner,
			want: true,
		},
		{
			name: "a wrapper that keeps its identity",
			a:    mounted,
			b:    inner,
			want: false,
		},
		{
			name: "the same wrapper twice",
			a:    mounted,
			b:    mounted,
			want: true,
		},
		{
			name: "a wrapper holding nothing",
			a:    thinFS{},
			b:    nil,
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := Equals(tc.a, tc.b); got != tc.want {
				t.Errorf("Equals(%v, %v) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

// fileSystemName reports whether name follows the convention this repo uses for
// file systems: an identifier ending in FS or fs, or the local variable name
// fsys. The io/fs qualifier is excluded because it names the package rather than
// a file system.
func fileSystemName(name string) bool {
	if name == "fs" {
		return false
	}
	return name == "fsys" || strings.HasSuffix(name, "FS") || strings.HasSuffix(name, "fs")
}

// operandText returns the dotted source text of a comparison operand when it
// names a value outright — an identifier, a field or a package-qualified name —
// rather than being a call, an index or a literal.
func operandText(expr ast.Expr) (string, bool) {
	for {
		paren, ok := expr.(*ast.ParenExpr)
		if !ok {
			break
		}
		expr = paren.X
	}
	switch v := expr.(type) {
	case *ast.Ident:
		return v.Name, true
	case *ast.SelectorExpr:
		base, ok := operandText(v.X)
		if !ok {
			return "", false
		}
		return base + "." + v.Sel.Name, true
	default:
		return "", false
	}
}

// lastName returns the final component of a dotted name, which is the part that
// says whether the value is a file system.
func lastName(name string) string {
	if idx := strings.LastIndex(name, "."); idx >= 0 {
		return name[idx+1:]
	}
	return name
}

// manualEquals reports the file-system comparisons in an already parsed file.
func manualEquals(fset *token.FileSet, file *ast.File) []string {
	var found []string
	ast.Inspect(file, func(n ast.Node) bool {
		cmp, ok := n.(*ast.BinaryExpr)
		if !ok {
			return true
		}
		if cmp.Op != token.EQL && cmp.Op != token.NEQ {
			return true
		}
		a, okA := operandText(cmp.X)
		b, okB := operandText(cmp.Y)
		if !okA || !okB {
			return true
		}
		// Comparing a file system against nil only asks whether one is set,
		// which is safe and common.
		if a == "nil" || b == "nil" {
			return true
		}
		if !fileSystemName(lastName(a)) && !fileSystemName(lastName(b)) {
			return true
		}
		pos := fset.Position(cmp.Pos())
		found = append(found, fmt.Sprintf("%s:%d: %s %s %s", pos.Filename, pos.Line, a, cmp.Op, b))
		return true
	})
	return found
}

// moduleRoot returns the directory holding go.mod, found by walking up from the
// test's working directory.
func moduleRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() = %v, want nil", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("no go.mod found at or above %q", dir)
		}
		dir = parent
	}
}

// TestNoManualEqualsOnFileSystems walks every Go file in the module and fails if
// one compares two file systems with == or !=. Such a comparison panics when the
// dynamic type of a file system is not comparable, so [Equals] has to be used
// instead. The check is syntactic: it looks for an operand named after the file
// system convention above, so a file system held under another name is not
// found.
func TestNoManualEqualsOnFileSystems(t *testing.T) {
	t.Parallel()
	root := moduleRoot(t)

	found := 0
	var hits []string
	err := filepath.WalkDir(root, func(name string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == "third_party" || (name != root && strings.HasPrefix(entry.Name(), ".")) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(name, ".go") {
			return nil
		}
		found++
		fset := token.NewFileSet()
		file, err := parser.ParseFile(fset, name, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("ParseFile(%q) = %v, want nil", name, err)
			return nil
		}
		hits = append(hits, manualEquals(fset, file)...)
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) = %v, want nil", root, err)
	}
	// A walk that found almost nothing means the module root is wrong, which
	// would otherwise pass the test silently.
	if found < 100 {
		t.Fatalf("checked %d Go files under %q, want at least 100", found, root)
	}
	for _, hit := range hits {
		t.Errorf("%s compares file systems directly: use types.Equals instead of ==", hit)
	}
}
