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
	"log/slog"
	"os"
	"path"
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

// Windows FILE_ATTRIBUTE_* bits used when reporting file/directory attributes.
const (
	fileAttributeReadOnly  = 0x1
	fileAttributeDirectory = 0x10
	fileAttributeNormal    = 0x80
)

// fileAttributesFor computes the FILE_ATTRIBUTES value ProjFS should report
// for info, adding FILE_ATTRIBUTE_READONLY when readOnly is true so that
// directory listings and stat responses reflect a mount (or sub-mount, for
// composite FS types) whose writes are guaranteed to fail.
func fileAttributesFor(info fs.FileInfo, readOnly bool) uint32 {
	attrs := uint32(fileAttributeNormal)
	if info.IsDir() {
		attrs = fileAttributeDirectory
	}
	if readOnly {
		attrs |= fileAttributeReadOnly
	}
	return attrs
}

func hresultName(hr uintptr) string {
	switch hr {
	case hresultOK:
		return "S_OK"
	case hresultFileNotFound:
		return "FILE_NOT_FOUND"
	case hresultAccessDenied:
		return "ACCESS_DENIED"
	case hresultFileExists:
		return "FILE_EXISTS"
	case hresultOutOfMemory:
		return "OUT_OF_MEMORY"
	case hresultFail:
		return "E_FAIL"
	default:
		return fmt.Sprintf("0x%08X", hr)
	}
}

func notificationName(n uint32) string {
	switch n {
	case prjNotificationFileOpened:
		return "FILE_OPENED"
	case prjNotificationNewFileCreated:
		return "NEW_FILE_CREATED"
	case prjNotificationFileOverwritten:
		return "FILE_OVERWRITTEN"
	case prjNotificationPreDelete:
		return "PRE_DELETE"
	case prjNotificationPreRename:
		return "PRE_RENAME"
	case prjNotificationPreSetHardlink:
		return "PRE_SET_HARDLINK"
	case prjNotificationFileRenamed:
		return "FILE_RENAMED"
	case prjNotificationHardlinkCreated:
		return "HARDLINK_CREATED"
	case prjNotificationFileHandleClosedNoModification:
		return "HANDLE_CLOSED_NO_MOD"
	case prjNotificationFileHandleClosedFileModified:
		return "HANDLE_CLOSED_MODIFIED"
	case prjNotificationFileHandleClosedFileDeleted:
		return "HANDLE_CLOSED_DELETED"
	case prjNotificationFilePreConvertToFull:
		return "PRE_CONVERT_TO_FULL"
	default:
		return fmt.Sprintf("UNKNOWN(0x%08X)", n)
	}
}

// projfsPath converts a ProjFS backslash-relative path to a forward-slash
// fs.ValidPath. ProjFS passes nil for the root directory.
func projfsPath(name utf16Str) string {
	if name == nil {
		return cwdPath
	}
	return coerceUnix(windows.UTF16PtrToString(name))
}

func cbDataAttrs(callbackData *prjCallbackData) []any {
	p := projfsPath(callbackData.FilePathName)
	var rawPath string
	if callbackData.FilePathName != nil {
		rawPath = windows.UTF16PtrToString(callbackData.FilePathName)
	} else {
		rawPath = "<nil>"
	}
	attrs := []any{
		"path", p,
		"rawPath", rawPath,
		"cbDataSize", callbackData.Size,
		"commandId", callbackData.CommandID,
		"flags", fmt.Sprintf("0x%X", callbackData.Flags),
		"triggeringPID", callbackData.TriggeringProcessID,
	}
	if callbackData.TriggeringProcessImageFileName != nil {
		attrs = append(attrs, "triggeringProcess", windows.UTF16PtrToString(callbackData.TriggeringProcessImageFileName))
	}
	return attrs
}

// projfsHRESULT converts a Go error to a Windows HRESULT for returning from
// ProjFS callbacks.
func projfsHRESULT(err error) uintptr {
	if err == nil {
		return hresultOK
	}
	var hr uintptr
	switch {
	case errors.Is(err, fs.ErrNotExist):
		hr = hresultFileNotFound
	case errors.Is(err, fs.ErrPermission):
		hr = hresultAccessDenied
	case errors.Is(err, fs.ErrExist):
		hr = hresultFileExists
	default:
		hr = hresultFail
	}
	slog.Debug("projfs: error→HRESULT mapping", "error", err, "errorType", fmt.Sprintf("%T", err), "hresult", hresultName(hr))
	return hr
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
	return (*projfsMountServer)(unsafe.Pointer(callbackData.InstanceContext)) //nolint:govet // ProjFS callback context is a pointer stored as uintptr
}

