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

package ufs

import (
	"context"
	"io/fs"
	"path"

	"golang.org/x/sync/errgroup"
)

// ParallelWalk walks dir in fsys concurrently, calling f for each file whose
// ancestor directories pass the filters in args. It behaves like [Walk] but
// processes subdirectories in parallel, bounded by the backing device's
// threadCount from the file system's device map.
//
// f must be safe for concurrent use. Files may be visited in any order.
// The walk stops and returns the first non-nil error from f or from reading
// a directory.
func ParallelWalk(fsys fs.FS, dir string, args WalkArgs, f func(string) error) error {
	deviceMap := getDeviceInfoOrDefault(fsys)
	sems := buildDeviceSemaphores(deviceMap)
	adc, hasADC := fsys.(archiveDirChecker)

	g, ctx := errgroup.WithContext(context.Background())

	var walkDir func(dirPath string) error
	walkDir = func(dirPath string) error {
		sem := lookupDeviceSemaphore(sems, deviceMap, dirPath)
		select {
		case sem <- struct{}{}:
		case <-ctx.Done():
			return ctx.Err()
		}
		entries, err := fs.ReadDir(fsys, dirPath)
		<-sem

		if err != nil {
			return err
		}

		for _, entry := range entries {
			if ctx.Err() != nil {
				return ctx.Err()
			}

			name := path.Join(dirPath, entry.Name())

			if entry.IsDir() {
				if !args.IncludeMountedArchive && hasADC && adc.isMountedArchiveDir(name) {
					continue
				}
				skip := false
				for _, pattern := range args.ExcludeDirectory {
					if matched, _ := path.Match(pattern, entry.Name()); matched {
						skip = true
						break
					}
				}
				if skip {
					continue
				}
				g.Go(func() error {
					return walkDir(name)
				})
			} else {
				if err := f(name); err != nil {
					return err
				}
			}
		}
		return nil
	}

	g.Go(func() error {
		return walkDir(dir)
	})

	return g.Wait()
}

func buildDeviceSemaphores(deviceMap map[string]deviceInfo) map[string]chan struct{} {
	sems := make(map[string]chan struct{})
	for _, di := range deviceMap {
		if existing, ok := sems[di.name]; ok {
			if di.threadCount > cap(existing) {
				sems[di.name] = make(chan struct{}, di.threadCount)
			}
			continue
		}
		sems[di.name] = make(chan struct{}, max(di.threadCount, 1))
	}
	return sems
}

func lookupDeviceSemaphore(sems map[string]chan struct{}, deviceMap map[string]deviceInfo, dirPath string) chan struct{} {
	di := deviceInfoForPath(deviceMap, dirPath)
	return sems[di.name]
}

func deviceInfoForPath(m map[string]deviceInfo, p string) deviceInfo {
	if di, ok := m[p]; ok {
		return di
	}
	return getParentDeviceInfo(m, p)
}
