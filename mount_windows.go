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
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"sort"
	"sync"
	"unsafe"

	"golang.org/x/sys/windows"
)

// utf16Str is a pointer to a null-terminated UTF-16 string (Windows LPCWSTR).
type utf16Str = *uint16

// HRESULT constants returned from ProjFS callbacks.
const (
	hresultOK           = 0          // S_OK
	hresultFileNotFound = 0x80070002 // HRESULT_FROM_WIN32(ERROR_FILE_NOT_FOUND)
	hresultAccessDenied = 0x80070005 // HRESULT_FROM_WIN32(ERROR_ACCESS_DENIED)
	hresultFileExists   = 0x80070050 // HRESULT_FROM_WIN32(ERROR_FILE_EXISTS)
	hresultOutOfMemory  = 0x8007000E // HRESULT_FROM_WIN32(ERROR_OUTOFMEMORY)
	hresultFail         = 0x80004005 // E_FAIL
)

// projfsPath converts a ProjFS backslash-relative path to a forward-slash
// fs.ValidPath. ProjFS passes nil for the root directory.
func projfsPath(name utf16Str) string {
	if name == nil {
		return cwdPath
	}
	return coerceUnix(windows.UTF16PtrToString(name))
}

// projfsHRESULT converts a Go error to a Windows HRESULT for returning from
// ProjFS callbacks.
func projfsHRESULT(err error) uintptr {
	if err == nil {
		return hresultOK
	}
	if errors.Is(err, fs.ErrNotExist) {
		return hresultFileNotFound
	}
	if errors.Is(err, fs.ErrPermission) {
		return hresultAccessDenied
	}
	if errors.Is(err, fs.ErrExist) {
		return hresultFileExists
	}
	return hresultFail
}

// projfsEnumSession holds state for an in-progress directory enumeration.
type projfsEnumSession struct {
	path    string
	entries []fs.DirEntry
	index   int
}

// projfsMountServer implements MountServer using ProjFS.
type projfsMountServer struct {
	fsys         ReadFS
	nsCtx        uintptr // PRJ_NAMESPACE_VIRTUALIZATION_CONTEXT
	enumSessions sync.Map
	done         chan struct{}
	closeOnce    sync.Once
}

var (
	cbStartDirEnum   uintptr
	cbEndDirEnum     uintptr
	cbGetDirEnum     uintptr
	cbGetPlaceholder uintptr
	cbGetFileData    uintptr
	cbQueryFileName  uintptr
	cbNotification   uintptr
	cbCancelCommand  uintptr

	cbOnce sync.Once
)

func initCallbacks() {
	cbOnce.Do(func() {
		cbStartDirEnum = windows.NewCallback(startDirEnumCB)
		cbEndDirEnum = windows.NewCallback(endDirEnumCB)
		cbGetDirEnum = windows.NewCallback(getDirEnumCB)
		cbGetPlaceholder = windows.NewCallback(getPlaceholderCB)
		cbGetFileData = windows.NewCallback(getFileDataCB)
		cbQueryFileName = windows.NewCallback(queryFileNameCB)
		cbNotification = windows.NewCallback(notificationCB)
		cbCancelCommand = windows.NewCallback(cancelCommandCB)
	})
}

func serverFromContext(callbackData *prjCallbackData) *projfsMountServer {
	return (*projfsMountServer)(unsafe.Pointer(callbackData.InstanceContext))
}

func startDirEnumCB(callbackData *prjCallbackData, enumerationId *windows.GUID) uintptr {
	s := serverFromContext(callbackData)
	p := projfsPath(callbackData.FilePathName)
	s.enumSessions.Store(*enumerationId, &projfsEnumSession{path: p})
	return 0
}

func endDirEnumCB(callbackData *prjCallbackData, enumerationId *windows.GUID) uintptr {
	s := serverFromContext(callbackData)
	s.enumSessions.Delete(*enumerationId)
	return 0
}

