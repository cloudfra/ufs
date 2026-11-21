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
//
// Neither the name of the copyright holder nor the names of its contributors
// may be used to endorse or promote products derived from this software
// without specific prior written permission.

package keyguardfs

import (
	"errors"
	"io/fs"
	"path"
	"slices"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// testNames are the files the fixture seeds: key material at two depths and
// inside a directory the default policy names, next to the files no policy
// covers. It keeps the protected names out of the root directory, where a guard
// has to list what it guards.
var testNames = []string{
	"keys/server.key",
	"deploy/prod/api.key",
	"deploy/prod/.env",
	".ssh/id_ed25519",
	"secrets/api-token",
	"checkout/deploy/secrets/token",
	"notes.txt",
	"dir/sub/plain.txt",
	"blob.bin",
	"checkout/deploy/pass",
	"checkout/deploy/sub/pass",
}

// testBody is the body written at a name, so that every seeded file holds
// something its own. A name policy never reads a body, so the bodies say nothing
// about keys and hold none.
func testBody(name string) string {
	return "the body of " + name + "\n"
}

// testDirs are the directories of [testNames], which memFS needs before it can
// create a file below them.
var testDirs = []string{
	"keys", "deploy", "deploy/prod", ".ssh", "dir", "dir/sub",
	"secrets", "checkout", "checkout/deploy", "checkout/deploy/secrets",
	"checkout/deploy/sub",
}

// testPatterns is the policy the refusals are checked against, spelled out
// rather than left to [DefaultPatterns] so that each case says what it tests.
var testPatterns = []string{"*.key", ".env", ".ssh", "secrets"}

// testProtectedNames are the names of [testNames] that [testPatterns] covers.
var testProtectedNames = []string{
	"keys/server.key",
	"deploy/prod/api.key",
	"deploy/prod/.env",
	".ssh/id_ed25519",
	"secrets/api-token",
}

// testPlainNames are the names of [testNames] that [testPatterns] leaves alone.
var testPlainNames = []string{
	"notes.txt",
	"dir/sub/plain.txt",
	"blob.bin",
	"checkout/deploy/pass",
}

// testReadOps are the read operations a guard enforces, with the operation name
// each reports in the error it returns.
var testReadOps = []struct {
	name string
	op   string
	call func(fsys types.ReadFS, name string) error
}{
	{
		name: "Open",
		op:   "open",
		call: func(fsys types.ReadFS, name string) error { _, err := fsys.Open(name); return err },
	},
	{
		name: "ReadFile",
		op:   "readfile",
		call: func(fsys types.ReadFS, name string) error { _, err := fsys.ReadFile(name); return err },
	},
	{
		name: "Stat",
		op:   "stat",
		call: func(fsys types.ReadFS, name string) error { _, err := fsys.Stat(name); return err },
	},
	{
		name: "Lstat",
		op:   "lstat",
		call: func(fsys types.ReadFS, name string) error { _, err := fsys.Lstat(name); return err },
	},
	{
		name: "ReadLink",
		op:   "readlink",
		call: func(fsys types.ReadFS, name string) error { _, err := fsys.ReadLink(name); return err },
	},
}

// invalidNames are the path spellings every file system refuses as invalid, the
// same list drivers/testing drives through its read suite. A policy matching
// everything must report them as invalid rather than as protected.
var invalidNames = []string{
	"/absolute/path",
	"../relative/path",
	"invalid/../path",
	"a/b/../c",
	"",
	"/",
	"a//b",
	"a/",
	"a/b/",
	"a/./b",
	"./a",
}

// makeGuard returns a guard over a seeded memFS, closing the guard and the file
// system it guards.
func makeGuard(tb testing.TB, opts Options) types.ReadFS {
	tb.Helper()

	inner := makeFixture(tb)

	fsys, err := New(inner, opts)
	if err != nil {
		tb.Fatalf("New() = %v, want nil", err)
	}
	tb.Cleanup(ufsTesting.ValidateClose(tb, fsys))

	return fsys
}

// makeFixture returns a memFS seeded with [testNames], which needs the
// directories of [testDirs] before it can hold a file below them.
func makeFixture(tb testing.TB) types.WriteFS {
	tb.Helper()

	inner := core.MakeMemFS("memory://test")
	tb.Cleanup(ufsTesting.ValidateClose(tb, inner))
	for _, dir := range testDirs {
		if err := inner.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("MkdirAll(%q) = %v", dir, err)
		}
	}
	for _, name := range testNames {
		ufsdriversTesting.WriteFile(tb, inner, name, testBody(name))
	}

	return inner
}

// assertDenied checks that err is the refusal of name by op, and that it stands
// for want.
func assertDenied(t *testing.T, op, name string, want error, err error) {
	t.Helper()

	var pathErr *fs.PathError
	if !errors.As(err, &pathErr) {
		t.Errorf("%s(%q) = %v, want an error wrapping %v", op, name, err, want)
		return
	}
	if pathErr.Op != op {
		t.Errorf("%s(%q) reported op %q, want %q", op, name, pathErr.Op, op)
	}
	if pathErr.Path != name {
		t.Errorf("%s(%q) reported path %q, want %q", op, name, pathErr.Path, name)
	}
	if !errors.Is(err, want) {
		t.Errorf("%s(%q) = %v, want an error wrapping %v", op, name, err, want)
	}
}

