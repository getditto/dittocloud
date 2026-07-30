package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
	"testing"
	"time"
)

func awsScopedImportTestScopes() AWSDeploymentScopes {
	defaultScope := testScope(true)
	defaultScope.ClusterType = awsClusterTypeEKS
	secondaryScope := testScope(false)
	secondaryScope.ClusterType = awsClusterTypeEKS
	secondaryScope.Region = "us-west-2"
	secondaryScope.VPC = AWSScopeVPC{Mode: awsVPCModeExisting, ID: "vpc-09e877f9012f52241"}
	return AWSDeploymentScopes{
		testDefaultScopeRef:   defaultScope,
		testSecondaryScopeRef: secondaryScope,
	}
}

func writeAWSScopedImportTestScopes(t *testing.T) string {
	t.Helper()
	return writeAWSScopeTestFile(t, testDefaultScopeRef+`:
  default: true
  clusterType: eks
  region: ap-southeast-2
  vpc:
    mode: capi
`+testSecondaryScopeRef+`:
  clusterType: eks
  region: us-west-2
  vpc:
    mode: existing
    id: vpc-09e877f9012f52241
`)
}

func rawAWSScopedImportState(serial int64, includeImportedResource bool) map[string]any {
	state := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
		rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, false),
	)
	state["serial"] = serial
	if !includeImportedResource {
		return state
	}
	resources := state["resources"].([]any)
	state["resources"] = append(resources, map[string]any{
		"mode": "managed", "type": "aws_sqs_queue", "name": "scoped_karpenter_interruption",
		"instances": []any{map[string]any{
			"index_key":  testSecondaryScopeRef,
			"attributes": map[string]any{"id": "imported-queue"},
		}},
	})
	return state
}

func TestPrepareAWSScopedResourceImports(t *testing.T) {
	scopes := awsScopedImportTestScopes()

	tests := []struct {
		name      string
		address   string
		id        string
		wantID    string
		wantError string
	}{
		{
			name:    "keeps default IAM import global",
			address: "module.cross_account_iam[0].aws_iam_role.capa_nodes",
			id:      "nodes.cluster-api-provider-aws.sigs.k8s.io",
			wantID:  "nodes.cluster-api-provider-aws.sigs.k8s.io",
		},
		{
			name:    "keeps non-default IAM import global",
			address: `module.scoped_cross_account_iam["` + testSecondaryScopeRef + `"].aws_iam_role.capa_nodes`,
			id:      "ditto-capa-nodes-" + testSecondaryScopeRef,
			wantID:  "ditto-capa-nodes-" + testSecondaryScopeRef,
		},
		{
			name:    "qualifies default regional import",
			address: "aws_ec2_instance_metadata_defaults.imdsv2[0]",
			id:      "account",
			wantID:  "account@ap-southeast-2",
		},
		{
			name:    "qualifies non-default regional import",
			address: `aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]`,
			id:      "https://sqs.us-west-2.amazonaws.com/123456789012/queue",
			wantID:  "https://sqs.us-west-2.amazonaws.com/123456789012/queue@us-west-2",
		},
		{
			name:    "keeps matching explicit Region",
			address: `aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]`,
			id:      "queue@us-west-2",
			wantID:  "queue@us-west-2",
		},
		{
			name:    "resolves Region-keyed IMDS singleton",
			address: `aws_ec2_instance_metadata_defaults.scoped_imdsv2["us-west-2"]`,
			id:      "account",
			wantID:  "account@us-west-2",
		},
		{
			name:      "rejects mismatched explicit Region",
			address:   `aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]`,
			id:        "queue@us-east-1",
			wantError: "belongs to Region \"us-west-2\"",
		},
		{
			name:      "rejects unknown scope reference",
			address:   `aws_sqs_queue.scoped_karpenter_interruption["` + testAddedScopeRef + `"]`,
			id:        "queue",
			wantError: "is not present in the scopes YAML",
		},
		{
			name:      "rejects keyed default resource address",
			address:   `module.scoped_cross_account_iam["` + testDefaultScopeRef + `"].aws_iam_role.capa_nodes`,
			id:        "role",
			wantError: "must use its legacy Terraform address",
		},
		{
			name:      "rejects scoped address without owner",
			address:   `aws_sqs_queue.scoped_karpenter_interruption["missing"]`,
			id:        "queue",
			wantError: "does not contain its owning scope reference",
		},
		{
			name:      "rejects providerless registry import",
			address:   `terraform_data.scope_registry["` + testSecondaryScopeRef + `"]`,
			id:        "registry",
			wantError: "providerless Terraform state",
		},
		{
			name:      "rejects data source import",
			address:   `data.aws_subnets.scoped_existing_private["` + testSecondaryScopeRef + `"]`,
			id:        "subnets",
			wantError: "targets a data source",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			prepared, err := prepareAWSScopedResourceImports(scopes, []resourceImport{{address: test.address, id: test.id}})
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got %v, want error containing %q", err, test.wantError)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected import preparation error: %v", err)
			}
			if len(prepared) != 1 || prepared[0].id != test.wantID || prepared[0].address != test.address {
				t.Fatalf("prepared import: got %#v, want address %q ID %q", prepared, test.address, test.wantID)
			}
		})
	}
}

