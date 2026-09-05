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
	"fmt"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	projectedfslib = windows.NewLazySystemDLL("projectedfslib.dll")

	procPrjStartVirtualizing          = projectedfslib.NewProc("PrjStartVirtualizing")
	procPrjStopVirtualizing           = projectedfslib.NewProc("PrjStopVirtualizing")
	procPrjMarkDirectoryAsPlaceholder = projectedfslib.NewProc("PrjMarkDirectoryAsPlaceholder")
	procPrjWritePlaceholderInfo       = projectedfslib.NewProc("PrjWritePlaceholderInfo")
	procPrjWriteFileData              = projectedfslib.NewProc("PrjWriteFileData")
	procPrjAllocateAlignedBuffer      = projectedfslib.NewProc("PrjAllocateAlignedBuffer")
	procPrjFreeAlignedBuffer          = projectedfslib.NewProc("PrjFreeAlignedBuffer")
	procPrjFillDirEntryBuffer         = projectedfslib.NewProc("PrjFillDirEntryBuffer")
	procPrjFileNameCompare            = projectedfslib.NewProc("PrjFileNameCompare")
	procPrjFileNameMatch              = projectedfslib.NewProc("PrjFileNameMatch")
)

// projfsAvailable reports whether projectedfslib.dll could be loaded,
// i.e. whether the Windows Projected File System feature is enabled.
func projfsAvailable() error {
	if err := projectedfslib.Load(); err != nil {
		return fmt.Errorf("ProjFS is not available: enable the 'Windows Projected File System' feature.\n"+
			"  Windows Server (admin): Install-WindowsFeature -Name FS-Projectedfs\n"+
			"  Windows 10/11 (admin):  Enable-WindowsOptionalFeature -Online -FeatureName Client-ProjFS\n"+
			"  See: https://learn.microsoft.com/en-us/windows/win32/projfs/enabling-windows-projected-file-system\n"+
			"  %w", err)
	}
	return nil
}

// PRJ_NOTIFY_TYPES bitmask values.
const (
	prjNotifyNone                           = 0x00000000
	prjNotifySuppress                       = 0x00000001
	prjNotifyFileOpened                     = 0x00000002
	prjNotifyNewFileCreated                 = 0x00000004
	prjNotifyFileOverwritten                = 0x00000008
	prjNotifyPreDelete                      = 0x00000010
	prjNotifyPreRename                      = 0x00000020
	prjNotifyPreSetHardlink                 = 0x00000040
	prjNotifyFileRenamed                    = 0x00000080
	prjNotifyHardlinkCreated                = 0x00000100
	prjNotifyFileHandleClosedNoModification = 0x00000200
	prjNotifyFileHandleClosedFileModified   = 0x00000400
	prjNotifyFileHandleClosedFileDeleted    = 0x00000800
	prjNotifyFilePreConvertToFull           = 0x00001000
	prjNotifyUseExistingMask                = 0xFFFFFFFF
)

// PRJ_NOTIFICATION values (individual notification IDs delivered in callbacks).
const (
	prjNotificationFileOpened                     = 0x00000002
	prjNotificationNewFileCreated                 = 0x00000004
	prjNotificationFileOverwritten                = 0x00000008
	prjNotificationPreDelete                      = 0x00000010
	prjNotificationPreRename                      = 0x00000020
	prjNotificationPreSetHardlink                 = 0x00000040
	prjNotificationFileRenamed                    = 0x00000080
	prjNotificationHardlinkCreated                = 0x00000100
	prjNotificationFileHandleClosedNoModification = 0x00000200
	prjNotificationFileHandleClosedFileModified   = 0x00000400
	prjNotificationFileHandleClosedFileDeleted    = 0x00000800
	prjNotificationFilePreConvertToFull           = 0x00001000
)

// PRJ_CALLBACK_DATA_FLAGS values.
const (
	prjCBDataFlagRestartScan       = 0x00000001
	prjCBDataFlagReturnSingleEntry = 0x00000002
)

