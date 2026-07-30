//go:build windows

package bootstrap

import (
	"errors"
	"os"

	"golang.org/x/sys/windows"
)

func tryAdvisoryLock(file *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.LockFileEx(
		windows.Handle(file.Fd()),
		windows.LOCKFILE_EXCLUSIVE_LOCK|windows.LOCKFILE_FAIL_IMMEDIATELY,
		0,
		1,
		0,
		overlapped,
	)
}

func isAdvisoryLockContention(err error) bool {
	return errors.Is(err, windows.ERROR_LOCK_VIOLATION)
}

func releaseAdvisoryLock(file *os.File) error {
	overlapped := &windows.Overlapped{}
	return windows.UnlockFileEx(windows.Handle(file.Fd()), 0, 1, 0, overlapped)
}

func atomicReplaceFile(sourcePath, destinationPath string) error {
	source, err := windows.UTF16PtrFromString(sourcePath)
	if err != nil {
		return err
	}
	destination, err := windows.UTF16PtrFromString(destinationPath)
	if err != nil {
		return err
	}
	return windows.MoveFileEx(source, destination, windows.MOVEFILE_REPLACE_EXISTING|windows.MOVEFILE_WRITE_THROUGH)
}

func syncParentDirectory(directoryPath string) error {
	// MoveFileEx with MOVEFILE_WRITE_THROUGH flushes the replacement before it
	// returns. Windows does not expose a portable directory fsync operation.
	return nil
}
