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

//go:build windows

package host

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/cloudfra/ufs/internal/osutil"
)

// hostOpEnv makes the test binary run one file operation, named by its first
// argument, on the paths that follow and exit instead of running the tests.
// See hostOp.
const hostOpEnv = "UFS_HOST_TEST_OP"

func TestMain(m *testing.M) {
	if os.Getenv(hostOpEnv) != "" {
		if err := runHostOp(os.Args[1], os.Args[2:]); err != nil {
			fmt.Fprint(os.Stderr, err)
			os.Exit(1)
		}
		os.Exit(0)
	}
	os.Exit(m.Run())
}

func runHostOp(op string, args []string) error {
	switch {
	case op == "rename" && len(args) == 2:
		return osutil.Rename(args[0], args[1])
	case op == "remove" && len(args) == 1:
		return osutil.Remove(args[0])
	}
	return fmt.Errorf("unknown host op %q %q", op, args)
}

// hostOp runs a file operation (see runHostOp) on host paths in a separate
// process. ProjFS sends no notifications for I/O by the provider's own
// process, so a delete or rename the test makes itself would never reach the
// mount's PRE_DELETE / PRE_RENAME handling. (Linux does it in-process, see
// hostop_other_test.go: a fork there briefly holds every open file of the
// parent, which makes concurrent FUSE unmounts fail with EBUSY.)
func hostOp(ctx context.Context, op string, args ...string) error {
	cmd := exec.CommandContext(ctx, os.Args[0], append([]string{op}, args...)...) //nolint:gosec // re-runs this test binary
	cmd.Env = append(os.Environ(), hostOpEnv+"=1")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %s: %w: %s", op, strings.Join(args, " "), err, stderr.String())
	}
	return nil
}

// hostRename renames oldPath to newPath from a separate process; see hostOp.
func hostRename(ctx context.Context, oldPath, newPath string) error {
	return hostOp(ctx, "rename", oldPath, newPath)
}

// hostRemove removes name from a separate process; see hostOp.
func hostRemove(ctx context.Context, name string) error {
	return hostOp(ctx, "remove", name)
}
