package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestStateOperationLock(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "terraform.tfstate")
	lock, err := acquireStateOperationLock(statePath, "bootstrap aws dry-run")
	if err != nil {
		t.Fatalf("unable to acquire operation lock: %v", err)
	}
	t.Cleanup(func() { _ = lock.Release() })

	canonicalStatePath, err := canonicalizeStatePath(statePath)
	if err != nil {
		t.Fatalf("unable to canonicalize state path: %v", err)
	}
	if lock.canonicalStatePath != canonicalStatePath {
		t.Fatalf("canonical state path: got %q, want %q", lock.canonicalStatePath, canonicalStatePath)
	}
	if lock.lockPath != canonicalStatePath+operationLockSuffix {
		t.Fatalf("lock path: got %q, want %q", lock.lockPath, canonicalStatePath+operationLockSuffix)
	}

	info, err := os.Stat(lock.lockPath)
	if err != nil {
		t.Fatalf("unable to inspect lock file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("lock file permissions: got %04o, want 0600", got)
	}

	content, err := os.ReadFile(lock.lockPath)
	if err != nil {
		t.Fatalf("unable to read lock metadata: %v", err)
	}
	var metadata operationLockMetadata
	if err := json.Unmarshal(content, &metadata); err != nil {
		t.Fatalf("unable to decode lock metadata: %v", err)
	}
	if metadata.PID != os.Getpid() || metadata.Operation != "bootstrap aws dry-run" || metadata.CanonicalStatePath != canonicalStatePath {
		t.Fatalf("unexpected lock metadata: %#v", metadata)
	}

	_, err = acquireStateOperationLock(statePath, "bootstrap aws")
	if err == nil {
		t.Fatal("expected a second operation lock acquisition to fail")
	}
	for _, want := range []string{"operation already in progress", canonicalStatePath, "bootstrap aws dry-run"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("contention error %q does not contain %q", err, want)
		}
	}

	if err := lock.Release(); err != nil {
		t.Fatalf("unable to release operation lock: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second release should be idempotent: %v", err)
	}

	reacquired, err := acquireStateOperationLock(statePath, "bootstrap aws")
	if err != nil {
		t.Fatalf("unable to reacquire operation lock: %v", err)
	}
	content, err = os.ReadFile(reacquired.lockPath)
	if err != nil {
		t.Fatalf("unable to read replaced lock metadata: %v", err)
	}
	if err := json.Unmarshal(content, &metadata); err != nil {
		t.Fatalf("unable to decode replaced lock metadata: %v", err)
	}
	if metadata.Operation != "bootstrap aws" {
		t.Fatalf("reacquired lock retained stale operation %q", metadata.Operation)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatalf("unable to release reacquired operation lock: %v", err)
	}
}

func TestStateOperationLockCanonicalizesSymlinkedDirectory(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require additional Windows privileges")
	}

	root := t.TempDir()
	realDirectory := filepath.Join(root, "real")
	if err := os.Mkdir(realDirectory, 0700); err != nil {
		t.Fatalf("unable to create real state directory: %v", err)
	}
	aliasDirectory := filepath.Join(root, "alias")
	if err := os.Symlink(realDirectory, aliasDirectory); err != nil {
		t.Fatalf("unable to create state directory symlink: %v", err)
	}

	realStatePath := filepath.Join(realDirectory, "terraform.tfstate")
	aliasStatePath := filepath.Join(aliasDirectory, "terraform.tfstate")
	lock, err := acquireStateOperationLock(aliasStatePath, "bootstrap aws")
	if err != nil {
		t.Fatalf("unable to acquire operation lock through symlink: %v", err)
	}
	defer func() { _ = lock.Release() }()

	if lock.canonicalStatePath != realStatePath {
		t.Fatalf("canonical state path: got %q, want %q", lock.canonicalStatePath, realStatePath)
	}
	if _, err := acquireStateOperationLock(realStatePath, "bootstrap aws"); err == nil {
		t.Fatal("expected real and symlinked state paths to contend on the same lock")
	}
}

func TestStateOperationLockRejectsDanglingStateSymlink(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("creating symlinks may require additional Windows privileges")
	}

	statePath := filepath.Join(t.TempDir(), "terraform.tfstate")
	if err := os.Symlink("missing-target.tfstate", statePath); err != nil {
		t.Fatalf("unable to create dangling state symlink: %v", err)
	}
	_, err := acquireStateOperationLock(statePath, "bootstrap aws")
	if err == nil || !strings.Contains(err.Error(), "unable to resolve dangling state symlink") {
		t.Fatalf("expected dangling symlink error, got %v", err)
	}
}

func TestBootstrapFailsBeforeTerraformWhenStateOperationIsLocked(t *testing.T) {
	statePath := filepath.Join(t.TempDir(), "terraform.tfstate")
	lock, err := acquireStateOperationLock(statePath, "bootstrap aws dry-run")
	if err != nil {
		t.Fatalf("unable to acquire operation lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--aws-profile=test-profile",
		"--state=" + statePath,
		"--dry-run",
	})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "operation already in progress") {
		t.Fatalf("expected operation lock contention, got %v", err)
	}
	assertCallCounts(t, mock, 0, 0, 0)
}
