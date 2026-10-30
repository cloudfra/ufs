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

package ufs

import (
	"bytes"
	"io"
	"os"
	"testing"

	"github.com/cloudfra/ufs/drivers/polyfills"
	"github.com/cloudfra/ufs/internal/pathutil"
	ufsTesting "github.com/cloudfra/ufs/testing"
)

// TestNestFSBufferingOfArchiveFiles opens one entry of an archive through a
// nestFS configured with each buffer mode. The archive is not a file system the
// caller mounted, so this also checks that mountArchive gives the archive's own
// nestFS the settings of the tree it belongs to: a mode that never reached the
// archive mount would only ever be tested against a mounted file system.
func TestNestFSBufferingOfArchiveFiles(t *testing.T) {
	const (
		archiveName = testAssetsArchivesDir + "/testassets.tar"
		mountName   = archiveName + archiveDirExt
		entryName   = mountName + "/assets/2.txt"
		hostName    = "testing/testassets/files/assets/2.txt"
	)
	want, err := os.ReadFile(hostName)
	if err != nil {
		t.Fatal(err)
	}

	testCases := []struct {
		name string
		args FSArgs
		// wantBuffer is the polyfill configuration testCase.args has to produce,
		// which is what tells the two auto cases apart once the threshold is
		// crossed.
		wantBuffer polyfills.BufferArgs
	}{
		{"default", FSArgs{}, polyfills.BufferArgs{}},
		{"memory", FSArgs{BufMode: polyfills.BufferMemory}, polyfills.BufferArgs{Mode: polyfills.BufferMemory}},
		{"disk", FSArgs{BufMode: polyfills.BufferDisk}, polyfills.BufferArgs{Mode: polyfills.BufferDisk}},
		{
			"auto_under_threshold",
			FSArgs{BufMode: polyfills.BufferAuto, AutoBufThreshold: 1 << 20},
			polyfills.BufferArgs{Mode: polyfills.BufferAuto, AutoThreshold: 1 << 20},
		},
		{
			"auto_over_threshold",
			FSArgs{BufMode: polyfills.BufferAuto, AutoBufThreshold: 1},
			polyfills.BufferArgs{Mode: polyfills.BufferAuto, AutoThreshold: 1},
		},
		{"auto_default_threshold", FSArgs{BufMode: polyfills.BufferAuto}, polyfills.BufferArgs{Mode: polyfills.BufferAuto}},
	}
	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fsys := makeNestFS(t.Context(), mustBaseFS(t, pathutil.CwdPath), testCase.args)
			defer ufsTesting.ValidateClose(t, fsys)()

			file, err := fsys.Open(entryName)
			if err != nil {
				t.Fatalf("Open(%q) = %v", entryName, err)
			}
			defer ufsTesting.ValidateClose(t, file)()

			// The mount nestFS makes for an archive has to keep the buffering
			// settings, or a file read out of the archive is buffered the way the
			// caller did not ask for.
			mount := fsys.mounts.getMount(mountName)
			if mount == nil {
				t.Fatalf("getMount(%q) = nil, want the archive mounted", mountName)
			}
			if mount.args != testCase.args {
				t.Errorf("the archive mount has buffer settings %+v, want %+v", mount.args, testCase.args)
			}

			// Which store a setting selects is checked in the polyfills package;
			// here the settings themselves have to survive the trip to the
			// polyfill.
			if got := testCase.args.bufferArgs(); got != testCase.wantBuffer {
				t.Errorf("bufferArgs() = %+v, want %+v", got, testCase.wantBuffer)
			}

			seeker, ok := file.(io.Seeker)
			if !ok {
				t.Fatalf("Open(%q) = %T, want a file that seeks", entryName, file)
			}
			readerAt, ok := file.(io.ReaderAt)
			if !ok {
				t.Fatalf("Open(%q) = %T, want a file that reads at an offset", entryName, file)
			}

			got, err := io.ReadAll(file)
			if err != nil {
				t.Fatalf("ReadAll() = %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Errorf("the archive entry read as %q, want %q", got, want)
			}

			// Seeking back into the content is what a stream-only archive entry
			// cannot do on its own.
			middle := int64(len(want) / 2)
			if pos, err := seeker.Seek(middle, io.SeekStart); err != nil || pos != middle {
				t.Fatalf("Seek(%d, io.SeekStart) = %d, %v, want %d, nil", middle, pos, err, middle)
			}
			tail, err := io.ReadAll(file)
			if err != nil {
				t.Fatalf("ReadAll() after Seek = %v", err)
			}
			if !bytes.Equal(tail, want[middle:]) {
				t.Errorf("reading from %d gave %q, want %q", middle, tail, want[middle:])
			}

			// ReadAt must serve from the requested offset without moving the
			// position that Seek set.
			if pos, err := seeker.Seek(middle, io.SeekStart); err != nil || pos != middle {
				t.Fatalf("Seek(%d, io.SeekStart) = %d, %v, want %d, nil", middle, pos, err, middle)
			}
			chunk := make([]byte, 4)
			if n, err := readerAt.ReadAt(chunk, middle); err != nil || n != len(chunk) {
				t.Fatalf("ReadAt(%d bytes, %d) = %d, %v", len(chunk), middle, n, err)
			}
			if !bytes.Equal(chunk, want[middle:middle+4]) {
				t.Errorf("ReadAt at %d gave %q, want %q", middle, chunk, want[middle:middle+4])
			}
			if pos, err := seeker.Seek(0, io.SeekCurrent); err != nil || pos != middle {
				t.Errorf("Seek(0, io.SeekCurrent) after ReadAt = %d, %v, want %d, nil", pos, err, middle)
			}
		})
	}
}