func TestRefuseModeRefusesProtectedNames(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: testPatterns})

	for _, name := range testProtectedNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, test := range testReadOps {
				err := test.call(fsys, name)
				assertDenied(t, test.op, name, ErrProtected, err)
				if !errors.Is(err, fs.ErrPermission) {
					t.Errorf("%s(%q) = %v, want an error wrapping %v", test.op, name, err, fs.ErrPermission)
				}
				if errors.Is(err, fs.ErrNotExist) {
					t.Errorf("%s(%q) = %v, want a refusal that does not report a missing file", test.op, name, err)
				}
			}
		})
	}
}

func TestHideModeReportsProtectedNamesMissing(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: testPatterns, Mode: ModeHide})

	for _, name := range testProtectedNames {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			for _, test := range testReadOps {
				err := test.call(fsys, name)
				assertDenied(t, test.op, name, fs.ErrNotExist, err)
				if errors.Is(err, ErrProtected) {
					t.Errorf("%s(%q) = %v, want a missing file that does not name the policy", test.op, name, err)
				}
				if errors.Is(err, fs.ErrPermission) {
					t.Errorf("%s(%q) = %v, want a missing file that is not a permission failure", test.op, name, err)
				}
			}
		})
	}
}

func TestRefuseModeLeavesProtectedNamesVisible(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: testPatterns})

	entries, err := fsys.ReadDir("keys")
	if err != nil {
		t.Fatalf("ReadDir(keys) = %v, want the protected file listed", err)
	}
	if got := ufsTesting.DirEntryListToNames(entries); !slices.Equal(got, []string{"server.key"}) {
		t.Errorf("ReadDir(keys) = %q, want %q", got, []string{"server.key"})
	}

	info, err := fsys.Stat("keys")
	if err != nil {
		t.Fatalf("Stat(keys) = %v, want nil", err)
	}
	if !info.IsDir() {
		t.Errorf("Stat(keys).IsDir() = false, want true")
	}
}

func TestHideModeRemovesProtectedNamesFromListings(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: testPatterns, Mode: ModeHide})

	for _, dir := range []string{"keys", "deploy/prod"} {
		entries, err := fsys.ReadDir(dir)
		if err != nil {
			t.Fatalf("ReadDir(%q) = %v, want an empty listing", dir, err)
		}
		if got := ufsTesting.DirEntryListToNames(entries); len(got) != 0 {
			t.Errorf("ReadDir(%q) = %q, want no entries", dir, got)
		}
	}

	// A protected directory is itself missing, so its listing is refused rather
	// than emptied.
	if _, err := fsys.ReadDir(".ssh"); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("ReadDir(.ssh) = %v, want an error wrapping %v", err, fs.ErrNotExist)
	}

	// The directory holding a protected file stays where it was.
	entries, err := fsys.ReadDir(".")
	if err != nil {
		t.Fatalf("ReadDir(.) = %v, want nil", err)
	}
	if got := ufsTesting.DirEntryListToNames(entries); !slices.Contains(got, "keys") {
		t.Errorf("ReadDir(.) = %q, want it to still hold %q", got, "keys")
	}
}

func TestPolicyNamesCoverEveryDepthBelowThem(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: []string{"secrets"}})

	for _, name := range []string{"secrets/api-token", "checkout/deploy/secrets/api-token"} {
		if _, err := fsys.ReadFile(name); !errors.Is(err, ErrProtected) {
			t.Errorf("ReadFile(%q) = %v, want an error wrapping %v", name, err, ErrProtected)
		}
	}

	if _, err := fsys.ReadFile("checkout/deploy/pass"); err != nil {
		t.Errorf("ReadFile(checkout/deploy/pass) = %v, want nil for a name the pattern does not reach", err)
	}
}

func TestUnprotectedNamesReadThrough(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: testPatterns})

	for _, name := range testPlainNames {
		got, err := fsys.ReadFile(name)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v, want nil", name, err)
			continue
		}
		if string(got) != testBody(name) {
			t.Errorf("ReadFile(%q) = %q, want %q", name, got, testBody(name))
		}
	}
}

func TestPolicyProtectingNothingLeavesEveryNameReadable(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: []string{"no-such-name"}})

	for _, name := range append(slices.Clone(testProtectedNames), testPlainNames...) {
		got, err := fsys.ReadFile(name)
		if err != nil {
			t.Errorf("ReadFile(%q) = %v, want nil", name, err)
			continue
		}
		if string(got) != testBody(name) {
			t.Errorf("ReadFile(%q) = %q, want %q", name, got, testBody(name))
		}
	}
}

func TestDefaultPolicyProtectsItsCatalog(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{})

	for _, name := range testProtectedNames {
		if _, err := fsys.ReadFile(name); !errors.Is(err, ErrProtected) {
			t.Errorf("ReadFile(%q) = %v under the default policy, want an error wrapping %v", name, err, ErrProtected)
		}
	}

	for _, name := range []string{"notes.txt", "checkout/deploy/pass", "blob.bin"} {
		if _, err := fsys.ReadFile(name); err != nil {
			t.Errorf("ReadFile(%q) = %v under the default policy, want nil", name, err)
		}
	}
}

