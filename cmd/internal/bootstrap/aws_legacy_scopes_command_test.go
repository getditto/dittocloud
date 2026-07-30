package bootstrap

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func forceAWSLegacyScopesNonInteractive(t *testing.T) {
	t.Helper()
	original := awsLegacyScopeInputIsInteractive
	awsLegacyScopeInputIsInteractive = func() bool { return false }
	t.Cleanup(func() { awsLegacyScopeInputIsInteractive = original })
}

func writeCompleteAWSLegacyState(t *testing.T) string {
	t.Helper()
	return writeTerraformStateTestFile(t, legacyStateFixture(
		map[string]any{
			"aws":    rawAWSLegacyOutput("us-west-2"),
			"secret": map[string]any{"value": "must-not-be-printed"},
		},
		rawAWSLegacyVPCValidationResource(true, "vpc-09e877f9012f52241"),
		rawAWSLegacyEKSMarkerResource(),
	))
}

func TestAWSLegacyScopesGenerationWritesReviewOnlyDraft(t *testing.T) {
	forceAWSLegacyScopesNonInteractive(t)
	setAWSScopesAddReferenceSequence(t, testDefaultScopeRef)
	statePath := writeCompleteAWSLegacyState(t)
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--state=" + statePath,
		"--scopes-file=" + scopesPath,
		"--generate-scopes-file",
	})
	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected generation error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	if mock.workingDir != "" {
		t.Fatalf("Terraform factory was unexpectedly called with %q", mock.workingDir)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state after generation: %v", err)
	}
	if !bytes.Equal(stateAfter, originalState) {
		t.Fatal("legacy scopes generation modified Terraform state")
	}
	scopes, err := loadAWSDeploymentScopes(scopesPath)
	if err != nil {
		t.Fatalf("generated scopes file is invalid: %v", err)
	}
	generated := scopes[testDefaultScopeRef]
	if !generated.Default || generated.ClusterType != awsClusterTypeEKS || generated.Region != "us-west-2" || generated.ScopeTagPolicyVersion != 0 {
		t.Fatalf("unexpected generated scope: %#v", generated)
	}
	if generated.VPC.Mode != awsVPCModeExisting || generated.VPC.ID != "vpc-09e877f9012f52241" {
		t.Fatalf("unexpected generated VPC: %#v", generated.VPC)
	}
	if !strings.Contains(output.String(), "Terraform state was not modified and Terraform was not run") {
		t.Fatalf("generation summary omitted the review-only boundary:\n%s", output.String())
	}
	if strings.Contains(output.String(), "must-not-be-printed") {
		t.Fatalf("generation summary exposed unrelated state output:\n%s", output.String())
	}
}

func TestAWSLegacyScopesGenerationRequiresCompleteEvidenceNonInteractively(t *testing.T) {
	forceAWSLegacyScopesNonInteractive(t)
	statePath := writeTerraformStateTestFile(t, legacyStateFixture(
		map[string]any{"aws": rawAWSLegacyOutput("ap-southeast-2")},
		rawAWSLegacyVPCValidationResource(false, nil),
	))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "--scopes=true", "--state=" + statePath,
		"--scopes-file=" + scopesPath, "--generate-scopes-file",
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "missing required fields: clusterType") {
		t.Fatalf("expected unresolved cluster type error, got %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	if _, statErr := os.Stat(scopesPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("scopes file was created despite missing evidence: %v", statErr)
	}
}

func TestAWSLegacyScopesGenerationNeverOverwritesNonEmptyDestination(t *testing.T) {
	forceAWSLegacyScopesNonInteractive(t)
	statePath := writeCompleteAWSLegacyState(t)
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	original := []byte("# existing reviewed configuration\n")
	if err := os.WriteFile(scopesPath, original, 0640); err != nil {
		t.Fatalf("unable to write scopes fixture: %v", err)
	}
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "--scopes=true", "--state=" + statePath,
		"--scopes-file=" + scopesPath, "--generate-scopes-file",
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "refuses to overwrite non-empty scopes file") {
		t.Fatalf("expected non-overwrite error, got %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	content, readErr := os.ReadFile(scopesPath)
	if readErr != nil || !bytes.Equal(content, original) {
		t.Fatalf("non-empty scopes file changed: content=%q err=%v", content, readErr)
	}
}

func TestAWSLegacyScopesGenerationDetectsLegacyStateWithoutImplicitWrite(t *testing.T) {
	forceAWSLegacyScopesNonInteractive(t)
	statePath := writeCompleteAWSLegacyState(t)
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "--scopes=true", "--state=" + statePath,
		"--scopes-file=" + scopesPath,
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "rerun with --generate-scopes-file") {
		t.Fatalf("expected explicit-generation error, got %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	if _, statErr := os.Stat(scopesPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("scopes file was written implicitly: %v", statErr)
	}
}

func TestAWSLegacyScopesGenerationInteractiveCompletion(t *testing.T) {
	setAWSScopesAddReferenceSequence(t, testDefaultScopeRef)
	originalInteractive := awsLegacyScopeInputIsInteractive
	originalConfirm := awsLegacyScopeConfirmGeneration
	originalPromptString := awsLegacyScopePromptString
	awsLegacyScopeInputIsInteractive = func() bool { return true }
	awsLegacyScopeConfirmGeneration = func() bool { return true }
	awsLegacyScopePromptString = func(label, defaultValue string) string {
		if strings.Contains(label, "kubeadm") {
			return awsClusterTypeKubeadm
		}
		return ""
	}
	t.Cleanup(func() {
		awsLegacyScopeInputIsInteractive = originalInteractive
		awsLegacyScopeConfirmGeneration = originalConfirm
		awsLegacyScopePromptString = originalPromptString
	})
	statePath := writeTerraformStateTestFile(t, legacyStateFixture(
		map[string]any{"aws": rawAWSLegacyOutput("ap-southeast-2")},
		rawAWSLegacyVPCValidationResource(false, nil),
	))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "--scopes=true", "--state=" + statePath,
		"--scopes-file=" + scopesPath,
	})

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected interactive generation error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	scopes, err := loadAWSDeploymentScopes(scopesPath)
	if err != nil {
		t.Fatalf("unable to load generated scopes file: %v", err)
	}
	if scopes[testDefaultScopeRef].ClusterType != awsClusterTypeKubeadm {
		t.Fatalf("interactive kubeadm confirmation was not persisted: %#v", scopes[testDefaultScopeRef])
	}
}

