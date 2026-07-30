//go:build !darwin && !freebsd && !linux && !netbsd && !openbsd && !windows

package bootstrap

import (
	"fmt"
	"os"
	"runtime"
)

func tryAdvisoryLock(file *os.File) error {
	return fmt.Errorf("Dittocloud operation locking is not supported on %s", runtime.GOOS)
}

func isAdvisoryLockContention(err error) bool {
	return false
}

func releaseAdvisoryLock(file *os.File) error {
	return nil
}

func atomicReplaceFile(sourcePath, destinationPath string) error {
	return fmt.Errorf("atomic state replacement is not supported on %s", runtime.GOOS)
}

func syncParentDirectory(directoryPath string) error {
	return nil
}
