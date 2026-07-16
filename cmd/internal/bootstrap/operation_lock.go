package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/spf13/cobra"
)

const operationLockSuffix = ".dittocloud.lock"

type operationLockContextKey struct{}
type skipTerraformLifecycleContextKey struct{}

type operationLockMetadata struct {
	PID                int       `json:"pid"`
	Hostname           string    `json:"hostname"`
	StartedAt          time.Time `json:"startedAt"`
	CanonicalStatePath string    `json:"canonicalStatePath"`
	Operation          string    `json:"operation"`
}

type stateOperationLock struct {
	canonicalStatePath string
	lockPath           string
	metadata           operationLockMetadata
	file               *os.File

	mu       sync.Mutex
	released bool
}

func canonicalizeStatePath(statePath string) (string, error) {
	return canonicalizeLocalPath(statePath, "state")
}

func canonicalizeLocalPath(selectedPath, pathKind string) (string, error) {
	if strings.TrimSpace(selectedPath) == "" {
		return "", fmt.Errorf("%s path cannot be empty", pathKind)
	}

	absolutePath, err := filepath.Abs(selectedPath)
	if err != nil {
		return "", fmt.Errorf("unable to make %s path absolute: %w", pathKind, err)
	}
	absolutePath = filepath.Clean(absolutePath)

	canonicalPath, err := filepath.EvalSymlinks(absolutePath)
	if err == nil {
		return canonicalPath, nil
	}
	if !errors.Is(err, os.ErrNotExist) {
		return "", fmt.Errorf("unable to resolve %s path %q: %w", pathKind, absolutePath, err)
	}
	statePathInfo, lstatErr := os.Lstat(absolutePath)
	if lstatErr == nil && statePathInfo.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("unable to resolve dangling %s symlink %q", pathKind, absolutePath)
	}
	if lstatErr != nil && !errors.Is(lstatErr, os.ErrNotExist) {
		return "", fmt.Errorf("unable to inspect unresolved %s path %q: %w", pathKind, absolutePath, lstatErr)
	}

	canonicalDirectory, err := filepath.EvalSymlinks(filepath.Dir(absolutePath))
	if err != nil {
		return "", fmt.Errorf("unable to resolve %s directory for %q: %w", pathKind, absolutePath, err)
	}
	return filepath.Join(canonicalDirectory, filepath.Base(absolutePath)), nil
}

func acquireStateOperationLock(statePath, operation string) (*stateOperationLock, error) {
	canonicalStatePath, err := canonicalizeStatePath(statePath)
	if err != nil {
		return nil, err
	}

	lockPath := canonicalStatePath + operationLockSuffix
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, fmt.Errorf("unable to open Dittocloud operation lock %q: %w", lockPath, err)
	}
	closeOnError := true
	defer func() {
		if closeOnError {
			_ = lockFile.Close()
		}
	}()

	if err := lockFile.Chmod(0600); err != nil {
		return nil, fmt.Errorf("unable to secure Dittocloud operation lock %q: %w", lockPath, err)
	}
	if err := tryAdvisoryLock(lockFile); err != nil {
		if isAdvisoryLockContention(err) {
			return nil, operationLockContentionError(canonicalStatePath, lockPath)
		}
		return nil, fmt.Errorf("unable to acquire Dittocloud operation lock %q: %w", lockPath, err)
	}

	hostname, err := os.Hostname()
	if err != nil {
		hostname = "unknown"
	}
	metadata := operationLockMetadata{
		PID:                os.Getpid(),
		Hostname:           hostname,
		StartedAt:          time.Now().UTC(),
		CanonicalStatePath: canonicalStatePath,
		Operation:          operation,
	}
	if err := writeOperationLockMetadata(lockFile, metadata); err != nil {
		_ = releaseAdvisoryLock(lockFile)
		return nil, fmt.Errorf("unable to write Dittocloud operation lock metadata to %q: %w", lockPath, err)
	}

	closeOnError = false
	return &stateOperationLock{
		canonicalStatePath: canonicalStatePath,
		lockPath:           lockPath,
		metadata:           metadata,
		file:               lockFile,
	}, nil
}

func writeOperationLockMetadata(lockFile *os.File, metadata operationLockMetadata) error {
	encoded, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	encoded = append(encoded, '\n')

	if err := lockFile.Truncate(0); err != nil {
		return err
	}
	if _, err := lockFile.Seek(0, 0); err != nil {
		return err
	}
	if _, err := lockFile.Write(encoded); err != nil {
		return err
	}
	return lockFile.Sync()
}

func operationLockContentionError(canonicalStatePath, lockPath string) error {
	metadataContent, err := os.ReadFile(lockPath)
	if err == nil {
		var metadata operationLockMetadata
		if json.Unmarshal(metadataContent, &metadata) == nil && metadata.PID != 0 {
			return fmt.Errorf(
				"Dittocloud operation already in progress for state %q: pid %d on %s started %s (%s)",
				canonicalStatePath,
				metadata.PID,
				metadata.Hostname,
				metadata.StartedAt.Format(time.RFC3339),
				metadata.Operation,
			)
		}
	}

	return fmt.Errorf("Dittocloud operation already in progress for state %q; lock owner metadata is unavailable", canonicalStatePath)
}

func (lock *stateOperationLock) Release() error {
	if lock == nil {
		return nil
	}

	lock.mu.Lock()
	defer lock.mu.Unlock()
	if lock.released {
		return nil
	}
	lock.released = true

	unlockErr := releaseAdvisoryLock(lock.file)
	closeErr := lock.file.Close()
	if unlockErr != nil || closeErr != nil {
		return errors.Join(unlockErr, closeErr)
	}
	return nil
}

func setCommandOperationLock(cmd *cobra.Command, lock *stateOperationLock) {
	cmd.SetContext(context.WithValue(cmd.Context(), operationLockContextKey{}, lock))
}

func commandOperationLock(cmd *cobra.Command) *stateOperationLock {
	lock, _ := cmd.Context().Value(operationLockContextKey{}).(*stateOperationLock)
	return lock
}

func commandCanonicalStatePath(cmd *cobra.Command) string {
	if lock := commandOperationLock(cmd); lock != nil {
		return lock.canonicalStatePath
	}
	return cmd.Flag("state").Value.String()
}

func skipCommandTerraformLifecycle(cmd *cobra.Command) {
	cmd.SetContext(context.WithValue(cmd.Context(), skipTerraformLifecycleContextKey{}, true))
}

func commandSkipsTerraformLifecycle(cmd *cobra.Command) bool {
	skip, _ := cmd.Context().Value(skipTerraformLifecycleContextKey{}).(bool)
	return skip
}

func releaseCommandOperationLock(cmd *cobra.Command) error {
	lock := commandOperationLock(cmd)
	if lock == nil {
		return nil
	}
	return lock.Release()
}

func releaseCommandOperationLockOnError(cmd *cobra.Command, runErr *error) {
	if runErr == nil || *runErr == nil {
		return
	}
	if err := releaseCommandOperationLock(cmd); err != nil {
		*runErr = errors.Join(*runErr, fmt.Errorf("unable to release Dittocloud operation lock: %w", err))
	}
}