func startDirEnumCB(callbackData *prjCallbackData, enumerationID *windows.GUID) uintptr {
	s := serverFromContext(callbackData)
	p := projfsPath(callbackData.FilePathName)
	slog.Debug("projfs: StartDirEnum", append(cbDataAttrs(callbackData), "enumId", enumerationID)...)
	s.enumSessions.Store(*enumerationID, &projfsEnumSession{path: p})
	slog.Debug("projfs: StartDirEnum complete", "path", p, "result", "S_OK")
	return 0
}

func endDirEnumCB(callbackData *prjCallbackData, enumerationID *windows.GUID) uintptr {
	s := serverFromContext(callbackData)
	slog.Debug("projfs: EndDirEnum", append(cbDataAttrs(callbackData), "enumId", enumerationID)...)
	s.enumSessions.Delete(*enumerationID)
	return 0
}

func getDirEnumCB(callbackData *prjCallbackData, enumerationID *windows.GUID, searchExpression *uint16, dirEntryBufferHandle uintptr) uintptr {
	s := serverFromContext(callbackData)
	v, ok := s.enumSessions.Load(*enumerationID)
	if !ok {
		slog.Error("projfs: GetDirEnum session not found", append(cbDataAttrs(callbackData), "enumId", enumerationID)...)
		return hresultFail
	}
	session := v.(*projfsEnumSession)

	searchStr := ""
	if searchExpression != nil {
		searchStr = windows.UTF16PtrToString(searchExpression)
	}
	slog.Debug("projfs: GetDirEnum", append(cbDataAttrs(callbackData),
		"enumId", enumerationID,
		"search", searchStr,
		"sessionPath", session.path,
		"sessionIndex", session.index,
		"hasEntries", session.entries != nil,
	)...)

	isRestart := callbackData.Flags&prjCBDataFlagRestartScan != 0
	returnSingleEntry := callbackData.Flags&prjCBDataFlagReturnSingleEntry != 0
	if session.entries == nil || isRestart {
		slog.Debug("projfs: GetDirEnum reading directory", "path", session.path, "isRestart", isRestart, "returnSingleEntry", returnSingleEntry)
		entries, err := s.fsys.ReadDir(session.path)
		if err != nil {
			hr := projfsHRESULT(err)
			slog.Error("projfs: GetDirEnum ReadDir failed", "path", session.path, "error", err, "hresult", hresultName(hr))
			return hr
		}
		slog.Debug("projfs: GetDirEnum ReadDir returned", "path", session.path, "count", len(entries))
		sort.Slice(entries, func(i, j int) bool {
			a, _ := windows.UTF16PtrFromString(entries[i].Name())
			b, _ := windows.UTF16PtrFromString(entries[j].Name())
			return prjFileNameCompare(a, b) < 0
		})
		session.entries = entries
		session.index = 0
	}

	filled := 0
	skippedInfo := 0
	skippedName := 0
	skippedFilter := 0
	for session.index < len(session.entries) {
		e := session.entries[session.index]
		info, err := e.Info()
		if err != nil {
			slog.Warn("projfs: GetDirEnum entry Info() failed", "name", e.Name(), "error", err)
			session.index++
			skippedInfo++
			continue
		}

		namePtr, err := windows.UTF16PtrFromString(e.Name())
		if err != nil {
			slog.Warn("projfs: GetDirEnum UTF16 conversion failed", "name", e.Name(), "error", err)
			session.index++
			skippedName++
			continue
		}

		if searchExpression != nil && !prjFileNameMatch(namePtr, searchExpression) {
			session.index++
			skippedFilter++
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
		}
		fbi.FileAttributes = fileAttributesFor(info, isReadOnlyAt(s.fsys, path.Join(session.path, e.Name())))

		slog.Debug("projfs: GetDirEnum filling entry",
			"name", e.Name(),
			"isDir", info.IsDir(),
			"size", info.Size(),
			"mode", info.Mode(),
			"modTime", info.ModTime(),
			"fileAttributes", fmt.Sprintf("0x%X", fbi.FileAttributes),
		)

		if err := prjFillDirEntryBuffer(namePtr, &fbi, dirEntryBufferHandle); err != nil {
			slog.Debug("projfs: GetDirEnum buffer full", "filledSoFar", filled, "remaining", len(session.entries)-session.index)
			break // buffer full — will be called again
		}
		filled++
		session.index++
	}
	slog.Debug("projfs: GetDirEnum complete",
		"path", session.path,
		"filled", filled,
		"skippedInfo", skippedInfo,
		"skippedName", skippedName,
		"skippedFilter", skippedFilter,
		"sessionIndex", session.index,
		"totalEntries", len(session.entries),
	)
	return 0
}

