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

package all

import (
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/cloudfra/ufs"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// The tests below only use the exported ufs API: nothing but the blank imports
// in all.go can make the drivers and decorators they ask for available.

// memorySpec returns a YAML mount spec for a memory: file system wrapped by
// the decorators in options, a YAML flow sequence.
func memorySpec(options string) string {
	return "- source: \"memory:\"\n  mountPoint: /\n  options: " + options + "\n"
}

func TestBoltDriverRegistered(t *testing.T) {
	if runtime.GOARCH == "wasm" {
		t.Skip("boltfs is a stub on wasm")
	}
	uri := "bolt:" + filepath.Join(t.TempDir(), "test.db")
	fsys, err := ufs.New(t.Context(), uri)
	if err != nil {
		t.Fatalf("ufs.New(%q) = %v, want nil", uri, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	const name, want = "hello.txt", "hello bolt"
	f, err := fsys.Create(name)
	if err != nil {
		t.Fatalf("Create(%q) = %v, want nil", name, err)
	}
	if _, err := f.WriteString(want); err != nil {
		t.Errorf("WriteString(%q) = %v, want nil", want, err)
	}
	if err := f.Close(); err != nil {
		t.Fatalf("Close() = %v, want nil", err)
	}
	got, err := fs.ReadFile(fsys, name)
	if err != nil {
		t.Fatalf("ReadFile(%q) = %v, want nil", name, err)
	}
	if string(got) != want {
		t.Errorf("ReadFile(%q) = %q, want %q", name, got, want)
	}
}

func TestGCSDriverRegistered(t *testing.T) {
	// With an emulator host the storage client needs neither credentials nor
	// the network to be created; nothing is listening on the address.
	t.Setenv("STORAGE_EMULATOR_HOST", "127.0.0.1:1")
	const uri = "gs://bucket/dir"
	fsys, err := ufs.New(t.Context(), uri)
	if err != nil {
		t.Fatalf("ufs.New(%q) = %v, want nil", uri, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()
}

func TestGitDriverRegistered(t *testing.T) {
	if runtime.GOOS == "aix" || runtime.GOOS == "wasip1" {
		t.Skip("gitfs matches no URI on " + runtime.GOOS)
	}
	// There is no repository to clone, so opening fails either way. The git
	// driver is registered if it is the one that reports the failure.
	uri := filepath.Join(t.TempDir(), "missing.git")
	fsys, err := ufs.New(t.Context(), uri)
	if err == nil {
		defer ufsTesting.ValidateClose(t, fsys)()
		t.Fatalf("ufs.New(%q) = nil, want an error", uri)
	}
	if want := `with driver "git"`; !strings.Contains(err.Error(), want) {
		t.Errorf("ufs.New(%q) = %v, want an error containing %q", uri, err, want)
	}
}

func TestReadOnlyDecoratorRegistered(t *testing.T) {
	testCases := []struct {
		name string
		spec string
	}{
		{name: "options", spec: memorySpec("[{readOnly: true}]")},
		{name: "fstab ro", spec: "memory: / memory ro 0 0"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fsys, err := ufs.New(t.Context(), tc.spec)
			if err != nil {
				t.Fatalf("ufs.New(%q) = %v, want nil", tc.spec, err)
			}
			defer ufsTesting.ValidateClose(t, fsys)()

			f, err := fsys.Create("hello.txt")
			if err == nil {
				defer ufsTesting.ValidateClose(t, f)()
			}
			if !errors.Is(err, fs.ErrPermission) {
				t.Errorf("Create(%q) = %v, want %v", "hello.txt", err, fs.ErrPermission)
			}
		})
	}
}

func TestFaultDecoratorRegistered(t *testing.T) {
	spec := memorySpec("[{fault: {errorRate: 1}}]")
	fsys, err := ufs.New(t.Context(), spec)
	if err != nil {
		t.Fatalf("ufs.New(%q) = %v, want nil", spec, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	// An error rate of 1 fails every operation, even one memory: allows.
	f, err := fsys.Create("hello.txt")
	if err == nil {
		defer ufsTesting.ValidateClose(t, f)()
		t.Errorf("Create(%q) = nil, want an injected error", "hello.txt")
	}
}

// TestDecoratorsCompose checks that both decorators are usable on one mount,
// as they are when a binary imports this package.
func TestDecoratorsCompose(t *testing.T) {
	spec := memorySpec("[{fault: {latency: 1ns}}, {readOnly: true}]")
	fsys, err := ufs.New(t.Context(), spec)
	if err != nil {
		t.Fatalf("ufs.New(%q) = %v, want nil", spec, err)
	}
	defer ufsTesting.ValidateClose(t, fsys)()

	if _, err := fsys.Create("hello.txt"); !errors.Is(err, fs.ErrPermission) {
		t.Errorf("Create(%q) = %v, want %v", "hello.txt", err, fs.ErrPermission)
	}
}

const (
	ufsImportPath     = "github.com/cloudfra/ufs"
	driversImportPath = ufsImportPath + "/drivers"
	// coreImportPath is imported by all.go although it registers nothing
	// yet: the built-in file systems are moving into it from the base
	// package. Remove the exception below once the first of them has moved.
	coreImportPath = driversImportPath + "/core"
)

// importPath returns the unquoted import path of imp.
func importPath(t *testing.T, imp *ast.ImportSpec) string {
	t.Helper()
	p, err := strconv.Unquote(imp.Path.Value)
	if err != nil {
		t.Fatalf("Unquote(%s) = %v, want nil", imp.Path.Value, err)
	}
	return p
}

// registers reports whether the Go source file at filename calls ufs.Register
// or ufs.RegisterDecorator.
func registers(t *testing.T, filename string) bool {
	t.Helper()
	f, err := parser.ParseFile(token.NewFileSet(), filename, nil, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("ParseFile(%q) = %v, want nil", filename, err)
	}
	ufsName := ""
	for _, imp := range f.Imports {
		if importPath(t, imp) != ufsImportPath {
			continue
		}
		ufsName = "ufs"
		if imp.Name != nil {
			ufsName = imp.Name.Name
		}
	}
	if ufsName == "" {
		return false
	}
	found := false
	ast.Inspect(f, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return !found
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return !found
		}
		if pkg, ok := sel.X.(*ast.Ident); ok && pkg.Name == ufsName &&
			(sel.Sel.Name == "Register" || sel.Sel.Name == "RegisterDecorator") {
			found = true
		}
		return !found
	})
	return found
}

// TestAllRegisteringPackagesImported reads the source of every package under
// drivers/ and checks that all.go blank-imports exactly the ones that register
// a driver or a decorator. It reads source rather than the registry so that a
// package whose registration is behind a build constraint is found on every
// platform.
func TestAllRegisteringPackagesImported(t *testing.T) {
	var want []string
	err := filepath.WalkDir("..", func(name string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || !strings.HasSuffix(name, ".go") || strings.HasSuffix(name, "_test.go") {
			return nil
		}
		if registers(t, name) {
			rel := filepath.ToSlash(filepath.Dir(name))
			want = append(want, path.Join(driversImportPath, strings.TrimPrefix(rel, "..")))
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir(%q) = %v, want nil", "..", err)
	}
	want = append(want, coreImportPath)
	slices.Sort(want)
	want = slices.Compact(want)
	if len(want) == 0 {
		t.Fatal("found no package under drivers/ that registers a driver or decorator")
	}

	f, err := parser.ParseFile(token.NewFileSet(), "all.go", nil, parser.ImportsOnly)
	if err != nil {
		t.Fatalf("ParseFile(%q) = %v, want nil", "all.go", err)
	}
	got := make([]string, 0, len(f.Imports))
	for _, imp := range f.Imports {
		got = append(got, importPath(t, imp))
	}

	for _, p := range want {
		if !slices.Contains(got, p) {
			t.Errorf("all.go does not import %q, which registers a driver or decorator", p)
		}
	}
	for _, p := range got {
		if !slices.Contains(want, p) {
			t.Errorf("all.go imports %q, which registers no driver or decorator", p)
		}
	}
}
