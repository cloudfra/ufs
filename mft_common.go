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
	"unicode/utf16"
)

const (
	fileAttributeDirectory = 0x10
	usnRecordV2HeaderSize  = 64
)

// mftRecordNumber extracts the 48-bit MFT record number from a file reference,
// stripping the 16-bit sequence number in the upper bits.
func mftRecordNumber(ref uint64) uint64 {
	return ref & 0x0000FFFFFFFFFFFF
}

type mftRecord struct {
	parentRef uint64
	name      string
	isDir     bool
}

// mftScanner is defined per-platform in mft_windows.go / mft_nowindows.go.
// All methods here access only mftScanner.rootRef, which is present on every
// platform.

type rawUSNRecord struct {
	fileRef   uint64
	parentRef uint64
	name      string
	isDir     bool
}

// parseUSNRecordV2 parses a USN_RECORD_V2 from a byte slice.
// Returns nil if the record is malformed or too short.
func parseUSNRecordV2(data []byte) *rawUSNRecord {
	if len(data) < usnRecordV2HeaderSize {
		return nil
	}

	fileRef := binary.LittleEndian.Uint64(data[8:])
	parentRef := binary.LittleEndian.Uint64(data[16:])
	fileAttributes := binary.LittleEndian.Uint32(data[52:])
	fileNameLength := binary.LittleEndian.Uint16(data[56:])
	fileNameOffset := binary.LittleEndian.Uint16(data[58:])

	if fileNameOffset < usnRecordV2HeaderSize {
		return nil
	}

	nameStart := int(fileNameOffset)
	nameEnd := nameStart + int(fileNameLength)
	if nameEnd > len(data) || nameStart >= nameEnd || fileNameLength%2 != 0 {
		return nil
	}

	nameBytes := data[nameStart:nameEnd]
	u16s := make([]uint16, len(nameBytes)/2)
	for i := range u16s {
		u16s[i] = uint16(nameBytes[2*i]) | uint16(nameBytes[2*i+1])<<8
	}
	name := string(utf16.Decode(u16s))

	return &rawUSNRecord{
		fileRef:   fileRef,
		parentRef: parentRef,
		name:      name,
		isDir:     fileAttributes&fileAttributeDirectory != 0,
	}
}

// filterSubtree performs a BFS from s.rootRef to collect only the records that
// are reachable within the scanner's root directory.
func (s *mftScanner) filterSubtree(allRecords map[uint64]mftRecord) map[uint64]mftRecord {
	children := make(map[uint64][]uint64, len(allRecords))
	for ref, rec := range allRecords {
		children[rec.parentRef] = append(children[rec.parentRef], ref)
	}

	reachable := make(map[uint64]mftRecord)
	queue := []uint64{s.rootRef}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, childRef := range children[cur] {
			if _, already := reachable[childRef]; already {
				continue
			}
			rec := allRecords[childRef]
			reachable[childRef] = rec
			if rec.isDir {
				queue = append(queue, childRef)
			}
		}
	}

	return reachable
}

// buildPaths returns FS-relative paths for all non-directory records.
func (s *mftScanner) buildPaths(records map[uint64]mftRecord) []string {
	var paths []string
	for ref, rec := range records {
		if rec.isDir {
			continue
		}
		p := s.buildPath(ref, records)
		if p != "" {
			paths = append(paths, p)
		}
	}
	return paths
}

// buildPath reconstructs the FS-relative forward-slash path for ref by walking
// the parent chain until a record has no parent entry (i.e. we've reached the
// root, whose parent is not in the subtree map).
func (s *mftScanner) buildPath(ref uint64, records map[uint64]mftRecord) string {
	var components []string
	cur := ref
	for range 4096 { // cycle breaker: NTFS max path depth
		rec, ok := records[cur]
		if !ok {
			break
		}
		components = append(components, rec.name)
		cur = rec.parentRef
	}
	if len(components) == 0 {
		return ""
	}
	for i, j := 0, len(components)-1; i < j; i, j = i+1, j-1 {
		components[i], components[j] = components[j], components[i]
	}
	return joinFSPath(components)
}

// joinFSPath joins path components with forward slashes into a single string,
// pre-computing the exact buffer size to avoid intermediate allocations.
func joinFSPath(components []string) string {
	n := 0
	for _, c := range components {
		n += len(c)
	}
	n += len(components) - 1
	buf := make([]byte, 0, n)
	for i, c := range components {
		if i > 0 {
			buf = append(buf, '/')
		}
		buf = append(buf, c...)
	}
	return string(buf)
}