func getPlaceholderCB(callbackData *prjCallbackData) uintptr {
	s := serverFromContext(callbackData)
	p := projfsPath(callbackData.FilePathName)

	slog.Debug("projfs: GetPlaceholder", cbDataAttrs(callbackData)...)

	fi, err := s.fsys.Stat(p)
	if err != nil {
		hr := projfsHRESULT(err)
		slog.Error("projfs: GetPlaceholder Stat failed", "path", p, "error", err, "hresult", hresultName(hr))
		return hr
	}

	slog.Debug("projfs: GetPlaceholder stat result",
		"path", p,
		"isDir", fi.IsDir(),
		"size", fi.Size(),
		"mode", fi.Mode(),
		"modTime", fi.ModTime(),
	)

	info := prjPlaceholderInfo{}
	info.FileBasicInfo.FileSize = fi.Size()
	info.FileBasicInfo.CreationTime = goTimeToFiletime(fi.ModTime())
	info.FileBasicInfo.LastAccessTime = goTimeToFiletime(fi.ModTime())
	info.FileBasicInfo.LastWriteTime = goTimeToFiletime(fi.ModTime())
	info.FileBasicInfo.ChangeTime = goTimeToFiletime(fi.ModTime())
	if fi.IsDir() {
		info.FileBasicInfo.IsDirectory = 1
	}
	info.FileBasicInfo.FileAttributes = fileAttributesFor(fi, isReadOnlyAt(s.fsys, p))

	slog.Debug("projfs: GetPlaceholder writing placeholder",
		"path", p,
		"fileAttributes", fmt.Sprintf("0x%X", info.FileBasicInfo.FileAttributes),
		"isDirectory", info.FileBasicInfo.IsDirectory,
		"fileSize", info.FileBasicInfo.FileSize,
		"placeholderInfoSize", unsafe.Sizeof(info),
	)

	if err := prjWritePlaceholderInfo(callbackData.NamespaceVirtualizationContext, callbackData.FilePathName, &info, uint32(unsafe.Sizeof(info))); err != nil {
		hr := projfsHRESULT(err)
		slog.Error("projfs: GetPlaceholder WritePlaceholderInfo failed", "path", p, "error", err, "hresult", hresultName(hr))
		return hr
	}
	slog.Debug("projfs: GetPlaceholder complete", "path", p, "result", "S_OK")
	return 0
}

func getFileDataCB(callbackData *prjCallbackData, byteOffset uint64, length uint32) uintptr {
	s := serverFromContext(callbackData)
	p := projfsPath(callbackData.FilePathName)

	slog.Debug("projfs: GetFileData", append(cbDataAttrs(callbackData),
		"byteOffset", byteOffset,
		"length", length,
	)...)

	f, err := s.fsys.Open(p)
	if err != nil {
		hr := projfsHRESULT(err)
		slog.Error("projfs: GetFileData Open failed", "path", p, "error", err, "hresult", hresultName(hr))
		return hr
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Warn("projfs: GetFileData close failed", "path", p, "error", err)
		}
	}()

	slog.Debug("projfs: GetFileData file opened",
		"path", p,
		"fileType", fmt.Sprintf("%T", f),
		"implementsReaderAt", func() bool { _, ok := f.(io.ReaderAt); return ok }(),
		"implementsSeeker", func() bool { _, ok := f.(io.Seeker); return ok }(),
	)

	buf := prjAllocateAlignedBuffer(callbackData.NamespaceVirtualizationContext, uint64(length))
	if buf == nil {
		slog.Error("projfs: GetFileData AllocateAlignedBuffer returned nil", "path", p, "length", length)
		return hresultOutOfMemory
	}
	defer prjFreeAlignedBuffer(buf)

	dest := unsafe.Slice((*byte)(buf), length)

	var n int
	var readMethod string
	if ra, ok := f.(io.ReaderAt); ok {
		readMethod = "ReaderAt"
		n, err = ra.ReadAt(dest, int64(byteOffset))
	} else if seeker, ok := f.(io.Seeker); ok {
		readMethod = "Seeker"
		if _, err = seeker.Seek(int64(byteOffset), io.SeekStart); err != nil {
			slog.Error("projfs: GetFileData Seek failed", "path", p, "offset", byteOffset, "error", err)
			return projfsHRESULT(err)
		}
		n, err = io.ReadFull(f, dest)
	} else if byteOffset == 0 {
		readMethod = "ReadFull"
		n, err = io.ReadFull(f, dest)
	} else {
		slog.Error("projfs: GetFileData no read method for non-zero offset", "path", p, "offset", byteOffset)
		return hresultFail
	}

	slog.Debug("projfs: GetFileData read result",
		"path", p,
		"method", readMethod,
		"bytesRead", n,
		"requestedLength", length,
		"offset", byteOffset,
		"error", err,
	)

	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		hr := projfsHRESULT(err)
		slog.Error("projfs: GetFileData read failed", "path", p, "error", err, "hresult", hresultName(hr))
		return hr
	}

	if err := prjWriteFileData(callbackData.NamespaceVirtualizationContext, &callbackData.DataStreamID, buf, byteOffset, uint32(n)); err != nil {
		hr := projfsHRESULT(err)
		slog.Error("projfs: GetFileData WriteFileData failed", "path", p, "error", err, "bytesWritten", n, "hresult", hresultName(hr))
		return hr
	}
	slog.Debug("projfs: GetFileData complete", "path", p, "bytesWritten", n, "result", "S_OK")
	return 0
}

