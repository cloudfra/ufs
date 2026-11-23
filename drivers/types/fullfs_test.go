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
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// The compiler is asked here whether fullFS answers to the file-system
// interfaces of the standard library, so that the claim is checked rather than
// merely documented. Go's fs.GlobFS and fs.SubFS are left out on purpose:
// globutil.GlobFS gives Glob to any fs.ReadDirFS, and fs.Sub gives Sub to any
// fs.FS, so a full file system gets both without asking for them.
var (
	_ fs.FS         = fullFS(nil)
	_ fs.ReadDirFS  = fullFS(nil)
	_ fs.ReadFileFS = fullFS(nil)
	_ fs.ReadLinkFS = fullFS(nil)
	_ fs.StatFS     = fullFS(nil)
)

// notFullFS records the interfaces that fullFS leaves out, and why. Every
// interface this package declares has to be reachable from fullFS or be listed
// here, so a new capability interface cannot join the package without being
// placed in one list or the other.
var notFullFS = map[string]string{
	"File":     "a file handle rather than a file system",
	"FileInfo": "a description of one node rather than a file system",
	"ReadFile": "a file handle rather than a file system",

	"ListFilenames":       "an ops hint: ops.ListFiles walks with ReadDir without it",
	"ForEachFileInfoIter": "an ops hint: ops.ForEachFileInfo walks with ReadDir without it",
	"ForEachFilenameIter": "an ops hint: ops.ForEachFilename walks with ReadDir without it",
}

// TestFullFSContainsAllInterfaces checks that fullFS is the whole capability
// surface it claims to be. It reads the interfaces declared in this package and
// the interfaces each of them embeds, then requires every exported one to be
// reachable from fullFS. Anything deliberately left out has to be written down
// in [notFullFS], which is where the decision that a capability does not belong
// in a full file system gets reviewed.
func TestFullFSContainsAllInterfaces(t *testing.T) {
	embedded := embeddedInterfaces(t, ".")
	if _, ok := embedded["fullFS"]; !ok {
		t.Fatal("fullFS is not declared in this package")
	}

	reached := map[string]bool{}
	for queue := []string{"fullFS"}; len(queue) > 0; {
		name := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		for _, next := range embedded[name] {
			if !reached[next] {
				reached[next] = true
				queue = append(queue, next)
			}
		}
	}

	declared := make([]string, 0, len(embedded))
	for name := range embedded {
		declared = append(declared, name)
	}
	sort.Strings(declared)

	for _, name := range declared {
		if !ast.IsExported(name) {
			continue
		}
		if reason, ok := notFullFS[name]; ok {
			if reached[name] {
				t.Errorf("fullFS includes %s, recorded in notFullFS as %q", name, reason)
			}
			continue
		}
		if !reached[name] {
			t.Errorf("fullFS does not include %s: embed it in fullFS or record why it is left out", name)
		}
	}

	leftOut := make([]string, 0, len(notFullFS))
	for name := range notFullFS {
		leftOut = append(leftOut, name)
	}
	sort.Strings(leftOut)
	for _, name := range leftOut {
		if _, ok := embedded[name]; !ok {
			t.Errorf("notFullFS records %s, which this package no longer declares", name)
		}
	}
}

// embeddedInterfaces maps every interface declared in the Go files of dir to
// the interfaces it embeds, as written in the source: a name of this package on
// its own, and another package's name qualified by the import it came through,
// such as "fs.FS". Test files are skipped, since what they declare is not part
// of the package.
func embeddedInterfaces(t *testing.T, dir string) map[string][]string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("cannot list %s: %v", dir, err)
	}

	embedded := map[string][]string{}
	fset := token.NewFileSet()
	for _, entry := range entries {
		base := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(base, ".go") || strings.HasSuffix(base, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, filepath.Join(dir, base), nil, parser.SkipObjectResolution)
		if err != nil {
			t.Fatalf("cannot parse %s: %v", base, err)
		}
		for _, decl := range file.Decls {
			typeDecl, ok := decl.(*ast.GenDecl)
			if !ok || typeDecl.Tok != token.TYPE {
				continue
			}
			for _, spec := range typeDecl.Specs {
				typeSpec, ok := spec.(*ast.TypeSpec)
				if !ok {
					continue
				}
				iface, ok := typeSpec.Type.(*ast.InterfaceType)
				if !ok {
					continue
				}
				name := typeSpec.Name.Name
				if _, seen := embedded[name]; !seen {
					embedded[name] = nil
				}
				for _, field := range iface.Methods.List {
					// Only an embedded interface is unnamed. A method has
					// a name, and adds to the method set without pulling
					// another interface in.
					if len(field.Names) > 0 {
						continue
					}
					embedded[name] = append(embedded[name], types.ExprString(field.Type))
				}
			}
		}
	}
	return embedded
}
