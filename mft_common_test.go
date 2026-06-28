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
	"encoding/binary"
	"sort"
	"testing"
	"unicode/utf16"

	"github.com/google/go-cmp/cmp"
)

// makeUSNRecord builds a synthetic USN_RECORD_V2 byte slice for use in tests.
func makeUSNRecord(fileRef, parentRef uint64, fileAttributes uint32, name string) []byte {
	runes := utf16.Encode([]rune(name))
	nameBytes := make([]byte, len(runes)*2)
	for i, ch := range runes {
		nameBytes[2*i] = byte(ch)
		nameBytes[2*i+1] = byte(ch >> 8)
	}
	totalLen := usnRecordV2HeaderSize + len(nameBytes)
	data := make([]byte, totalLen)
	binary.LittleEndian.PutUint32(data[0:], uint32(totalLen))
	data[4] = 2 // MajorVersion = 2
	binary.LittleEndian.PutUint64(data[8:], fileRef)
	binary.LittleEndian.PutUint64(data[16:], parentRef)
	binary.LittleEndian.PutUint32(data[52:], fileAttributes)
	binary.LittleEndian.PutUint16(data[56:], uint16(len(nameBytes)))
	binary.LittleEndian.PutUint16(data[58:], usnRecordV2HeaderSize)
	copy(data[usnRecordV2HeaderSize:], nameBytes)
	return data
}

// ---- mftRecordNumber -------------------------------------------------------

func TestMFTRecordNumber(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   uint64
		want uint64
	}{
		{"zero", 0, 0},
		{"low48_unchanged", 0x0000FFFFFFFFFFFF, 0x0000FFFFFFFFFFFF},
		{"strips_high16", 0xABCD000000000001, 0x000000000001},
		{"all_ones", 0xFFFFFFFFFFFFFFFF, 0x0000FFFFFFFFFFFF},
		{"seq_number_only", 0xABCD000000000000, 0},
		{"typical_ref", 0x0002000000000025, 0x000000000025},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := mftRecordNumber(tc.in); got != tc.want {
				t.Errorf("mftRecordNumber(0x%X) = 0x%X, want 0x%X", tc.in, got, tc.want)
			}
		})
	}
}

// ---- joinFSPath ------------------------------------------------------------

func TestJoinFSPath(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		components []string
		want       string
	}{
		{"single", []string{"a"}, "a"},
		{"two", []string{"a", "b"}, "a/b"},
		{"three", []string{"dir", "sub", "file.txt"}, "dir/sub/file.txt"},
		{"deep", []string{"a", "b", "c", "d", "e"}, "a/b/c/d/e"},
		{"unicode", []string{"日本語", "ファイル.txt"}, "日本語/ファイル.txt"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := joinFSPath(tc.components); got != tc.want {
				t.Errorf("joinFSPath(%v) = %q, want %q", tc.components, got, tc.want)
			}
		})
	}
}

// ---- parseUSNRecordV2 ------------------------------------------------------

func TestParseUSNRecordV2Valid(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name       string
		fileRef    uint64
		parentRef  uint64
		attributes uint32
		filename   string
		wantIsDir  bool
	}{
		{"ascii_file", 300, 200, 0x20, "document.txt", false},
		{"directory", 200, 100, fileAttributeDirectory, "subdir", true},
		{"unicode_name", 400, 100, 0x20, "日本語ファイル.txt", false},
		{"dir_and_archive", 500, 200, fileAttributeDirectory | 0x20, "archive.zip", true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			data := makeUSNRecord(tc.fileRef, tc.parentRef, tc.attributes, tc.filename)
			rec := parseUSNRecordV2(data)
			if rec == nil {
				t.Fatal("parseUSNRecordV2() = nil, want non-nil")
			}
			if rec.fileRef != tc.fileRef {
				t.Errorf("fileRef = %d, want %d", rec.fileRef, tc.fileRef)
			}
			if rec.parentRef != tc.parentRef {
				t.Errorf("parentRef = %d, want %d", rec.parentRef, tc.parentRef)
			}
			if rec.name != tc.filename {
				t.Errorf("name = %q, want %q", rec.name, tc.filename)
			}
			if rec.isDir != tc.wantIsDir {
				t.Errorf("isDir = %v, want %v", rec.isDir, tc.wantIsDir)
			}
		})
	}
}

