package bootstrap

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func setAWSScopesAddReferenceSequence(t *testing.T, scopeRefs ...string) {
	t.Helper()
	originalGenerator := nextAWSDeploymentScopeReference
	nextAWSDeploymentScopeReference = func() (string, error) {
		if len(scopeRefs) == 0 {
			return "", errors.New("scope reference test sequence exhausted")
		}
		scopeRef := scopeRefs[0]
		scopeRefs = scopeRefs[1:]
		return scopeRef, nil
	}
	t.Cleanup(func() { nextAWSDeploymentScopeReference = originalGenerator })
}

func forceAWSScopesAddNonInteractive(t *testing.T) {
	t.Helper()
	originalInteractiveCheck := awsScopeAddInputIsInteractive
	awsScopeAddInputIsInteractive = func() bool { return false }
	t.Cleanup(func() { awsScopeAddInputIsInteractive = originalInteractiveCheck })
}

func assertNoTerraformLifecycleCalls(t *testing.T, mock *mockTerraformExecutor) {
	t.Helper()
	assertCallCounts(t, mock, 0, 0, 0)
	if mock.importCallCount != 0 {
		t.Errorf("expected zero Import calls, got %d", mock.importCallCount)
	}
}

func TestAWSScopesAddCreatesGreenfieldDefaultWithoutTerraform(t *testing.T) {
	forceAWSScopesAddNonInteractive(t)
	setAWSScopesAddReferenceSequence(t, testDefaultScopeRef)
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "scopes", "add",
		"--scopes-file=" + scopesPath,
		"--default",
		"--region=ap-southeast-2",
		"--cluster-type=eks",
		"--cluster-name=default-cluster",
		"--vpc-mode=dittocloud",
		"--vpc-name=ditto-default",
		"--vpc-cidr=10.210.0.0/16",
	})
	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected scopes-add error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	if strings.TrimSpace(output.String()) != testDefaultScopeRef {
		t.Fatalf("command output: got %q, want generated reference %q", output.String(), testDefaultScopeRef)
	}
	scopes, err := loadAWSDeploymentScopes(scopesPath)
	if err != nil {
		t.Fatalf("unable to load created scopes file: %v", err)
	}
	created := scopes[testDefaultScopeRef]
	if !created.Default || created.ClusterType != awsClusterTypeEKS || created.ScopeTagPolicyVersion != 0 {
		t.Fatalf("unexpected created default scope: %#v", created)
	}
	content, err := os.ReadFile(scopesPath)
	if err != nil {
		t.Fatalf("unable to read created scopes file: %v", err)
	}
	if !strings.Contains(string(content), "scopeTagPolicyVersion: 0") {
		t.Fatalf("created scopes file does not explicitly record policy version 0:\n%s", content)
	}
}

func TestAWSScopesAddExposesNoReferenceOrPolicyOverride(t *testing.T) {
	cmd, _ := setupBootstrapTest(t, nil)
	addCommand, _, err := cmd.Find([]string{"aws", "scopes", "add"})
	if err != nil {
		t.Fatalf("unable to find scopes add command: %v", err)
	}
	for _, prohibitedFlag := range []string{"scope-ref", "scope-tag-policy-version"} {
		if addCommand.Flags().Lookup(prohibitedFlag) != nil {
			t.Errorf("scopes add unexpectedly exposes --%s", prohibitedFlag)
		}
	}
}

