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
	"flag"
	"log"

	"github.com/cloudfra/ufs"
)

var (
	dirFlag = flag.String("path", ".", "Path to walk the directory tree to report file names.")
)

func main() {
	if err := run(*dirFlag); err != nil {
		log.Printf("ERROR: %s", err)
	}
}

func run(dir string) error {
	ctx := context.Background()
	fsys, err := ufs.New(ctx, dir)
	if err != nil {
		return err
	}
	return ufs.ForEachFilename(fsys, dir, true, func(name string) error {
		log.Printf("- %s", name)
		return nil
	})
}
