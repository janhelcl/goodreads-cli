//go:build windows

package app

import "golang.org/x/sys/windows"

func moveFile(staging, target string, flags uint32) error {
	from, err := windows.UTF16PtrFromString(staging)
	if err != nil {
		return err
	}
	to, err := windows.UTF16PtrFromString(target)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(from, to, flags|windows.MOVEFILE_WRITE_THROUGH)
}

func installFileNoReplace(staging, target string) error {
	return moveFile(staging, target, 0)
}

func replaceFile(staging, target string) error {
	return moveFile(staging, target, windows.MOVEFILE_REPLACE_EXISTING)
}
