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

//go:build windows

package host

import (
	"fmt"
	"math"

	"golang.org/x/sys/windows"
)

// projfsEnableHelp says how to turn on the Windows Projected File System
// feature, which provides projectedfslib.dll and the PrjFlt filter driver.
const projfsEnableHelp = "enable the 'Windows Projected File System' feature and reboot: " +
	"Enable-WindowsOptionalFeature -Online -FeatureName Client-ProjFS on Windows 10/11, " +
	"Install-WindowsFeature -Name FS-Projectedfs on Windows Server " +
	"(see https://learn.microsoft.com/en-us/windows/win32/projfs/enabling-windows-projected-file-system)"

// projfsResolutions holds, for the Win32 errors ProjFS calls are known to
// return, the likely cause and what to do about it.
var projfsResolutions = map[windows.Errno]string{
	windows.ERROR_MOD_NOT_FOUND: "projectedfslib.dll is missing, so ProjFS is not installed: " + projfsEnableHelp,
	windows.ERROR_PROC_NOT_FOUND: "projectedfslib.dll lacks a ProjFS function: ProjFS needs Windows 10 version 1809, " +
		"Windows Server 2019 or later",
	windows.ERROR_FILE_SYSTEM_VIRTUALIZATION_UNAVAILABLE: "the ProjFS filter driver (PrjFlt) is not running: " +
		"reboot after enabling ProjFS, or run 'fltmc load PrjFlt' as administrator and check that 'fltmc filters' lists PrjFlt; " +
		projfsEnableHelp,
	windows.ERROR_FILE_SYSTEM_VIRTUALIZATION_METADATA_CORRUPT: "the ProjFS metadata of the mount directory is corrupt: " +
		"delete the mount directory and mount on a new empty directory",
	windows.ERROR_FILE_SYSTEM_VIRTUALIZATION_BUSY: "another ProjFS provider is already virtualizing the mount directory: " +
		"stop the other mount (for example another ufsmount process) or mount on a different directory",
	windows.ERROR_FILE_SYSTEM_VIRTUALIZATION_PROVIDER_UNKNOWN: "the mount directory belongs to a ProjFS provider that is not running, " +
		"such as one left behind by a crashed mount: mount on a new empty directory",
	windows.ERROR_FILE_SYSTEM_VIRTUALIZATION_INVALID_OPERATION: "ProjFS does not allow this inside a virtualization root, " +
		"for example a mount inside another ProjFS mount: mount on a directory outside any other ProjFS mount",
	windows.ERROR_REPARSE_POINT_ENCOUNTERED: "the mount directory already has a reparse point " +
		"(a symlink, junction, cloud-file placeholder or an old ProjFS root): mount on a new empty directory, " +
		"or remove the reparse point with 'fsutil reparsepoint delete <dir>'",
	windows.ERROR_REPARSE_TAG_MISMATCH: "the mount directory is not marked as a ProjFS virtualization root: " +
		"mount on a new empty directory",
	windows.ERROR_NOT_A_REPARSE_POINT: "the mount directory is not marked as a ProjFS virtualization root: " +
		"mount on a new empty directory",
	windows.ERROR_DIRECTORY:      "the mount path is not a directory: pass an existing, empty directory",
	windows.ERROR_PATH_NOT_FOUND: "the mount path does not exist: create the directory before mounting",
	windows.ERROR_ACCESS_DENIED: "access was denied: check that the current user can write to the mount directory " +
		"and that no other process has it open",
	windows.ERROR_SHARING_VIOLATION: "another process has the file or directory open: close it " +
		"(antivirus scanners and search indexers often do) and retry",
	windows.ERROR_NOT_SUPPORTED: "the volume or operation is not supported: ProjFS only virtualizes local NTFS volumes, " +
		"so do not mount on ReFS, FAT, a network share or a RAM disk; it also cannot rename or move a directory " +
		"projected from the mounted file system, so rename it in the mounted file system itself",
	windows.ERROR_INVALID_FUNCTION: "the volume does not support ProjFS: mount on a local NTFS volume and check that " +
		"'fltmc instances -f PrjFlt' shows the filter attached to it",
	windows.ERROR_INVALID_PARAMETER: "ProjFS rejected an argument, which is a bug in the ufs ProjFS bindings: " +
		"report it at https://github.com/cloudfra/ufs/issues with the log output",
	windows.ERROR_INSUFFICIENT_BUFFER: "a buffer passed to ProjFS was too small; from PrjWritePlaceholderInfo this is a bug " +
		"in the ufs ProjFS bindings: report it at https://github.com/cloudfra/ufs/issues with the log output",
	windows.ERROR_OFFSET_ALIGNMENT_VIOLATION: "the file was opened for unbuffered I/O and the read is not sector aligned: " +
		"read it with buffered I/O",
	windows.ERROR_DISK_FULL:         "the volume of the mount directory is full, and ProjFS stores read files there: free disk space",
	windows.ERROR_NOT_ENOUGH_MEMORY: "the system is out of memory: free memory and retry",
	windows.ERROR_OUTOFMEMORY:       "the system is out of memory: free memory and retry",
}

// projfsError is a failed ProjFS call. It unwraps to the windows.Errno of the
// HRESULT, so errors.Is matches both the Win32 error and the fs error it
// stands for (such as fs.ErrNotExist).
type projfsError struct {
	op      string // ProjFS function, e.g. "PrjStartVirtualizing"
	hresult uint32
	errno   windows.Errno
}

func (e *projfsError) Error() string {
	msg := fmt.Sprintf("%s: %v (HRESULT 0x%08X)", e.op, e.errno, e.hresult)
	if resolution, ok := projfsResolutions[e.errno]; ok {
		msg += ": " + resolution
	}
	return msg
}

func (e *projfsError) Unwrap() error {
	return e.errno
}

// hresultToError converts the HRESULT returned by the ProjFS function op to
// a Go error. S_OK (0) maps to nil. HRESULTs of the form HRESULT_FROM_WIN32(x)
// (0x8007 in the high word) are unwrapped to the Win32 error code in the low
// word so windows.Errno reports the right message.
func hresultToError(op string, hr uintptr) error {
	if hr == 0 {
		return nil
	}
	// A value above the 32-bit range is not a valid HRESULT; report it as a
	// failure instead of silently narrowing it.
	if hr > math.MaxUint32 {
		return fmt.Errorf("%s: invalid 64-bit HRESULT 0x%X", op, hr)
	}
	hr32 := uint32(hr)
	errno := windows.Errno(hr32)
	if hr32>>16 == 0x8007 {
		errno = windows.Errno(hr32 & 0xFFFF)
	}
	return &projfsError{op: op, hresult: hr32, errno: errno}
}
