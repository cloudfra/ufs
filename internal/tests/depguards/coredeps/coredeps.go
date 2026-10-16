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

// Package main is a consumer of the core github.com/cloudfra/ufs package
// only. It exists so the module's dependency graph reflects what users of
// the core file system APIs import: if forbidden packages such as
// protocol buffers appear in that graph, dependency guard tests fail.
package main

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/cloudfra/ufs"
)

func main() {
	ctx := context.Background()
	if err := run(ctx); err != nil {
		slog.ErrorContext(ctx, "failed to use file system", "error", err)
	}
}

func run(ctx context.Context) error {
	fsys, err := ufs.New(ctx, "memory://")
	if err != nil {
		return fmt.Errorf("cannot create memory file system, %w", err)
	}
	if err := fsys.Close(); err != nil {
		return fmt.Errorf("cannot close file system, %s", err)
	}
	return nil
}