func TestAWSScopedImportsUseValidatedConfigurationBackupAndNeverApply(t *testing.T) {
	fixedTime := time.Date(2026, time.July, 16, 14, 0, 0, 0, time.UTC)
	originalNow := terraformStateBackupNow
	terraformStateBackupNow = func() time.Time { return fixedTime }
	t.Cleanup(func() { terraformStateBackupNow = originalNow })

	statePath := writeTerraformStateTestFile(t, rawAWSScopedImportState(1, false))
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read scope import state: %v", err)
	}
	scopesPath := writeAWSScopedImportTestScopes(t)
	addresses := []string{
		"module.cross_account_iam[0].aws_iam_role.capa_nodes",
		`module.scoped_cross_account_iam["` + testSecondaryScopeRef + `"].aws_iam_role.capa_nodes`,
		`aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]`,
		"aws_ec2_instance_metadata_defaults.imdsv2[0]",
	}
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
		"--aws-profile=test-profile",
		"--controller-trusted-role-arns=arn:aws:iam::123456789012:role/controller",
		"--import-resource=" + addresses[0] + "=nodes.cluster-api-provider-aws.sigs.k8s.io",
		"--import-resource=" + addresses[1] + "=ditto-capa-nodes-" + testSecondaryScopeRef,
		"--import-resource=" + addresses[2] + "=https://sqs.us-west-2.amazonaws.com/123456789012/queue",
		"--import-resource=" + addresses[3] + "=account",
	})
	mock.importState = marshalTerraformStateFixture(t, rawAWSScopedImportState(2, true))

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected scope import error: %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 0)
	if mock.importCallCount != 4 {
		t.Fatalf("expected four imports, got %d", mock.importCallCount)
	}
	wantIDs := []string{
		"nodes.cluster-api-provider-aws.sigs.k8s.io",
		"ditto-capa-nodes-" + testSecondaryScopeRef,
		"https://sqs.us-west-2.amazonaws.com/123456789012/queue@us-west-2",
		"account@ap-southeast-2",
	}
	for index, call := range mock.importCalls {
		if call.address != addresses[index] || call.id != wantIDs[index] {
			t.Errorf("import %d: got address=%q ID=%q, want address=%q ID=%q", index, call.address, call.id, addresses[index], wantIDs[index])
		}
		if call.vars["deployment_scopes"] == "" || call.vars["profile"] != "test-profile" || call.vars["controller_trusted_role_arns"] == "" {
			t.Errorf("import %d omitted validated scope or account variables: %#v", index, call.vars)
		}
		if _, exists := call.vars["region"]; exists {
			t.Errorf("import %d received legacy root Region variable: %#v", index, call.vars)
		}
	}
	if mock.PlanVars["deployment_scopes"] == "" || mock.PlanVars["deployment_scopes"] != mock.importCalls[0].vars["deployment_scopes"] {
		t.Fatalf("post-import plan did not receive the exact imported scope configuration: %#v", mock.PlanVars)
	}

	backupPath := statePath + ".dittocloud-backup-20260716T140000.000000000Z"
	backupState, err := os.ReadFile(backupPath)
	if err != nil || !bytes.Equal(backupState, originalState) {
		t.Fatalf("pre-import backup is not byte-for-byte exact: err=%v", err)
	}
	manifestContent, err := os.ReadFile(backupPath + ".manifest.json")
	if err != nil {
		t.Fatalf("unable to read import backup manifest: %v", err)
	}
	var manifest terraformMigrationManifest
	if err := json.Unmarshal(manifestContent, &manifest); err != nil {
		t.Fatalf("unable to decode import backup manifest: %v", err)
	}
	if manifest.Operation != "scope-import" || !slices.Equal(manifest.ImportAddresses, addresses) || manifest.ScopeRef != "" || manifest.TargetAddress != "" {
		t.Fatalf("unexpected import backup manifest: %#v", manifest)
	}
}