func TestParseUSNRecordV2Invalid(t *testing.T) {
	t.Parallel()
	t.Run("too_short", func(t *testing.T) {
		t.Parallel()
		if got := parseUSNRecordV2(make([]byte, usnRecordV2HeaderSize-1)); got != nil {
			t.Errorf("parseUSNRecordV2(short) = %+v, want nil", got)
		}
	})
	t.Run("exactly_header_zero_length_name", func(t *testing.T) {
		t.Parallel()
		// FileNameLength=0, FileNameOffset=64: nameStart==nameEnd → nil
		data := make([]byte, usnRecordV2HeaderSize)
		binary.LittleEndian.PutUint16(data[56:], 0)                    // FileNameLength
		binary.LittleEndian.PutUint16(data[58:], usnRecordV2HeaderSize) // FileNameOffset
		if got := parseUSNRecordV2(data); got != nil {
			t.Errorf("parseUSNRecordV2(zero-length name) = %+v, want nil", got)
		}
	})
	t.Run("name_offset_before_header", func(t *testing.T) {
		t.Parallel()
		data := makeUSNRecord(1, 2, 0x20, "file.txt")
		// Override offset to point inside the header area.
		binary.LittleEndian.PutUint16(data[58:], 4)
		if got := parseUSNRecordV2(data); got != nil {
			t.Errorf("parseUSNRecordV2(bad offset) = %+v, want nil", got)
		}
	})
	t.Run("name_end_past_record", func(t *testing.T) {
		t.Parallel()
		data := makeUSNRecord(1, 2, 0x20, "file.txt")
		// Set FileNameLength to a value that exceeds the buffer.
		binary.LittleEndian.PutUint16(data[56:], uint16(len(data)))
		if got := parseUSNRecordV2(data); got != nil {
			t.Errorf("parseUSNRecordV2(name past end) = %+v, want nil", got)
		}
	})
	t.Run("odd_name_length", func(t *testing.T) {
		t.Parallel()
		// UTF-16 names must be an even number of bytes.
		data := makeUSNRecord(1, 2, 0x20, "file.txt")
		binary.LittleEndian.PutUint16(data[56:], 3) // odd → malformed
		if got := parseUSNRecordV2(data); got != nil {
			t.Errorf("parseUSNRecordV2(odd length) = %+v, want nil", got)
		}
	})
}

// ---- filterSubtree ---------------------------------------------------------

func TestFilterSubtree(t *testing.T) {
	t.Parallel()
	scanner := &mftScanner{rootRef: 100}
	allRecords := map[uint64]mftRecord{
		200: {parentRef: 100, name: "sub", isDir: true},
		300: {parentRef: 200, name: "file.txt", isDir: false},
		999: {parentRef: 888, name: "orphan.txt", isDir: false},
	}
	got := scanner.filterSubtree(allRecords)
	if _, ok := got[200]; !ok {
		t.Error("expected ref 200 (sub) in result")
	}
	if _, ok := got[300]; !ok {
		t.Error("expected ref 300 (file.txt) in result")
	}
	if _, ok := got[999]; ok {
		t.Error("ref 999 (orphan) should not be reachable")
	}
}

func TestFilterSubtreeEmpty(t *testing.T) {
	t.Parallel()
	scanner := &mftScanner{rootRef: 100}
	got := scanner.filterSubtree(map[uint64]mftRecord{})
	if len(got) != 0 {
		t.Errorf("filterSubtree(empty) = %v, want empty", got)
	}
}

