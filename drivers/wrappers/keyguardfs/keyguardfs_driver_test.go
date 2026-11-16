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

package keyguardfs_test

import (
	"testing"

	"github.com/cloudfra/ufs/drivers/core"
	ufsdriversTesting "github.com/cloudfra/ufs/drivers/testing"
	"github.com/cloudfra/ufs/drivers/types"
	"github.com/cloudfra/ufs/drivers/wrappers/keyguardfs"
)

// seedTree holds key material at three depths and inside a directory the
// default policy names, next to the files it leaves alone. It keeps every
// protected name out of the root directory, where the wrapperFS suite needs a
// guard and the file system it guards to list the same entries. A name policy
// never reads a body, so the bodies say nothing about keys and hold none.
var seedTree = map[string]string{
	"keys/server.key":      "the body of a protected key file\n",
	"deploy/prod/api.key":  "the body of a protected file two levels down\n",
	"deploy/prod/.env":     "APP_MODE=production\n",
	"certs/ca.pem":         "the body of a protected certificate\n",
	"notes.txt":            "a note about the deploy\n",
	"dir/sub/plain.txt":    "nothing protected\n",
	"blob.bin":             "\x00\x01\x02binary\n",
	"checkout/deploy/pass": "a name no policy covers\n",
}

// seedInner returns a seeded memFS for a guard to protect.
func seedInner(t *testing.T) types.WriteFS {
	t.Helper()

	inner := core.MakeMemFS("memory://test")
	for _, dir := range []string{"keys", "deploy", "deploy/prod", "certs", "dir", "dir/sub", "checkout", "checkout/deploy"} {
		if err := inner.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("MkdirAll(%q) = %v", dir, err)
		}
	}
	for name, content := range seedTree {
		ufsdriversTesting.WriteFile(t, inner, name, content)
	}

	return inner
}

// makeGuard returns the constructor the shared suites call, for the policy in
// opts. Each call builds a fresh guarded file system, which the suites close.
func makeGuard(opts keyguardfs.Options) func(*testing.T) types.SimpleFS {
	return func(t *testing.T) types.SimpleFS {
		fsys, err := keyguardfs.New(seedInner(t), opts)
		if err != nil {
			t.Fatalf("New() = %v, want nil", err)
		}

		return fsys
	}
}

func TestKeyGuardFSRefuseDriver(t *testing.T) {
	ufsdriversTesting.FS(t, ufsdriversTesting.Args{ReadOnly: true}, makeGuard(keyguardfs.Options{}))
}

func TestKeyGuardFSHideDriver(t *testing.T) {
	ufsdriversTesting.FS(t, ufsdriversTesting.Args{ReadOnly: true},
		makeGuard(keyguardfs.Options{Mode: keyguardfs.ModeHide}))
}
