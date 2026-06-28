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

//go:build windows

package ufs

import (
	"errors"
	"fmt"
	"path/filepath"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	fsctlEnumUSNData  = 0x000900B3
	mftEnumBufferSize = 64 * 1024
)

var errMFTUnavailable = errors.New("MFT scan unavailable")

// mftScanner walks the MFT of a Windows volume rooted at a specific directory.
type mftScanner struct {
	volumePath string // \\.\C: style volume path
	rootRef    uint64 // 48-bit MFT record number of the root directory
	rootAbs    string // absolute OS path of the root directory
}

func newMFTScanner(rootAbsPath string) (*mftScanner, error) {
	volRoot := filepath.VolumeName(rootAbsPath)
	if volRoot == "" {
		return nil, fmt.Errorf("cannot determine volume for %q: %w", rootAbsPath, errMFTUnavailable)
	}
	volumePath := `\\.\` + volRoot

	rootRef, err := getFileReferenceNumber(rootAbsPath)
	if err != nil {
		return nil, fmt.Errorf("cannot get MFT reference for %q: %w", rootAbsPath, errMFTUnavailable)
	}

	return &mftScanner{
		volumePath: volumePath,
		rootRef:    rootRef,
		rootAbs:    rootAbsPath,
	}, nil
}

func getFileReferenceNumber(path string) (uint64, error) {
	pathUTF16, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return 0, err
	}
	h, err := windows.CreateFile(
		pathUTF16,
		0,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return 0, err
	}
	defer windows.CloseHandle(h)

	var info windows.ByHandleFileInformation
	if err := windows.GetFileInformationByHandle(h, &info); err != nil {
		return 0, err
	}
	return mftRecordNumber(uint64(info.FileIndexHigh)<<32 | uint64(info.FileIndexLow)), nil
}

func (s *mftScanner) enumSubtree() (map[uint64]mftRecord, error) {
	volumeUTF16, err := windows.UTF16PtrFromString(s.volumePath)
	if err != nil {
		return nil, err
	}
	vol, err := windows.CreateFile(
		volumeUTF16,
		windows.GENERIC_READ,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		0,
		0,
	)
	if err != nil {
		return nil, fmt.Errorf("open volume %s: %w", s.volumePath, errMFTUnavailable)
	}
	defer windows.CloseHandle(vol)

	allRecords, err := s.readAllRecords(vol)
	if err != nil {
		return nil, err
	}

	return s.filterSubtree(allRecords), nil
}

func (s *mftScanner) readAllRecords(vol windows.Handle) (map[uint64]mftRecord, error) {
	records := make(map[uint64]mftRecord)

	// MFT_ENUM_DATA_V0: StartFileReferenceNumber (uint64) + LowUsn (int64) + HighUsn (int64)
	enumData := make([]byte, 24)
	// StartFileReferenceNumber = 0, LowUsn = 0, HighUsn = max int64
	*(*int64)(unsafe.Pointer(&enumData[16])) = 1<<63 - 1

	buf := make([]byte, mftEnumBufferSize)

	for {
		var bytesReturned uint32
		err := windows.DeviceIoControl(
			vol,
			fsctlEnumUSNData,
			&enumData[0], uint32(len(enumData)),
			&buf[0], uint32(len(buf)),
			&bytesReturned, nil,
		)
		if err != nil {
			if errors.Is(err, windows.ERROR_HANDLE_EOF) {
				break
			}
			return nil, fmt.Errorf("FSCTL_ENUM_USN_DATA: %w", err)
		}
		if bytesReturned <= 8 {
			break
		}

		// First 8 bytes of output is the next StartFileReferenceNumber.
		copy(enumData[:8], buf[:8])

		offset := uint32(8)
		for offset < bytesReturned {
			if offset+4 > bytesReturned {
				break
			}
			recordLen := *(*uint32)(unsafe.Pointer(&buf[offset]))
			if recordLen == 0 || offset+recordLen > bytesReturned {
				break
			}
			rec := parseUSNRecordV2(buf[offset : offset+recordLen])
			if rec != nil {
				records[mftRecordNumber(rec.fileRef)] = mftRecord{
					parentRef: mftRecordNumber(rec.parentRef),
					name:      rec.name,
					isDir:     rec.isDir,
				}
			}
			offset += recordLen
		}
	}

	return records, nil
}