func queryFileNameCB(callbackData *prjCallbackData) uintptr {
	s := serverFromContext(callbackData)
	p := projfsPath(callbackData.FilePathName)
	slog.Debug("projfs: QueryFileName", cbDataAttrs(callbackData)...)
	if _, err := s.fsys.Stat(p); err != nil {
		hr := projfsHRESULT(err)
		slog.Debug("projfs: QueryFileName not found", "path", p, "error", err, "hresult", hresultName(hr))
		return hr
	}
	slog.Debug("projfs: QueryFileName found", "path", p)
	return 0
}

func notificationCB(callbackData *prjCallbackData, isDirectory uint8, notification uint32, destinationFileName utf16Str, _ uintptr) uintptr {
	destName := ""
	if destinationFileName != nil {
		destName = windows.UTF16PtrToString(destinationFileName)
	}
	slog.Debug("projfs: Notification", append(cbDataAttrs(callbackData),
		"notification", notificationName(notification),
		"notificationRaw", fmt.Sprintf("0x%08X", notification),
		"isDirectory", isDirectory != 0,
		"destinationFileName", destName,
	)...)

	switch notification {
	case prjNotificationPreDelete, prjNotificationPreRename:
		slog.Debug("projfs: Notification denying operation",
			"path", projfsPath(callbackData.FilePathName),
			"notification", notificationName(notification),
			"result", "ACCESS_DENIED",
		)
		return hresultAccessDenied
	}
	slog.Debug("projfs: Notification allowing operation",
		"path", projfsPath(callbackData.FilePathName),
		"notification", notificationName(notification),
		"result", "S_OK",
	)
	return 0
}

func cancelCommandCB(callbackData *prjCallbackData) uintptr {
	slog.Debug("projfs: CancelCommand", cbDataAttrs(callbackData)...)
	return 0
}

