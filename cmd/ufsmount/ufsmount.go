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

// Package main is the ufsmount application for mounting a virtual file system
// on the host operating system.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudfra/ufs"
)

var (
	uriFlag     = flag.String("uri", "", "URI of the virtual file system to mount (e.g. memory://, gs://bucket).")
	mountFlag   = flag.String("mount", "", "Path on the host to mount the file system at.")
	verboseFlag = flag.Bool("verbose", false, "Enable debug logging for ProjFS/FUSE callbacks.")
)

func main() {
	flag.Parse()
	if *verboseFlag {
		slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelDebug})))
	}
	if *uriFlag == "" || *mountFlag == "" {
		flag.Usage()
		os.Exit(1)
	}
	if err := run(*uriFlag, *mountFlag); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

func run(uri, mountPath string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	slog.Info("opening file system", "uri", uri)
	fsys, err := ufs.New(ctx, uri)
	if err != nil {
		return err
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			slog.Warn("cannot close file system", "fs", fsys, "error", err)
		}
	}()

	slog.Info("mounting file system", "uri", uri, "mountPath", mountPath)
	server, err := ufs.HostMount(ctx, fsys, mountPath)
	if err != nil {
		return fmt.Errorf("cannot mount %q at %q: %w", uri, mountPath, err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			slog.Warn("cannot unmount", "mountPath", mountPath, "error", err)
		}
	}()

	slog.Info("mounted file system", "uri", uri, "mountPath", mountPath)
	server.Wait()
	slog.Info("unmounted file system", "mountPath", mountPath)
	return nil
}
