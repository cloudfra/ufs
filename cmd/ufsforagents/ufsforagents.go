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

// Package main is the ufsforagents application for providing a callable tool for ufs mounts for AI agents.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"

	"github.com/cloudfra/ufs"
	"github.com/cloudfra/ufs/agenttools/posixcli"
	_ "github.com/cloudfra/ufs/drivers/all"
)

var (
	uriFlag = flag.String("uri", "", "URI of the virtual file system to mount (e.g. memory://, gs://bucket).")
	cmdFlag = flag.String("cmd", "", "Command to run against the ufs mount. (e.g. ls)")
)

func main() {
	flag.Parse()
	if err := run(*uriFlag, *cmdFlag); err != nil {
		slog.Error("walk failed", "error", err)
		os.Exit(1)
	}
}

func run(uri string, cmd string) error {
	ctx := context.Background()
	fsys, err := ufs.New(ctx, uri)
	if err != nil {
		return err
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Warn("cannot close mounted file system", "fs", fsys, "error", err)
		}
	}()
	output, err := posixcli.Run(ctx, fsys, cmd)
	if err != nil {
		return err
	}

	fmt.Print(output)
	return nil
}
