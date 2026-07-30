package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPersistTerraformStateAtomically(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "temporary.tfstate")
	destinationPath := filepath.Join(directory, "terraform.tfstate")
	if err := os.WriteFile(sourcePath, []byte(`{"serial":2}`), 0600); err != nil {
		t.Fatalf("unable to write source state: %v", err)
	}
	if err := os.WriteFile(destinationPath, []byte(`{"serial":1}`), 0644); err != nil {
		t.Fatalf("unable to write destination state: %v", err)
	}

	if err := persistTerraformState(sourcePath, destinationPath); err != nil {
		t.Fatalf("unable to persist state: %v", err)
	}
	content, err := os.ReadFile(destinationPath)
	if err != nil {
		t.Fatalf("unable to read persisted state: %v", err)
	}
	if got, want := string(content), `{"serial":2}`; got != want {
		t.Fatalf("persisted state: got %q, want %q", got, want)
	}
	info, err := os.Stat(destinationPath)
	if err != nil {
		t.Fatalf("unable to inspect persisted state: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Errorf("persisted state permissions: got %04o, want 0600", got)
	}
}

func TestPersistTerraformStateRetainsRecoveryFileWhenReplaceFails(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "temporary.tfstate")
	destinationPath := filepath.Join(directory, "terraform.tfstate")
	if err := os.WriteFile(sourcePath, []byte(`{"serial":2}`), 0600); err != nil {
		t.Fatalf("unable to write source state: %v", err)
	}
	if err := os.WriteFile(destinationPath, []byte(`{"serial":1}`), 0600); err != nil {
		t.Fatalf("unable to write destination state: %v", err)
	}

	originalReplaceStateFile := replaceStateFile
	replaceStateFile = func(sourcePath, destinationPath string) error {
		return errors.New("injected replacement failure")
	}
	t.Cleanup(func() { replaceStateFile = originalReplaceStateFile })

	err := persistTerraformState(sourcePath, destinationPath)
	if err == nil {
		t.Fatal("expected state persistence to fail")
	}
	var persistenceErr *statePersistenceError
	if !errors.As(err, &persistenceErr) {
		t.Fatalf("got %T, want *statePersistenceError", err)
	}
	if persistenceErr.RecoveryPath == "" || !strings.Contains(err.Error(), persistenceErr.RecoveryPath) {
		t.Fatalf("persistence error does not identify a recovery state: %v", err)
	}
	recoveryContent, readErr := os.ReadFile(persistenceErr.RecoveryPath)
	if readErr != nil {
		t.Fatalf("unable to read recovery state: %v", readErr)
	}
	if got, want := string(recoveryContent), `{"serial":2}`; got != want {
		t.Fatalf("recovery state: got %q, want %q", got, want)
	}
	destinationContent, readErr := os.ReadFile(destinationPath)
	if readErr != nil {
		t.Fatalf("unable to read original state: %v", readErr)
	}
	if got, want := string(destinationContent), `{"serial":1}`; got != want {
		t.Fatalf("destination state changed: got %q, want %q", got, want)
	}
}

func TestPersistTerraformStateReportsDirectorySyncFailure(t *testing.T) {
	directory := t.TempDir()
	sourcePath := filepath.Join(directory, "temporary.tfstate")
	destinationPath := filepath.Join(directory, "terraform.tfstate")
	if err := os.WriteFile(sourcePath, []byte(`{"serial":2}`), 0600); err != nil {
		t.Fatalf("unable to write source state: %v", err)
	}

	originalSyncStateDirectory := syncStateDirectory
	syncStateDirectory = func(directoryPath string) error {
		return errors.New("injected directory sync failure")
	}
	t.Cleanup(func() { syncStateDirectory = originalSyncStateDirectory })

	err := persistTerraformState(sourcePath, destinationPath)
	if err == nil {
		t.Fatal("expected state persistence to report directory sync failure")
	}
	var persistenceErr *statePersistenceError
	if !errors.As(err, &persistenceErr) {
		t.Fatalf("got %T, want *statePersistenceError", err)
	}
	if persistenceErr.RecoveryPath != destinationPath {
		t.Fatalf("recovery path: got %q, want replaced destination %q", persistenceErr.RecoveryPath, destinationPath)
	}
	content, readErr := os.ReadFile(destinationPath)
	if readErr != nil {
		t.Fatalf("unable to read replaced state: %v", readErr)
	}
	if got, want := string(content), `{"serial":2}`; got != want {
		t.Fatalf("replaced state: got %q, want %q", got, want)
	}
}