func getDirEnumCB(callbackData *prjCallbackData, enumerationId *windows.GUID, searchExpression *uint16, dirEntryBufferHandle uintptr) uintptr {
	s := serverFromContext(callbackData)
	v, ok := s.enumSessions.Load(*enumerationId)
	if !ok {
		return hresultFail
	}
	session := v.(*projfsEnumSession)

	isRestart := callbackData.Flags&prjCBDataFlagRestartScan != 0
	if session.entries == nil || isRestart {
		entries, err := s.fsys.ReadDir(session.path)
		if err != nil {
			return projfsHRESULT(err)
		}
		sort.Slice(entries, func(i, j int) bool {
			a, _ := windows.UTF16PtrFromString(entries[i].Name())
			b, _ := windows.UTF16PtrFromString(entries[j].Name())
			return prjFileNameCompare(a, b) < 0
		})
		session.entries = entries
		session.index = 0
	}

	for session.index < len(session.entries) {
		e := session.entries[session.index]
		info, err := e.Info()
		if err != nil {
			session.index++
			continue
		}

		namePtr, err := windows.UTF16PtrFromString(e.Name())
		if err != nil {
			session.index++
			continue
		}

		if searchExpression != nil && !prjFileNameMatch(namePtr, searchExpression) {
			session.index++
			continue
		}

		fbi := prjFileBasicInfo{
			FileSize:       info.Size(),
			CreationTime:   goTimeToFiletime(info.ModTime()),
			LastAccessTime: goTimeToFiletime(info.ModTime()),
			LastWriteTime:  goTimeToFiletime(info.ModTime()),
			ChangeTime:     goTimeToFiletime(info.ModTime()),
		}
		if info.IsDir() {
			fbi.IsDirectory = 1
			fbi.FileAttributes = 0x10 // FILE_ATTRIBUTE_DIRECTORY
		} else {
			fbi.FileAttributes = 0x80 // FILE_ATTRIBUTE_NORMAL
		}

		if err := prjFillDirEntryBuffer(namePtr, &fbi, dirEntryBufferHandle); err != nil {
			break // buffer full — will be called again
		}
		session.index++
	}
	return 0
}

func getPlaceholderCB(callbackData *prjCallbackData) uintptr {
	s := serverFromContext(callbackData)
	p := projfsPath(callbackData.FilePathName)

	fi, err := s.fsys.Stat(p)
	if err != nil {
		return projfsHRESULT(err)
	}

	info := prjPlaceholderInfo{}
	info.FileBasicInfo.FileSize = fi.Size()
	info.FileBasicInfo.CreationTime = goTimeToFiletime(fi.ModTime())
	info.FileBasicInfo.LastAccessTime = goTimeToFiletime(fi.ModTime())
	info.FileBasicInfo.LastWriteTime = goTimeToFiletime(fi.ModTime())
	info.FileBasicInfo.ChangeTime = goTimeToFiletime(fi.ModTime())
	if fi.IsDir() {
		info.FileBasicInfo.IsDirectory = 1
		info.FileBasicInfo.FileAttributes = 0x10 // FILE_ATTRIBUTE_DIRECTORY
	} else {
		info.FileBasicInfo.FileAttributes = 0x80 // FILE_ATTRIBUTE_NORMAL
	}

	if err := prjWritePlaceholderInfo(callbackData.NamespaceVirtualizationContext, callbackData.FilePathName, &info, uint32(unsafe.Sizeof(info))); err != nil {
		return projfsHRESULT(err)
	}
	return 0
}