func TestAWSScopedImportSecondFailureKeepsFirstImportAndOriginalBackup(t *testing.T) {
	fixedTime := time.Date(2026, time.July, 16, 14, 30, 0, 0, time.UTC)
	originalNow := terraformStateBackupNow
	terraformStateBackupNow = func() time.Time { return fixedTime }
	t.Cleanup(func() { terraformStateBackupNow = originalNow })

	statePath := writeTerraformStateTestFile(t, rawAWSScopedImportState(1, false))
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read original import state: %v", err)
	}
	firstImportedState := marshalTerraformStateFixture(t, rawAWSScopedImportState(2, true))
	scopesPath := writeAWSScopedImportTestScopes(t)
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
		`--import-resource=aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]=queue`,
		`--import-resource=module.scoped_cross_account_iam["` + testSecondaryScopeRef + `"].aws_iam_role.capa_nodes=role`,
	})
	mock.importState = firstImportedState
	mock.importReturnError = errors.New("injected second import failure")
	mock.importReturnErrorAt = 2

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "pre-import state backup remains") {
		t.Fatalf("expected retained-backup import error, got %v", err)
	}
	assertCallCounts(t, mock, 1, 0, 0)
	if mock.importCallCount != 2 {
		t.Fatalf("expected the second import to fail, got %d calls", mock.importCallCount)
	}
	persistedState, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(persistedState, firstImportedState) {
		t.Fatalf("first successful import was not persisted before the second failed: err=%v", readErr)
	}
	backupPath := statePath + ".dittocloud-backup-20260716T143000.000000000Z"
	backupState, readErr := os.ReadFile(backupPath)
	if readErr != nil || !bytes.Equal(backupState, originalState) {
		t.Fatalf("original pre-import backup was not retained exactly: err=%v", readErr)
	}
}

func TestAWSScopedImportRejectsRegistryMismatchBeforeTerraform(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	))
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + writeAWSScopedImportTestScopes(t),
		"--state=" + statePath,
		`--import-resource=aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]=queue`,
	})

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "state registry to exactly match the scopes YAML") {
		t.Fatalf("expected exact registry mismatch error, got %v", err)
	}
	assertNoTerraformLifecycleCalls(t, mock)
}

func TestAWSScopedImportBackupFailureStopsBeforeImport(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawAWSScopedImportState(1, false))
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read original import state: %v", err)
	}
	originalSync := syncStateBackupDirectory
	syncStateBackupDirectory = func(directoryPath string) error { return errors.New("injected import backup sync failure") }
	t.Cleanup(func() { syncStateBackupDirectory = originalSync })

	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + writeAWSScopedImportTestScopes(t),
		"--state=" + statePath,
		`--import-resource=aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]=queue`,
	})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "unable to create scope-mode import backup") {
		t.Fatalf("expected pre-import backup failure, got %v", err)
	}
	assertCallCounts(t, mock, 1, 0, 0)
	if mock.importCallCount != 0 {
		t.Fatalf("backup failure ran %d imports", mock.importCallCount)
	}
	persistedState, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(persistedState, originalState) {
		t.Fatalf("backup failure changed canonical state: err=%v", readErr)
	}
}

func TestAWSScopedImportRejectsImportedStateWithoutRegistry(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawAWSScopedImportState(1, false))
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read original import state: %v", err)
	}
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + writeAWSScopedImportTestScopes(t),
		"--state=" + statePath,
		`--import-resource=aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]=queue`,
	})
	mock.importState = marshalTerraformStateFixture(t, rawTerraformStateWithResources([]any{
		map[string]any{
			"mode": "managed", "type": "aws_sqs_queue", "name": "scoped_karpenter_interruption",
			"instances": []any{map[string]any{"index_key": testSecondaryScopeRef, "attributes": map[string]any{"id": "queue"}}},
		},
	}))

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "produced invalid scope registry state") {
		t.Fatalf("expected imported-state registry validation error, got %v", err)
	}
	assertCallCounts(t, mock, 1, 0, 0)
	if mock.importCallCount != 1 {
		t.Fatalf("expected one import, got %d", mock.importCallCount)
	}
	persistedState, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(persistedState, originalState) {
		t.Fatalf("invalid imported state replaced canonical state: err=%v", readErr)
	}
}

func TestAWSScopedImportPersistenceFailureRetainsBackupAndCanonicalState(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawAWSScopedImportState(1, false))
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read original import state: %v", err)
	}
	originalReplace := replaceStateFile
	replaceStateFile = func(sourcePath, destinationPath string) error {
		return errors.New("injected import persistence failure")
	}
	t.Cleanup(func() { replaceStateFile = originalReplace })

	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + writeAWSScopedImportTestScopes(t),
		"--state=" + statePath,
		`--import-resource=aws_sqs_queue.scoped_karpenter_interruption["` + testSecondaryScopeRef + `"]=queue`,
	})
	mock.importState = marshalTerraformStateFixture(t, rawAWSScopedImportState(2, true))

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "pre-import state backup remains") || !strings.Contains(err.Error(), "recovery state retained") {
		t.Fatalf("expected scoped import persistence recovery error, got %v", err)
	}
	assertCallCounts(t, mock, 1, 0, 0)
	if mock.importCallCount != 1 {
		t.Fatalf("expected one import, got %d", mock.importCallCount)
	}
	persistedState, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(persistedState, originalState) {
		t.Fatalf("failed import persistence changed canonical state: err=%v", readErr)
	}
}