// prjPlaceholderIDLength is the fixed length of PRJ_PLACEHOLDER_ID.
const prjPlaceholderIDLength = 128

// prjFileBasicInfo maps PRJ_FILE_BASIC_INFO.
type prjFileBasicInfo struct {
	IsDirectory    uint8
	_              [7]byte // padding
	FileSize       int64
	CreationTime   int64 // FILETIME as 100-ns intervals since 1601
	LastAccessTime int64
	LastWriteTime  int64
	ChangeTime     int64
	FileAttributes uint32
	_              [4]byte // padding
}

// prjPlaceholderVersionInfo maps PRJ_PLACEHOLDER_VERSION_INFO.
type prjPlaceholderVersionInfo struct {
	ProviderID [prjPlaceholderIDLength]byte
	ContentID  [prjPlaceholderIDLength]byte
}

// prjPlaceholderInfo maps PRJ_PLACEHOLDER_INFO.
type prjPlaceholderInfo struct {
	FileBasicInfo prjFileBasicInfo
	EaInformation struct {
		EaBufferSize    uint32
		OffsetToFirstEa uint32
	}
	SecurityInformation struct {
		SecurityBufferSize         uint32
		OffsetToSecurityDescriptor uint32
	}
	StreamsInformation struct {
		StreamsInfoBufferSize   uint32
		OffsetToFirstStreamInfo uint32
	}
	VersionInfo prjPlaceholderVersionInfo
}

// prjCallbackData maps PRJ_CALLBACK_DATA.
type prjCallbackData struct {
	Size                           uint32
	Flags                          uint32
	NamespaceVirtualizationContext uintptr
	CommandID                      int32
	FileID                         windows.GUID
	DataStreamID                   windows.GUID
	FilePathName                   *uint16
	VersionInfo                    *prjPlaceholderVersionInfo
	TriggeringProcessID            uint32
	_                              [4]byte
	TriggeringProcessImageFileName *uint16
	InstanceContext                uintptr
}

// prjCallbacks maps PRJ_CALLBACKS — 8 callback function pointers.
type prjCallbacks struct {
	StartDirectoryEnumerationCallback uintptr
	EndDirectoryEnumerationCallback   uintptr
	GetDirectoryEnumerationCallback   uintptr
	GetPlaceholderInfoCallback        uintptr
	GetFileDataCallback               uintptr
	QueryFileNameCallback             uintptr
	NotificationCallback              uintptr
	CancelCommandCallback             uintptr
}

// prjNotificationMapping maps PRJ_NOTIFICATION_MAPPING.
type prjNotificationMapping struct {
	NotificationBitMask uint32
	_                   [4]byte
	NotificationRoot    *uint16
}

// prjStartVirtualizingOptions maps PRJ_STARTVIRTUALIZING_OPTIONS.
type prjStartVirtualizingOptions struct {
	Flags                     uint32
	PoolThreadCount           uint32
	ConcurrentThreadCount     uint32
	_                         [4]byte
	NotificationMappings      *prjNotificationMapping
	NotificationMappingsCount uint32
	_                         [4]byte
}

// hresultToError converts an HRESULT (uintptr) to a Go error.
// S_OK (0) maps to nil. HRESULTs of the form HRESULT_FROM_WIN32(x)
// (facility code 0x0007 in the high word) are unwrapped to the underlying
// Win32 error code in the low word so windows.Errno reports the right message.
func hresultToError(hr uintptr) error {
	if hr == 0 {
		return nil
	}
	hr32 := uint32(hr)
	if hr32>>16 == 0x8007 {
		return windows.Errno(hr32 & 0xFFFF)
	}
	return windows.Errno(hr32)
}

func prjMarkDirectoryAsPlaceholder(rootPathName *uint16, targetPathName *uint16, versionInfo *prjPlaceholderVersionInfo, virtualizationInstanceID *windows.GUID) error {
	hr, _, _ := procPrjMarkDirectoryAsPlaceholder.Call(
		uintptr(unsafe.Pointer(rootPathName)),
		uintptr(unsafe.Pointer(targetPathName)),
		uintptr(unsafe.Pointer(versionInfo)),
		uintptr(unsafe.Pointer(virtualizationInstanceID)),
	)
	return hresultToError(hr)
}