func hostMount(ctx context.Context, fsys ReadFS, mountPath string) (MountServer, error) {
	slog.Info("projfs: hostMount starting",
		"mountPath", mountPath,
		"fsysType", fmt.Sprintf("%T", fsys),
	)

	// Log mount path state before we touch it — critical for diagnosing "directory inaccessible".
	if fi, err := os.Stat(mountPath); err != nil {
		slog.Warn("projfs: mount path Stat failed", "mountPath", mountPath, "error", err)
	} else {
		slog.Info("projfs: mount path exists",
			"mountPath", mountPath,
			"isDir", fi.IsDir(),
			"mode", fi.Mode(),
			"modTime", fi.ModTime(),
		)
	}
	if entries, err := os.ReadDir(mountPath); err != nil {
		slog.Warn("projfs: mount path ReadDir failed", "mountPath", mountPath, "error", err)
	} else {
		names := make([]string, len(entries))
		for i, e := range entries {
			names[i] = e.Name()
		}
		slog.Info("projfs: mount path contents", "mountPath", mountPath, "count", len(entries), "entries", names)
	}

	// Log struct sizes — a mismatch vs Windows SDK indicates alignment bugs.
	slog.Debug("projfs: struct sizes",
		"prjCallbackData", unsafe.Sizeof(prjCallbackData{}),
		"prjCallbacks", unsafe.Sizeof(prjCallbacks{}),
		"prjPlaceholderInfo", unsafe.Sizeof(prjPlaceholderInfo{}),
		"prjFileBasicInfo", unsafe.Sizeof(prjFileBasicInfo{}),
		"prjNotificationMapping", unsafe.Sizeof(prjNotificationMapping{}),
		"prjStartVirtualizingOptions", unsafe.Sizeof(prjStartVirtualizingOptions{}),
		"prjPlaceholderVersionInfo", unsafe.Sizeof(prjPlaceholderVersionInfo{}),
	)

	if err := projfsAvailable(); err != nil {
		slog.Error("projfs: ProjFS not available", "error", err)
		return nil, err
	}
	slog.Debug("projfs: ProjFS DLL loaded successfully")

	// Log backing FS root listing to verify the FS is functional.
	if rootEntries, err := fsys.ReadDir("."); err != nil {
		slog.Warn("projfs: backing FS ReadDir(\".\") failed", "error", err)
	} else {
		rootNames := make([]string, len(rootEntries))
		for i, e := range rootEntries {
			rootNames[i] = e.Name()
		}
		slog.Info("projfs: backing FS root listing", "count", len(rootEntries), "entries", rootNames)
	}

	initCallbacks()
	slog.Debug("projfs: callbacks initialized",
		"cbStartDirEnum", fmt.Sprintf("0x%X", cbStartDirEnum),
		"cbEndDirEnum", fmt.Sprintf("0x%X", cbEndDirEnum),
		"cbGetDirEnum", fmt.Sprintf("0x%X", cbGetDirEnum),
		"cbGetPlaceholder", fmt.Sprintf("0x%X", cbGetPlaceholder),
		"cbGetFileData", fmt.Sprintf("0x%X", cbGetFileData),
		"cbQueryFileName", fmt.Sprintf("0x%X", cbQueryFileName),
		"cbNotification", fmt.Sprintf("0x%X", cbNotification),
		"cbCancelCommand", fmt.Sprintf("0x%X", cbCancelCommand),
	)

	rootPathPtr, err := windows.UTF16PtrFromString(mountPath)
	if err != nil {
		slog.Error("projfs: UTF16 conversion of mount path failed", "mountPath", mountPath, "error", err)
		return nil, err
	}

	instanceID, err := windows.GenerateGUID()
	if err != nil {
		slog.Error("projfs: GenerateGUID failed", "error", err)
		return nil, err
	}
	slog.Debug("projfs: generated instance GUID", "instanceID", instanceID)

	slog.Debug("projfs: marking directory as placeholder", "mountPath", mountPath)
	if err := prjMarkDirectoryAsPlaceholder(rootPathPtr, nil, nil, &instanceID); err != nil {
		slog.Error("projfs: MarkDirectoryAsPlaceholder failed", "mountPath", mountPath, "error", err, "errorType", fmt.Sprintf("%T", err))
		return nil, fmt.Errorf("cannot mark %q as virtualization root: %w", mountPath, err)
	}
	slog.Debug("projfs: directory marked as placeholder", "mountPath", mountPath)

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

	slog.Debug("projfs: starting virtualization",
		"mountPath", mountPath,
		"notificationBitMask", fmt.Sprintf("0x%X", notifMapping.NotificationBitMask),
		"optsFlags", opts.Flags,
		"optsPoolThreadCount", opts.PoolThreadCount,
		"optsConcurrentThreadCount", opts.ConcurrentThreadCount,
		"optsMappingsCount", opts.NotificationMappingsCount,
		"instanceContext", fmt.Sprintf("0x%X", uintptr(unsafe.Pointer(s))),
	)
	if err := prjStartVirtualizing(rootPathPtr, &cbs, uintptr(unsafe.Pointer(s)), &opts, &s.nsCtx); err != nil {
		slog.Error("projfs: StartVirtualizing failed", "mountPath", mountPath, "error", err, "errorType", fmt.Sprintf("%T", err))
		return nil, fmt.Errorf("cannot start ProjFS virtualization at %q: %w", mountPath, err)
	}
	slog.Info("projfs: virtualization started successfully",
		"mountPath", mountPath,
		"nsCtx", fmt.Sprintf("0x%X", s.nsCtx),
	)

	go func() {
		<-ctx.Done()
		slog.Info("projfs: context canceled, closing mount", "mountPath", mountPath)
		if err := s.Close(); err != nil {
			slog.Warn("projfs: close on context cancel failed", "mountPath", mountPath, "error", err)
		}
	}()

	return s, nil
}

func (s *projfsMountServer) Close() error {
	s.closeOnce.Do(func() {
		slog.Info("projfs: stopping virtualization", "nsCtx", fmt.Sprintf("0x%X", s.nsCtx))
		prjStopVirtualizing(s.nsCtx)
		close(s.done)
		slog.Info("projfs: virtualization stopped")
	})
	return nil
}

func (s *projfsMountServer) Wait() {
	<-s.done
}