func TestAWSLegacyScopesGenerationGuardsFlagsAndStateBoundary(t *testing.T) {
	forceAWSLegacyScopesNonInteractive(t)
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	registryStatePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	))
	tests := []struct {
		name      string
		args      []string
		wantError string
	}{
		{
			name:      "requires scope mode",
			args:      []string{"aws", "--scopes-file=" + scopesPath, "--generate-scopes-file"},
			wantError: "requires --scopes=true",
		},
		{
			name:      "requires legacy state",
			args:      []string{"aws", "--scopes=true", "--scopes-file=" + scopesPath, "--generate-scopes-file"},
			wantError: "requires an existing legacy Terraform state",
		},
		{
			name: "rejects registry-backed state without YAML",
			args: []string{
				"aws", "--scopes=true", "--state=" + registryStatePath,
				"--scopes-file=" + scopesPath, "--generate-scopes-file",
			},
			wantError: "already contains a scope registry",
		},
		{
			name: "rejects lifecycle flags",
			args: []string{
				"aws", "--scopes=true", "--state=" + writeCompleteAWSLegacyState(t),
				"--scopes-file=" + scopesPath, "--generate-scopes-file", "--dry-run",
			},
			wantError: "--dry-run cannot be used",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd, mock := setupBootstrapTest(t, test.args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
			assertNoTerraformLifecycleCalls(t, mock)
		})
	}
}

func TestAWSLegacyScopesGenerationReportsAtomicRecoveryAndLockContention(t *testing.T) {
	forceAWSLegacyScopesNonInteractive(t)
	t.Run("atomic replacement failure leaves the destination absent", func(t *testing.T) {
		setAWSScopesAddReferenceSequence(t, testDefaultScopeRef)
		statePath := writeCompleteAWSLegacyState(t)
		scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
		originalReplace := replaceScopesFile
		replaceScopesFile = func(sourcePath, destinationPath string) error {
			return errors.New("injected replacement failure")
		}
		t.Cleanup(func() { replaceScopesFile = originalReplace })
		cmd, mock := setupBootstrapTest(t, []string{
			"aws", "--scopes=true", "--state=" + statePath,
			"--scopes-file=" + scopesPath, "--generate-scopes-file",
		})

		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "recovery file retained at") {
			t.Fatalf("expected atomic recovery error, got %v", err)
		}
		assertNoTerraformLifecycleCalls(t, mock)
		if _, statErr := os.Stat(scopesPath); !errors.Is(statErr, os.ErrNotExist) {
			t.Fatalf("destination exists after failed replacement: %v", statErr)
		}
	})

	t.Run("scopes-file contention releases the state operation lock", func(t *testing.T) {
		statePath := writeCompleteAWSLegacyState(t)
		scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
		fileLock, err := acquireScopesFileLock(scopesPath, "test owner")
		if err != nil {
			t.Fatalf("unable to acquire scopes-file lock: %v", err)
		}
		defer func() { _ = fileLock.Release() }()
		cmd, mock := setupBootstrapTest(t, []string{
			"aws", "--scopes=true", "--state=" + statePath,
			"--scopes-file=" + scopesPath, "--generate-scopes-file",
		})

		err = cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "scopes-file operation already in progress") {
			t.Fatalf("expected scopes-file contention, got %v", err)
		}
		assertNoTerraformLifecycleCalls(t, mock)
		operationLock, err := acquireStateOperationLock(statePath, "test verification")
		if err != nil {
			t.Fatalf("state lock was not released: %v", err)
		}
		_ = operationLock.Release()
	})
}
