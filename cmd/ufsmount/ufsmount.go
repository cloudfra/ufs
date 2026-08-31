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
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/cloudfra/ufs"
)

var (
	uriFlag   = flag.String("uri", "", "URI of the virtual file system to mount (e.g. memory://, gs://bucket).")
	mountFlag = flag.String("mount", "", "Path on the host to mount the file system at.")
)

func main() {
	flag.Parse()
	if *uriFlag == "" || *mountFlag == "" {
		flag.Usage()
		os.Exit(1)
	}
	if err := run(*uriFlag, *mountFlag); err != nil {
		log.Fatalf("ERROR: %s", err)
	}
}

func run(uri, mountPath string) error {
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	fsys, err := ufs.New(ctx, uri)
	if err != nil {
		return err
	}
	defer func() {
		if err := fsys.Close(); err != nil {
			log.Printf("cannot close file system %q, %s", fsys, err)
		}
	}()

	server, err := ufs.HostMount(ctx, fsys, mountPath)
	if err != nil {
		return fmt.Errorf("cannot mount %q at %q: %w", uri, mountPath, err)
	}
	defer func() {
		if err := server.Close(); err != nil {
			log.Printf("cannot unmount %q, %s", mountPath, err)
		}
	}()

	log.Printf("Mounted %s at %s", uri, mountPath)
	server.Wait()
	log.Printf("Unmounted %s", mountPath)
	return nil
}
