package bootstrap

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func rawRecoverableScopeState(scopes AWSDeploymentScopes) map[string]any {
	registryInstances := make([]map[string]any, 0, len(scopes))
	policyInstances := make([]map[string]any, 0, len(scopes))
	configurationInstances := make([]map[string]any, 0, len(scopes))
	for _, scopeRef := range sortedAWSDeploymentScopeRefs(scopes) {
		scope := scopes[scopeRef]
		registryInstances = append(registryInstances, rawScopeRegistryInstance(scopeRef, scopeRef, scope.Default))
		policyInstances = append(policyInstances, rawScopeTagPolicyInstance(scopeRef, scopeRef, scope.ScopeTagPolicyVersion))
		configurationInstances = append(configurationInstances, rawScopeConfigurationInstance(scopeRef, scopeRef, scope))
	}
	state := rawScopeRegistryState(registryInstances...)
	appendRawTerraformStateResource(state, rawScopeTagPolicyResource(policyInstances...))
	appendRawTerraformStateResource(state, rawScopeConfigurationResource(configurationInstances...))
	return state
}

func testRecoverableScopes() AWSDeploymentScopes {
	return AWSDeploymentScopes{
		testDefaultScopeRef: {
			Default:               true,
			ClusterName:           "default-kubeadm",
			ClusterType:           awsClusterTypeKubeadm,
			Region:                "ap-southeast-2",
			ScopeTagPolicyVersion: 0,
			VPC: AWSScopeVPC{
				Mode:                 awsVPCModeDittocloud,
				Name:                 "default-vpc",
				CIDR:                 "10.210.0.0/16",
				PublicSubnetNetmask:  awsLegacyPublicSubnetNetmask,
				PrivateSubnetNetmask: awsLegacyPrivateSubnetNetmask,
				NATGatewayName:       "default-kubeadm-nat",
			},
		},
		testSecondaryScopeRef: {
			ClusterName:           "secondary-eks",
			ClusterType:           awsClusterTypeEKS,
			Region:                "us-west-2",
			ScopeTagPolicyVersion: 1,
			VPC: AWSScopeVPC{
				Mode: awsVPCModeExisting,
				ID:   "vpc-09e877f9012f52241",
			},
		},
	}
}

func executeAWSScopesRecoverTest(t *testing.T, statePath, scopesPath string) (*mockTerraformExecutor, string, error) {
	t.Helper()
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "scopes", "recover",
		"--state=" + statePath,
		"--scopes-file=" + scopesPath,
	})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return mock, output.String(), err
}