func TestAWSScopesAddAppendsNonDefaultAndRegeneratesCollision(t *testing.T) {
	forceAWSScopesAddNonInteractive(t)
	setAWSScopesAddReferenceSequence(t, testDefaultScopeRef, testSecondaryScopeRef)
	scopesPath := writeAWSScopeTestFile(t, `# preserve this comment
`+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`)
	statePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	))
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "scopes", "add",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
		"--region=us-west-2",
		"--vpc-mode=existing",
		"--vpc-id=vpc-09e877f9012f52241",
	})
	var output bytes.Buffer
	cmd.SetOut(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected scopes-add error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	if strings.TrimSpace(output.String()) != testSecondaryScopeRef {
		t.Fatalf("command output: got %q, want regenerated reference %q", output.String(), testSecondaryScopeRef)
	}
	content, err := os.ReadFile(scopesPath)
	if err != nil {
		t.Fatalf("unable to read updated scopes file: %v", err)
	}
	if !strings.Contains(string(content), "# preserve this comment") {
		t.Fatalf("updated scopes file lost existing comment:\n%s", content)
	}
	if strings.Index(string(content), testDefaultScopeRef) >= strings.Index(string(content), testSecondaryScopeRef) {
		t.Fatalf("new scope was not appended after existing scope:\n%s", content)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state after scopes add: %v", err)
	}
	if !bytes.Equal(stateAfter, originalState) {
		t.Fatal("configuration-only scopes add modified Terraform state")
	}
}

func TestAWSScopesAddAcceptsEmptyGreenfieldFile(t *testing.T) {
	forceAWSScopesAddNonInteractive(t)
	setAWSScopesAddReferenceSequence(t, testDefaultScopeRef)
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	if err := os.WriteFile(scopesPath, nil, 0640); err != nil {
		t.Fatalf("unable to create empty scopes file: %v", err)
	}
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "scopes", "add",
		"--scopes-file=" + scopesPath,
		"--default",
		"--region=ap-southeast-2",
		"--vpc-mode=capi",
	})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected scopes-add error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	info, err := os.Stat(scopesPath)
	if err != nil {
		t.Fatalf("unable to inspect created scopes file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0640 {
		t.Fatalf("created scopes file permissions: got %04o, want 0640", got)
	}
}

func TestAWSScopesAddGuardsConfigurationAndStateBoundaries(t *testing.T) {
	forceAWSScopesAddNonInteractive(t)
	validArgs := func(scopesPath string) []string {
		return []string{
			"aws", "scopes", "add",
			"--scopes-file=" + scopesPath,
			"--region=ap-southeast-2",
			"--vpc-mode=capi",
		}
	}

	legacyStatePath := writeTerraformStateTestFile(t, rawTerraformStateWithResources([]any{
		map[string]any{
			"mode": "managed", "type": "aws_vpc", "name": "legacy", "instances": []any{},
		},
	}))
	scopedStatePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	))
	existingScopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`)
	invalidScopesPath := filepath.Join(t.TempDir(), "invalid-scopes.yaml")
	invalidScopesContent := testDefaultScopeRef + `:
  default: true
  region: ap-southeast-2
  unsupported: true
  vpc:
    mode: capi
`
	if err := os.WriteFile(invalidScopesPath, []byte(invalidScopesContent), 0600); err != nil {
		t.Fatalf("unable to write invalid scopes fixture: %v", err)
	}

	tests := []struct {
		name      string
		args      []string
		wantError string
	}{
		{
			name:      "requires default for first greenfield scope",
			args:      validArgs(filepath.Join(t.TempDir(), "missing.yaml")),
			wantError: "--default is required to create the first scope",
		},
		{
			name: "rejects default when scopes file exists",
			args: append(
				validArgs(existingScopesPath),
				"--default",
			),
			wantError: "--default cannot be used when adding to an existing scopes file",
		},
		{
			name: "routes legacy state to migration generation",
			args: append(
				validArgs(filepath.Join(t.TempDir(), "legacy.yaml")),
				"--default",
				"--state="+legacyStatePath,
			),
			wantError: "guarded legacy scopes-file generation workflow",
		},
		{
			name: "rejects empty configuration for scoped state",
			args: append(
				validArgs(filepath.Join(t.TempDir(), "scoped.yaml")),
				"--default",
				"--state="+scopedStatePath,
			),
			wantError: "already contains a scope registry but the scopes file is empty",
		},
		{
			name: "requires legacy registry seed before adding",
			args: append(
				validArgs(existingScopesPath),
				"--state="+legacyStatePath,
			),
			wantError: "must seed its default scope registry before additional scopes are added",
		},
		{
			name: "rejects Terraform lifecycle flags",
			args: append(
				validArgs(existingScopesPath),
				"--dry-run",
			),
			wantError: "--dry-run cannot be used with configuration-only scopes add",
		},
		{
			name:      "rejects invalid existing scopes input",
			args:      validArgs(invalidScopesPath),
			wantError: "field unsupported not found",
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
	invalidContentAfter, err := os.ReadFile(invalidScopesPath)
	if err != nil {
		t.Fatalf("unable to read invalid scopes fixture after rejected add: %v", err)
	}
	if string(invalidContentAfter) != invalidScopesContent {
		t.Fatalf("invalid existing scopes file was modified:\n%s", invalidContentAfter)
	}
}

func TestAWSScopesAddValidatesModeSpecificFields(t *testing.T) {
	forceAWSScopesAddNonInteractive(t)
	tests := []struct {
		name      string
		extraArgs []string
		wantError string
	}{
		{name: "requires scopes file", extraArgs: []string{"--vpc-mode=capi"}, wantError: "--scopes-file is required"},
		{name: "requires Region", extraArgs: []string{"--vpc-mode=capi"}, wantError: "--region is required"},
		{name: "requires VPC mode", extraArgs: nil, wantError: "--vpc-mode is required"},
		{name: "requires Dittocloud VPC name", extraArgs: []string{"--vpc-mode=dittocloud", "--vpc-cidr=10.210.0.0/16"}, wantError: "--vpc-name is required"},
		{name: "requires existing VPC ID", extraArgs: []string{"--vpc-mode=existing"}, wantError: "--vpc-id is required"},
		{name: "rejects VPC name in CAPI mode", extraArgs: []string{"--vpc-mode=capi", "--vpc-name=unused"}, wantError: "cannot set vpc.name, vpc.cidr, or vpc.natGatewayName"},
		{name: "rejects VPC ID in Dittocloud mode", extraArgs: []string{"--vpc-mode=dittocloud", "--vpc-name=ditto", "--vpc-cidr=10.210.0.0/16", "--vpc-id=vpc-09e877f9012f52241"}, wantError: "cannot set vpc.id"},
		{name: "rejects unsupported cluster type", extraArgs: []string{"--vpc-mode=capi", "--cluster-type=kops"}, wantError: "clusterType must be"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
			regionArgument := "--region=ap-southeast-2"
			if test.name == "requires Region" {
				regionArgument = "--region="
			}
			args := []string{
				"aws", "scopes", "add",
				"--scopes-file=" + scopesPath,
				"--default",
				regionArgument,
			}
			if test.name == "requires scopes file" {
				args[3] = "--scopes-file="
			}
			args = append(args, test.extraArgs...)
			cmd, mock := setupBootstrapTest(t, args)
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
			assertNoTerraformLifecycleCalls(t, mock)
		})
	}
}

func TestAWSScopesAddLockOrdering(t *testing.T) {
	forceAWSScopesAddNonInteractive(t)
	t.Run("state lock contention happens before scopes-file locking", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "terraform.tfstate")
		scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
		operationLock, err := acquireStateOperationLock(statePath, "test owner")
		if err != nil {
			t.Fatalf("unable to acquire state lock: %v", err)
		}
		defer func() { _ = operationLock.Release() }()

		cmd, mock := setupBootstrapTest(t, []string{
			"aws", "scopes", "add",
			"--scopes-file=" + scopesPath,
			"--state=" + statePath,
			"--default",
			"--region=ap-southeast-2",
			"--vpc-mode=capi",
		})
		err = cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "operation already in progress") {
			t.Fatalf("expected state-lock contention, got %v", err)
		}
		assertNoTerraformLifecycleCalls(t, mock)

		fileLock, err := acquireScopesFileLock(scopesPath, "test verification")
		if err != nil {
			t.Fatalf("scopes-file lock was unexpectedly acquired before state-lock failure: %v", err)
		}
		_ = fileLock.Release()
	})

	t.Run("scopes-file contention releases the state lock", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "terraform.tfstate")
		scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
		fileLock, err := acquireScopesFileLock(scopesPath, "test owner")
		if err != nil {
			t.Fatalf("unable to acquire scopes-file lock: %v", err)
		}
		defer func() { _ = fileLock.Release() }()

		cmd, mock := setupBootstrapTest(t, []string{
			"aws", "scopes", "add",
			"--scopes-file=" + scopesPath,
			"--state=" + statePath,
			"--default",
			"--region=ap-southeast-2",
			"--vpc-mode=capi",
		})
		err = cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "scopes-file operation already in progress") {
			t.Fatalf("expected scopes-file contention, got %v", err)
		}
		assertNoTerraformLifecycleCalls(t, mock)

		operationLock, err := acquireStateOperationLock(statePath, "test verification")
		if err != nil {
			t.Fatalf("state lock was not released after scopes-file contention: %v", err)
		}
		_ = operationLock.Release()
	})
}

func TestAWSScopesAddReportsAtomicWriteRecoveryPath(t *testing.T) {
	forceAWSScopesAddNonInteractive(t)
	setAWSScopesAddReferenceSequence(t, testSecondaryScopeRef)
	scopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`)
	originalContent, err := os.ReadFile(scopesPath)
	if err != nil {
		t.Fatalf("unable to read original scopes file: %v", err)
	}
	originalReplaceScopesFile := replaceScopesFile
	replaceScopesFile = func(sourcePath, destinationPath string) error {
		return errors.New("injected replacement failure")
	}
	t.Cleanup(func() { replaceScopesFile = originalReplaceScopesFile })

	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "scopes", "add",
		"--scopes-file=" + scopesPath,
		"--region=us-west-2",
		"--vpc-mode=capi",
	})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "recovery file retained at") {
		t.Fatalf("expected atomic-write recovery error, got %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	unchangedContent, readErr := os.ReadFile(scopesPath)
	if readErr != nil {
		t.Fatalf("unable to read scopes file after failed write: %v", readErr)
	}
	if string(unchangedContent) != string(originalContent) {
		t.Fatalf("scopes file changed after failed replacement:\n%s", unchangedContent)
	}
}