func getFileDataCB(callbackData *prjCallbackData, byteOffset uint64, length uint32) uintptr {
	s := serverFromContext(callbackData)
	p := projfsPath(callbackData.FilePathName)

	f, err := s.fsys.Open(p)
	if err != nil {
		return projfsHRESULT(err)
	}
	defer f.Close()

	buf := prjAllocateAlignedBuffer(callbackData.NamespaceVirtualizationContext, uint64(length))
	if buf == nil {
		return hresultOutOfMemory
	}
	defer prjFreeAlignedBuffer(buf)

	dest := unsafe.Slice((*byte)(buf), length)

	var n int
	if ra, ok := f.(io.ReaderAt); ok {
		n, err = ra.ReadAt(dest, int64(byteOffset))
	} else if seeker, ok := f.(io.Seeker); ok {
		if _, err = seeker.Seek(int64(byteOffset), io.SeekStart); err != nil {
			return projfsHRESULT(err)
		}
		n, err = io.ReadFull(f, dest)
	} else if byteOffset == 0 {
		n, err = io.ReadFull(f, dest)
	} else {
		return hresultFail
	}
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return projfsHRESULT(err)
	}

	if err := prjWriteFileData(callbackData.NamespaceVirtualizationContext, &callbackData.DataStreamId, buf, byteOffset, uint32(n)); err != nil {
		return projfsHRESULT(err)
	}
	return 0
}

func queryFileNameCB(callbackData *prjCallbackData) uintptr {
	s := serverFromContext(callbackData)
	p := projfsPath(callbackData.FilePathName)
	if _, err := s.fsys.Stat(p); err != nil {
		return projfsHRESULT(err)
	}
	return 0
}

func notificationCB(callbackData *prjCallbackData, isDirectory uint8, notification uint32, destinationFileName utf16Str, operationParameters uintptr) uintptr {
	switch notification {
	case prjNotificationPreDelete, prjNotificationPreRename:
		return hresultAccessDenied
	}
	return 0
}

func cancelCommandCB(callbackData *prjCallbackData) uintptr {
	return 0
}

func hostMount(ctx context.Context, fsys ReadFS, mountPath string) (MountServer, error) {
	if err := projfsAvailable(); err != nil {
		return nil, err
	}

	initCallbacks()

	rootPathPtr, err := windows.UTF16PtrFromString(mountPath)
	if err != nil {
		return nil, err
	}

	instanceID, err := windows.GenerateGUID()
	if err != nil {
		return nil, err
	}

	if err := prjMarkDirectoryAsPlaceholder(rootPathPtr, nil, nil, &instanceID); err != nil {
		return nil, fmt.Errorf("cannot mark %q as virtualization root: %w", mountPath, err)
	}

	s := &projfsMountServer{
		fsys: fsys,
		done: make(chan struct{}),
	}

	cbs := prjCallbacks{
		StartDirectoryEnumerationCallback: cbStartDirEnum,
		EndDirectoryEnumerationCallback:   cbEndDirEnum,
		GetDirectoryEnumerationCallback:   cbGetDirEnum,
		GetPlaceholderInfoCallback:        cbGetPlaceholder,
		GetFileDataCallback:               cbGetFileData,
		QueryFileNameCallback:             cbQueryFileName,
		NotificationCallback:              cbNotification,
		CancelCommandCallback:             cbCancelCommand,
	}

	notifRoot, _ := windows.UTF16PtrFromString("")
	notifMapping := prjNotificationMapping{
		NotificationBitMask: prjNotifyPreDelete | prjNotifyPreRename,
		NotificationRoot:    notifRoot,
	}

	opts := prjStartVirtualizingOptions{
		NotificationMappings:      &notifMapping,
		NotificationMappingsCount: 1,
	}

	if err := prjStartVirtualizing(rootPathPtr, &cbs, uintptr(unsafe.Pointer(s)), &opts, &s.nsCtx); err != nil {
		return nil, fmt.Errorf("cannot start ProjFS virtualization at %q: %w", mountPath, err)
	}

	go func() {
		<-ctx.Done()
		s.Close()
	}()

	return s, nil
}

func (s *projfsMountServer) Close() error {
	s.closeOnce.Do(func() {
		prjStopVirtualizing(s.nsCtx)
		close(s.done)
	})
	return nil
}

func (s *projfsMountServer) Wait() {
	<-s.done
}
