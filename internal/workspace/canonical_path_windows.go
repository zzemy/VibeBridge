//go:build windows

package workspace

import (
	"fmt"
	"strings"

	"golang.org/x/sys/windows"
)

func canonicalizePath(path string) (string, error) {
	pathPointer, err := windows.UTF16PtrFromString(path)
	if err != nil {
		return "", err
	}
	handle, err := windows.CreateFile(
		pathPointer,
		windows.FILE_READ_ATTRIBUTES,
		windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE|windows.FILE_SHARE_DELETE,
		nil,
		windows.OPEN_EXISTING,
		windows.FILE_FLAG_BACKUP_SEMANTICS,
		0,
	)
	if err != nil {
		return "", err
	}
	defer windows.CloseHandle(handle)

	buffer := make([]uint16, 512)
	for {
		length, err := windows.GetFinalPathNameByHandle(handle, &buffer[0], uint32(len(buffer)), 0)
		if err != nil {
			return "", err
		}
		if length < uint32(len(buffer)) {
			return normalizeWindowsFinalPath(windows.UTF16ToString(buffer[:length])), nil
		}
		if length > windows.MAX_LONG_PATH {
			return "", fmt.Errorf("canonical path exceeds the Windows path limit")
		}
		buffer = make([]uint16, length+1)
	}
}

func normalizeWindowsFinalPath(path string) string {
	const extendedPrefix = `\\?\`
	const extendedUNCPrefix = `\\?\UNC\`
	if strings.HasPrefix(path, extendedUNCPrefix) {
		return `\\` + strings.TrimPrefix(path, extendedUNCPrefix)
	}
	return strings.TrimPrefix(path, extendedPrefix)
}