func TestFilterSubtreeBranching(t *testing.T) {
	t.Parallel()
	// Root(1) -> dirA(2) -> file1(4)
	//         -> dirB(3) -> file2(5)
	//         -> dirB(3) -> dirC(6) -> file3(7)
	// orphanDir(8) -> orphanFile(9)
	scanner := &mftScanner{rootRef: 1}
	allRecords := map[uint64]mftRecord{
		2: {parentRef: 1, name: "dirA", isDir: true},
		3: {parentRef: 1, name: "dirB", isDir: true},
		4: {parentRef: 2, name: "file1.txt", isDir: false},
		5: {parentRef: 3, name: "file2.txt", isDir: false},
		6: {parentRef: 3, name: "dirC", isDir: true},
		7: {parentRef: 6, name: "file3.txt", isDir: false},
		8: {parentRef: 99, name: "orphanDir", isDir: true},
		9: {parentRef: 8, name: "orphanFile.txt", isDir: false},
	}
	got := scanner.filterSubtree(allRecords)
	for _, ref := range []uint64{2, 3, 4, 5, 6, 7} {
		if _, ok := got[ref]; !ok {
			t.Errorf("expected ref %d in result", ref)
		}
	}
	for _, ref := range []uint64{8, 9} {
		if _, ok := got[ref]; ok {
			t.Errorf("ref %d should not be reachable", ref)
		}
	}
}

func TestFilterSubtreeSelfLoop(t *testing.T) {
	t.Parallel()
	// A record that is its own parent should not cause infinite BFS if it is
	// not reachable from rootRef. If it IS somehow reached, the already-seen
	// guard in filterSubtree prevents unbounded looping.
	scanner := &mftScanner{rootRef: 1}
	allRecords := map[uint64]mftRecord{
		2: {parentRef: 1, name: "dir", isDir: true},
		3: {parentRef: 3, name: "self-loop.txt", isDir: false}, // parent = self
	}
	// ref 3 is unreachable from root (its parent chain never reaches 1).
	got := scanner.filterSubtree(allRecords)
	if _, ok := got[2]; !ok {
		t.Error("expected ref 2 in result")
	}
	if _, ok := got[3]; ok {
		t.Error("ref 3 (self-loop) should not be reachable")
	}
}

// ---- buildPath / buildPaths ------------------------------------------------

func TestBuildPath(t *testing.T) {
	t.Parallel()
	scanner := &mftScanner{rootRef: 1}
	records := map[uint64]mftRecord{
		2: {parentRef: 1, name: "dirA", isDir: true},
		3: {parentRef: 2, name: "sub", isDir: true},
		4: {parentRef: 3, name: "file.txt", isDir: false},
	}

	t.Run("direct_child", func(t *testing.T) {
		// ref 2 is dirA (dir), but buildPath works for any ref in records.
		got := scanner.buildPath(2, records)
		if got != "dirA" {
			t.Errorf("buildPath(2) = %q, want %q", got, "dirA")
		}
	})
	t.Run("nested", func(t *testing.T) {
		got := scanner.buildPath(4, records)
		if got != "dirA/sub/file.txt" {
			t.Errorf("buildPath(4) = %q, want %q", got, "dirA/sub/file.txt")
		}
	})
	t.Run("unknown_ref", func(t *testing.T) {
		// ref not in records → empty string (parent chain immediately missing)
		got := scanner.buildPath(999, records)
		if got != "" {
			t.Errorf("buildPath(unknown) = %q, want empty", got)
		}
	})
}

func TestBuildPaths(t *testing.T) {
	t.Parallel()
	// root(100) -> dirA(200) -> file1(300)
	//           -> file2(400)
	scanner := &mftScanner{rootRef: 100}
	records := map[uint64]mftRecord{
		200: {parentRef: 100, name: "dirA", isDir: true},
		300: {parentRef: 200, name: "file1.txt", isDir: false},
		400: {parentRef: 100, name: "file2.txt", isDir: false},
	}
	got := scanner.buildPaths(records)
	sort.Strings(got)
	want := []string{"dirA/file1.txt", "file2.txt"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("buildPaths() mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildPathsEmpty(t *testing.T) {
	t.Parallel()
	scanner := &mftScanner{rootRef: 1}
	got := scanner.buildPaths(map[uint64]mftRecord{})
	if len(got) != 0 {
		t.Errorf("buildPaths(empty) = %v, want empty", got)
	}
}

func TestBuildPathsDirectoriesOnly(t *testing.T) {
	t.Parallel()
	scanner := &mftScanner{rootRef: 100}
	records := map[uint64]mftRecord{
		200: {parentRef: 100, name: "subdir", isDir: true},
	}
	got := scanner.buildPaths(records)
	if len(got) != 0 {
		t.Errorf("buildPaths(dirs-only) = %v, want empty", got)
	}
}