func TestCatchAllPolicyStillRefusesInvalidPathsAsInvalid(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: []string{"*"}})

	for _, test := range testReadOps {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			for _, name := range invalidNames {
				ufsTesting.AssertInvalidPathError(t, name, test.call(fsys, name), test.op)
			}
		})
	}
}

func TestNewRejectsAnUnknownMode(t *testing.T) {
	t.Parallel()

	inner := core.MakeMemFS("memory://reject")
	t.Cleanup(ufsTesting.ValidateClose(t, inner))

	_, err := New(inner, Options{Mode: "refuses"})
	if err == nil {
		t.Fatalf("New with mode %q = nil, want an error", "refuses")
	}
	if !strings.Contains(err.Error(), ModeRefuse) || !strings.Contains(err.Error(), ModeHide) {
		t.Errorf("New with mode %q = %q, want it to name the modes it accepts", "refuses", err)
	}
}

func TestNewRejectsAMalformedPattern(t *testing.T) {
	t.Parallel()

	inner := core.MakeMemFS("memory://reject")
	t.Cleanup(ufsTesting.ValidateClose(t, inner))

	_, err := New(inner, Options{Patterns: []string{"*.key", "["}})
	if err == nil {
		t.Fatalf(`New with the pattern "[" = nil, want an error`)
	}
	if !errors.Is(err, path.ErrBadPattern) {
		t.Errorf(`New with the pattern "[" = %v, want an error wrapping %v`, err, path.ErrBadPattern)
	}
}

func TestStringNamesTheModeAndThePolicySize(t *testing.T) {
	t.Parallel()

	fsys := makeGuard(t, Options{Patterns: testPatterns})
	want := `{"keyGuardFS":{"inner":{"memFS":"memory://test"},"mode":"refuse","patterns":"4"}}`
	if got := fsys.String(); got != want {
		t.Errorf("String() = %s, want %s", got, want)
	}

	hidden := makeGuard(t, Options{Patterns: testPatterns, Mode: ModeHide})
	want = `{"keyGuardFS":{"inner":{"memFS":"memory://test"},"mode":"hide","patterns":"4"}}`
	if got := hidden.String(); got != want {
		t.Errorf("String() of a hiding guard = %s, want %s", got, want)
	}
}

func TestURIMarksTheGuardReadOnly(t *testing.T) {
	t.Parallel()
	fsys := makeGuard(t, Options{Patterns: testPatterns})

	u, err := fsys.URI()
	if err != nil {
		t.Fatalf("URI() = %v, want nil", err)
	}
	if got := u.String(); got != "memory://test?ro=true" {
		t.Errorf("URI() = %q, want %q", got, "memory://test?ro=true")
	}

	// A caller editing the URL it was given cannot change what the guard reports
	// next time.
	u.RawQuery = "ro=false"
	again, err := fsys.URI()
	if err != nil {
		t.Fatalf("URI() = %v, want nil", err)
	}
	if got := again.String(); got != "memory://test?ro=true" {
		t.Errorf("URI() after editing the first = %q, want %q", got, "memory://test?ro=true")
	}
}

func TestInnerFSAndIdentity(t *testing.T) {
	t.Parallel()

	inner := core.MakeMemFS("memory://test")
	t.Cleanup(ufsTesting.ValidateClose(t, inner))
	fsys, err := New(inner, Options{Patterns: testPatterns})
	if err != nil {
		t.Fatalf("New() = %v, want nil", err)
	}
	t.Cleanup(ufsTesting.ValidateClose(t, fsys))

	wrapper, ok := fsys.(types.WrapperFS)
	if !ok {
		t.Fatalf("%T does not implement types.WrapperFS", fsys)
	}
	if wrapper.InnerFS() != fs.FS(inner) {
		t.Errorf("InnerFS() = %T, want the guarded %T", wrapper.InnerFS(), inner)
	}
	if !types.SameFS(fsys, inner) {
		t.Errorf("types.SameFS(guard, inner) = false, want true, because a guard stores none of the data it presents")
	}
	if !types.SameFS(inner, fsys) {
		t.Errorf("types.SameFS(inner, guard) = false, want true, because a guard stores none of the data it presents")
	}
}

func TestErrProtectedStandsForAPermissionFailure(t *testing.T) {
	t.Parallel()

	if !errors.Is(ErrProtected, fs.ErrPermission) {
		t.Errorf("errors.Is(ErrProtected, fs.ErrPermission) = false, want true")
	}
	if errors.Is(ErrProtected, fs.ErrNotExist) {
		t.Errorf("errors.Is(ErrProtected, fs.ErrNotExist) = true, want false")
	}
	if ErrProtected.Error() != "protected by the key material policy" {
		t.Errorf("ErrProtected.Error() = %q, want %q", ErrProtected.Error(), "protected by the key material policy")
	}
}
