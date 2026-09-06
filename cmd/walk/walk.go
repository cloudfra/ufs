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

// Package main is the walk application for traversing through a virtual file system.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/cloudfra/ufs"
)

var pathFlag = flag.String("path", ".", "Path to walk the directory tree to report file names.")

func main() {
	flag.Parse()
	if err := run(*pathFlag); err != nil {
		slog.Error("walk failed", "error", err)
		os.Exit(1)
	}
}

func run(dir string) error {
	ctx := context.Background()
	fsys, err := ufs.New(ctx, dir)
	if err != nil {
		return err
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Warn("cannot close mounted file system", "fs", fsys, "error", err)
		}
	}()
	return ufs.ForEachFilename(fsys, ".", func(name string) error {
		absName, err := ufs.AbsPath(fsys, name)
		if err == nil {
			fmt.Println(absName)
		} else {
			fmt.Printf("ufs://%s\n", name)
		}
		return nil
	})
}