func TestAWSScopesRecoverExactMultiScopeRoundTripWithoutTerraform(t *testing.T) {
	wantScopes := testRecoverableScopes()
	statePath := writeTerraformStateTestFile(t, rawRecoverableScopeState(wantScopes))
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")

	mock, output, err := executeAWSScopesRecoverTest(t, statePath, scopesPath)
	if err != nil {
		t.Fatalf("unexpected scopes recovery error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)

	gotScopes, err := loadAWSDeploymentScopes(scopesPath)
	if err != nil {
		t.Fatalf("unable to load recovered scopes file: %v", err)
	}
	if !reflect.DeepEqual(gotScopes, wantScopes) {
		t.Fatalf("recovered scopes:\n got: %#v\nwant: %#v", gotScopes, wantScopes)
	}
	content, err := os.ReadFile(scopesPath)
	if err != nil {
		t.Fatalf("unable to read recovered scopes file: %v", err)
	}
	if strings.Index(string(content), testDefaultScopeRef) >= strings.Index(string(content), testSecondaryScopeRef) {
		t.Fatalf("recovered scopes are not in lexical order:\n%s", content)
	}
	info, err := os.Stat(scopesPath)
	if err != nil {
		t.Fatalf("unable to inspect recovered scopes file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("recovered scopes permissions: got %04o, want 0600", got)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state after recovery: %v", err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("review-only scopes recovery modified Terraform state")
	}
	for _, want := range []string{
		"Recovered the last applied AWS deployment scopes (2)",
		testDefaultScopeRef + " [default]",
		"clusterType=eks",
		"scopeTagPolicyVersion=1",
		"Terraform state was not changed",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("recovery output does not contain %q:\n%s", want, output)
		}
	}
}

func TestAWSScopesRecoverExactSingleScopeIntoEmptyFile(t *testing.T) {
	scopes := AWSDeploymentScopes{testDefaultScopeRef: testRecoverableScopes()[testDefaultScopeRef]}
	statePath := writeTerraformStateTestFile(t, rawRecoverableScopeState(scopes))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	if err := os.WriteFile(scopesPath, nil, 0644); err != nil {
		t.Fatalf("unable to create empty recovery destination: %v", err)
	}

	mock, _, err := executeAWSScopesRecoverTest(t, statePath, scopesPath)
	if err != nil {
		t.Fatalf("unexpected single-scope recovery error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	info, err := os.Stat(scopesPath)
	if err != nil {
		t.Fatalf("unable to inspect recovered scopes file: %v", err)
	}
	if got := info.Mode().Perm(); got != 0600 {
		t.Fatalf("recovered scopes permissions: got %04o, want 0600", got)
	}
}

func TestAWSScopesRecoverRejectsUnsafeOrIncompleteEvidence(t *testing.T) {
	validScopes := testRecoverableScopes()

	tests := []struct {
		name            string
		state           map[string]any
		missingState    bool
		destination     string
		wantError       string
		wantDestination string
	}{
		{
			name:         "missing state",
			missingState: true,
			wantError:    "is empty; exact AWS scopes recovery requires",
		},
		{
			name: "registry without configuration snapshots",
			state: rawScopeRegistryState(
				rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
			),
			wantError: "has no scope configuration snapshots",
		},
		{
			name: "missing one configuration snapshot",
			state: func() map[string]any {
				state := rawRecoverableScopeState(validScopes)
				resources := state["resources"].([]any)
				configurationResource := resources[len(resources)-1].(map[string]any)
				configurationResource["instances"] = configurationResource["instances"].([]any)[:1]
				return state
			}(),
			wantError: "missing snapshots: [" + testSecondaryScopeRef + "]",
		},
		{
			name: "default marker mismatch",
			state: func() map[string]any {
				state := rawRecoverableScopeState(validScopes)
				resources := state["resources"].([]any)
				configurationResource := resources[len(resources)-1].(map[string]any)
				instance := configurationResource["instances"].([]any)[0].(map[string]any)
				input := instance["attributes"].(map[string]any)["input"].(map[string]any)
				configuration := input["value"].(map[string]any)["configuration"].(map[string]any)
				configuration["default"] = false
				return state
			}(),
			wantError: "has default=false but the identity registry has default=true",
		},
		{
			name: "applied policy marker mismatch",
			state: func() map[string]any {
				state := rawRecoverableScopeState(validScopes)
				resources := state["resources"].([]any)
				policyResource := resources[len(resources)-2].(map[string]any)
				instance := policyResource["instances"].([]any)[1].(map[string]any)
				input := instance["attributes"].(map[string]any)["input"].(map[string]any)
				input["value"].(map[string]any)["policy_version"] = 0
				return state
			}(),
			wantError: "has tag policy version 1 but the applied marker has version 0",
		},
		{
			name: "unsupported snapshot schema",
			state: func() map[string]any {
				state := rawRecoverableScopeState(validScopes)
				resources := state["resources"].([]any)
				configurationResource := resources[len(resources)-1].(map[string]any)
				instance := configurationResource["instances"].([]any)[0].(map[string]any)
				input := instance["attributes"].(map[string]any)["input"].(map[string]any)
				input["value"].(map[string]any)["schema_version"] = awsScopeConfigurationSchemaVersion + 1
				return state
			}(),
			wantError: "unsupported schema_version",
		},
		{
			name:        "non-empty destination",
			state:       rawRecoverableScopeState(validScopes),
			destination: "do not replace\n",
			wantError:   "refusing to overwrite non-empty AWS scopes recovery destination",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statePath := filepath.Join(t.TempDir(), "terraform.tfstate")
			if !test.missingState {
				statePath = writeTerraformStateTestFile(t, test.state)
			}
			scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
			if test.destination != "" {
				if err := os.WriteFile(scopesPath, []byte(test.destination), 0600); err != nil {
					t.Fatalf("unable to write recovery destination fixture: %v", err)
				}
			}

			mock, _, err := executeAWSScopesRecoverTest(t, statePath, scopesPath)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
			assertNoTerraformLifecycleCalls(t, mock)
			content, readErr := os.ReadFile(scopesPath)
			if test.destination != "" {
				if readErr != nil || string(content) != test.destination {
					t.Fatalf("non-empty destination changed: content=%q error=%v", content, readErr)
				}
			} else if !errors.Is(readErr, os.ErrNotExist) {
				t.Fatalf("rejected recovery unexpectedly wrote destination: content=%q error=%v", content, readErr)
			}
		})
	}
}

func TestAWSScopesRecoverLockOrdering(t *testing.T) {
	scopes := testRecoverableScopes()
	statePath := writeTerraformStateTestFile(t, rawRecoverableScopeState(scopes))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")

	t.Run("state contention happens before scopes-file locking", func(t *testing.T) {
		operationLock, err := acquireStateOperationLock(statePath, "test owner")
		if err != nil {
			t.Fatalf("unable to acquire state lock: %v", err)
		}
		defer func() { _ = operationLock.Release() }()

		mock, _, err := executeAWSScopesRecoverTest(t, statePath, scopesPath)
		if err == nil || !strings.Contains(err.Error(), "operation already in progress") {
			t.Fatalf("expected state-lock contention, got %v", err)
		}
		assertNoTerraformLifecycleCalls(t, mock)
		fileLock, err := acquireScopesFileLock(scopesPath, "test verification")
		if err != nil {
			t.Fatalf("scopes-file lock was unexpectedly held: %v", err)
		}
		_ = fileLock.Release()
	})

	t.Run("scopes-file contention releases the state lock", func(t *testing.T) {
		fileLock, err := acquireScopesFileLock(scopesPath, "test owner")
		if err != nil {
			t.Fatalf("unable to acquire scopes-file lock: %v", err)
		}
		defer func() { _ = fileLock.Release() }()

		mock, _, err := executeAWSScopesRecoverTest(t, statePath, scopesPath)
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

func TestAWSScopesRecoverRetainsAtomicWriteRecoveryFile(t *testing.T) {
	scopes := testRecoverableScopes()
	statePath := writeTerraformStateTestFile(t, rawRecoverableScopeState(scopes))
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	originalReplaceScopesFile := replaceScopesFile
	replaceScopesFile = func(sourcePath, destinationPath string) error {
		return errors.New("injected recovery replacement failure")
	}
	t.Cleanup(func() { replaceScopesFile = originalReplaceScopesFile })

	mock, _, err := executeAWSScopesRecoverTest(t, statePath, scopesPath)
	if err == nil || !strings.Contains(err.Error(), "recovery file retained at") {
		t.Fatalf("expected atomic-write recovery error, got %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
	if _, statErr := os.Stat(scopesPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("failed atomic recovery unexpectedly created destination: %v", statErr)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state after failed recovery: %v", err)
	}
	if !bytes.Equal(stateBefore, stateAfter) {
		t.Fatal("failed scopes recovery modified Terraform state")
	}
}

func TestAWSScopesRecoverRejectsTerraformLifecycleFlags(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawRecoverableScopeState(testRecoverableScopes()))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	cmd, mock := setupBootstrapTest(t, []string{
		"aws", "scopes", "recover",
		"--state=" + statePath,
		"--scopes-file=" + scopesPath,
		"--dry-run",
	})
	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--dry-run cannot be used with review-only scopes recover") {
		t.Fatalf("got %v, want lifecycle-flag rejection", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
}
