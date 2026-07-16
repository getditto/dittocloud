package bootstrap

import (
	"fmt"
	"os"
	"path/filepath"
)

type statePersistenceError struct {
	DestinationPath string
	RecoveryPath    string
	Err             error
}

func (err *statePersistenceError) Error() string {
	if err.RecoveryPath != "" {
		return fmt.Sprintf(
			"unable to atomically persist Terraform state to %q: %v; recovery state retained at %q",
			err.DestinationPath,
			err.Err,
			err.RecoveryPath,
		)
	}
	return fmt.Sprintf("unable to atomically persist Terraform state to %q: %v", err.DestinationPath, err.Err)
}

func (err *statePersistenceError) Unwrap() error {
	return err.Err
}

var (
	replaceStateFile   = atomicReplaceFile
	syncStateDirectory = syncParentDirectory
)

func persistTerraformState(tmpStateFilePath, localStateFilePath string) error {
	stateFileData, err := os.ReadFile(tmpStateFilePath)
	if err != nil {
		return &statePersistenceError{
			DestinationPath: localStateFilePath,
			Err:             fmt.Errorf("unable to read state file from temporary directory: %w", err),
		}
	}

	canonicalStatePath, err := canonicalizeStatePath(localStateFilePath)
	if err != nil {
		return &statePersistenceError{DestinationPath: localStateFilePath, Err: err}
	}
	stateDirectory := filepath.Dir(canonicalStatePath)
	temporaryState, err := os.CreateTemp(stateDirectory, "."+filepath.Base(canonicalStatePath)+".dittocloud-*")
	if err != nil {
		return &statePersistenceError{
			DestinationPath: canonicalStatePath,
			Err:             fmt.Errorf("unable to create sibling state file: %w", err),
		}
	}
	temporaryStatePath := temporaryState.Name()
	temporaryStateOpen := true
	closeTemporaryState := func() error {
		if !temporaryStateOpen {
			return nil
		}
		temporaryStateOpen = false
		return temporaryState.Close()
	}
	failure := func(cause error) error {
		_ = closeTemporaryState()
		return &statePersistenceError{
			DestinationPath: canonicalStatePath,
			RecoveryPath:    temporaryStatePath,
			Err:             cause,
		}
	}

	if err := temporaryState.Chmod(0600); err != nil {
		return failure(fmt.Errorf("unable to secure sibling state file: %w", err))
	}
	if _, err := temporaryState.Write(stateFileData); err != nil {
		return failure(fmt.Errorf("unable to write sibling state file: %w", err))
	}
	if err := temporaryState.Sync(); err != nil {
		return failure(fmt.Errorf("unable to flush sibling state file: %w", err))
	}
	if err := closeTemporaryState(); err != nil {
		return failure(fmt.Errorf("unable to close sibling state file: %w", err))
	}
	if err := replaceStateFile(temporaryStatePath, canonicalStatePath); err != nil {
		return failure(fmt.Errorf("unable to replace destination state file: %w", err))
	}
	if err := syncStateDirectory(stateDirectory); err != nil {
		return &statePersistenceError{
			DestinationPath: canonicalStatePath,
			RecoveryPath:    canonicalStatePath,
			Err:             fmt.Errorf("state was replaced but the parent directory could not be flushed: %w", err),
		}
	}
	return nil
}
