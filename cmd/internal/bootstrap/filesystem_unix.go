//go:build darwin || freebsd || linux || netbsd || openbsd

package bootstrap

import (
	"errors"
	"os"

	"golang.org/x/sys/unix"
)

func tryAdvisoryLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB)
}

func isAdvisoryLockContention(err error) bool {
	return errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN)
}

func releaseAdvisoryLock(file *os.File) error {
	return unix.Flock(int(file.Fd()), unix.LOCK_UN)
}

func atomicReplaceFile(sourcePath, destinationPath string) error {
	return os.Rename(sourcePath, destinationPath)
}

func syncParentDirectory(directoryPath string) error {
	directory, err := os.Open(directoryPath)
	if err != nil {
		return err
	}
	defer func() { _ = directory.Close() }()
	return directory.Sync()
}
