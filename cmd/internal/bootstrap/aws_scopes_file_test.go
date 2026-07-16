package bootstrap

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestAWSDeploymentScopesDocumentPreservesCommentsAndOrder(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scopes.yaml")
	content := `# account scopes
` + testDefaultScopeRef + `: # immutable default
  default: true
  # default Region
  region: ap-southeast-2
  vpc:
    mode: capi
`
	if err := os.WriteFile(path, []byte(content), 0640); err != nil {
		t.Fatalf("unable to write scopes fixture: %v", err)
	}

	document, err := loadAWSDeploymentScopesDocument(path)
	if err != nil {
		t.Fatalf("unable to load scopes document: %v", err)
	}
	encoded, err := document.appendScope(testSecondaryScopeRef, AWSDeploymentScope{
		ClusterType: awsClusterTypeEKS,
		Region:      "us-west-2",
		VPC: AWSScopeVPC{
			Mode: awsVPCModeExisting,
			ID:   "vpc-09e877f9012f52241",
		},
	})
	if err != nil {
		t.Fatalf("unable to append scope: %v", err)
	}
	encodedString := string(encoded)
	for _, comment := range []string{"# account scopes", "# immutable default", "# default Region"} {
		if !strings.Contains(encodedString, comment) {
			t.Errorf("encoded scopes file lost comment %q:\n%s", comment, encodedString)
		}
	}
	defaultIndex := strings.Index(encodedString, testDefaultScopeRef)
	secondaryIndex := strings.Index(encodedString, testSecondaryScopeRef)
	if defaultIndex < 0 || secondaryIndex <= defaultIndex {
		t.Fatalf("scope order was not preserved:\n%s", encodedString)
	}
	if !strings.Contains(encodedString[secondaryIndex:], "scopeTagPolicyVersion: 0") {
		t.Fatalf("new scope does not explicitly start at tag policy version 0:\n%s", encodedString)
	}

	if err := persistAWSDeploymentScopesFile(path, encoded, document.permissions); err != nil {
		t.Fatalf("unable to persist scopes document: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("unable to inspect persisted scopes file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Fatalf("persisted permissions: got %04o, want 0640", got)
	}
	scopes, err := loadAWSDeploymentScopes(path)
	if err != nil {
		t.Fatalf("persisted scopes failed strict validation: %v", err)
	}
	if len(scopes) != 2 || scopes[testSecondaryScopeRef].ScopeTagPolicyVersion != 0 {
		t.Fatalf("unexpected persisted scopes: %#v", scopes)
	}
}

func TestAWSDeploymentScopesDocumentRejectsInvalidExistingFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scopes.yaml")
	content := testDefaultScopeRef + `:
  default: true
  region: ap-southeast-2
  unsupported: true
  vpc:
    mode: capi
`
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("unable to write invalid scopes fixture: %v", err)
	}
	_, err := loadAWSDeploymentScopesDocument(path)
	if err == nil || !strings.Contains(err.Error(), "field unsupported not found") {
		t.Fatalf("expected strict existing-file error, got %v", err)
	}
}

func TestScopesFileLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "scopes.yaml")
	lock, err := acquireScopesFileLock(path, "bootstrap aws scopes add")
	if err != nil {
		t.Fatalf("unable to acquire scopes-file lock: %v", err)
	}
	defer func() { _ = lock.Release() }()

	_, err = acquireScopesFileLock(path, "bootstrap aws scopes tags verify")
	if err == nil {
		t.Fatal("expected scopes-file lock contention")
	}
	for _, want := range []string{"scopes-file operation already in progress", "bootstrap aws scopes add", lock.canonicalPath} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("contention error %q does not contain %q", err, want)
		}
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("unable to release scopes-file lock: %v", err)
	}
	if err := lock.Release(); err != nil {
		t.Fatalf("second scopes-file lock release should be idempotent: %v", err)
	}
	reacquired, err := acquireScopesFileLock(path, "bootstrap aws scopes add")
	if err != nil {
		t.Fatalf("unable to reacquire scopes-file lock: %v", err)
	}
	if err := reacquired.Release(); err != nil {
		t.Fatalf("unable to release reacquired scopes-file lock: %v", err)
	}
}

func TestPersistAWSDeploymentScopesFileRetainsRecoveryFile(t *testing.T) {
	directory := t.TempDir()
	path := filepath.Join(directory, "scopes.yaml")
	originalContent := []byte("original\n")
	newContent := []byte("replacement\n")
	if err := os.WriteFile(path, originalContent, 0600); err != nil {
		t.Fatalf("unable to write original scopes file: %v", err)
	}

	originalReplaceScopesFile := replaceScopesFile
	replaceScopesFile = func(sourcePath, destinationPath string) error {
		return errors.New("injected scopes-file replacement failure")
	}
	t.Cleanup(func() { replaceScopesFile = originalReplaceScopesFile })

	err := persistAWSDeploymentScopesFile(path, newContent, 0600)
	if err == nil {
		t.Fatal("expected scopes-file persistence failure")
	}
	var persistenceErr *scopesFilePersistenceError
	if !errors.As(err, &persistenceErr) {
		t.Fatalf("got %T, want *scopesFilePersistenceError", err)
	}
	if persistenceErr.RecoveryPath == "" || !strings.Contains(err.Error(), persistenceErr.RecoveryPath) {
		t.Fatalf("persistence error does not identify recovery file: %v", err)
	}
	recoveryContent, readErr := os.ReadFile(persistenceErr.RecoveryPath)
	if readErr != nil {
		t.Fatalf("unable to read recovery file: %v", readErr)
	}
	if string(recoveryContent) != string(newContent) {
		t.Fatalf("recovery content: got %q, want %q", recoveryContent, newContent)
	}
	unchangedContent, readErr := os.ReadFile(path)
	if readErr != nil {
		t.Fatalf("unable to read original scopes file: %v", readErr)
	}
	if string(unchangedContent) != string(originalContent) {
		t.Fatalf("original scopes file changed: got %q, want %q", unchangedContent, originalContent)
	}
}