func prjStartVirtualizing(virtualizationRootPath *uint16, callbacks *prjCallbacks, instanceContext uintptr, options *prjStartVirtualizingOptions, namespaceVirtualizationContext *uintptr) error {
	hr, _, _ := procPrjStartVirtualizing.Call(
		uintptr(unsafe.Pointer(virtualizationRootPath)),
		uintptr(unsafe.Pointer(callbacks)),
		instanceContext,
		uintptr(unsafe.Pointer(options)),
		uintptr(unsafe.Pointer(namespaceVirtualizationContext)),
	)
	return hresultToError(hr)
}

func prjStopVirtualizing(namespaceVirtualizationContext uintptr) {
	_, _, _ = procPrjStopVirtualizing.Call(namespaceVirtualizationContext)
}

func prjWritePlaceholderInfo(namespaceVirtualizationContext uintptr, destinationFileName *uint16, placeholderInfo *prjPlaceholderInfo, placeholderInfoSize uint32) error {
	hr, _, _ := procPrjWritePlaceholderInfo.Call(
		namespaceVirtualizationContext,
		uintptr(unsafe.Pointer(destinationFileName)),
		uintptr(unsafe.Pointer(placeholderInfo)),
		uintptr(placeholderInfoSize),
	)
	return hresultToError(hr)
}

func prjWriteFileData(namespaceVirtualizationContext uintptr, dataStreamID *windows.GUID, buffer unsafe.Pointer, byteOffset uint64, length uint32) error {
	hr, _, _ := procPrjWriteFileData.Call(
		namespaceVirtualizationContext,
		uintptr(unsafe.Pointer(dataStreamID)),
		uintptr(buffer),
		uintptr(byteOffset),
		uintptr(length),
	)
	return hresultToError(hr)
}

func prjAllocateAlignedBuffer(namespaceVirtualizationContext uintptr, size uint64) unsafe.Pointer {
	ptr, _, _ := procPrjAllocateAlignedBuffer.Call(
		namespaceVirtualizationContext,
		uintptr(size),
	)
	return unsafe.Pointer(ptr) //nolint:govet // syscall returns uintptr that must be converted to unsafe.Pointer
}

func prjFreeAlignedBuffer(buffer unsafe.Pointer) {
	_, _, _ = procPrjFreeAlignedBuffer.Call(uintptr(buffer))
}

func prjFillDirEntryBuffer(fileName *uint16, fileBasicInfo *prjFileBasicInfo, dirEntryBufferHandle uintptr) error {
	hr, _, _ := procPrjFillDirEntryBuffer.Call(
		uintptr(unsafe.Pointer(fileName)),
		uintptr(unsafe.Pointer(fileBasicInfo)),
		dirEntryBufferHandle,
	)
	return hresultToError(hr)
}

func prjFileNameCompare(fileName1 *uint16, fileName2 *uint16) int32 {
	r, _, _ := procPrjFileNameCompare.Call(
		uintptr(unsafe.Pointer(fileName1)),
		uintptr(unsafe.Pointer(fileName2)),
	)
	return int32(r)
}

func prjFileNameMatch(fileNameToCheck *uint16, pattern *uint16) bool {
	r, _, _ := procPrjFileNameMatch.Call(
		uintptr(unsafe.Pointer(fileNameToCheck)),
		uintptr(unsafe.Pointer(pattern)),
	)
	return r != 0
}

// goTimeToFiletime converts a Go time.Time to a Windows FILETIME int64
// (100-ns intervals since 1601-01-01).
func goTimeToFiletime(t time.Time) int64 {
	if t.IsZero() {
		return 0
	}
	ft := windows.NsecToFiletime(t.UnixNano())
	return int64(ft.HighDateTime)<<32 | int64(ft.LowDateTime)
}
