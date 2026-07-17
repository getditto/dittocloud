package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tfjson "github.com/hashicorp/terraform-json"
)

func rawAWSRegistrySeedLegacyState() map[string]any {
	return legacyStateFixture(
		map[string]any{"aws": rawAWSLegacyOutput("ap-southeast-2")},
		rawAWSLegacyVPCValidationResource(false, nil),
		map[string]any{
			"module": "module.cross_account_iam[0]", "mode": "managed", "type": "aws_iam_role", "name": "capa_control_plane",
			"instances": []any{map[string]any{"attributes": map[string]any{"name": "control-plane.cluster-api-provider-aws.sigs.k8s.io"}}},
		},
	)
}

func rawAWSRegistrySeedAppliedState() map[string]any {
	state := rawAWSRegistrySeedLegacyState()
	registryState := rawScopeRegistryState(rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true))
	state["serial"] = int64(2)
	state["resources"] = append(state["resources"].([]any), registryState["resources"].([]any)...)
	return state
}

func marshalTerraformStateFixture(t *testing.T, state map[string]any) []byte {
	t.Helper()
	content, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("unable to encode Terraform state fixture: %v", err)
	}
	return content
}

func writeAWSRegistrySeedScopesFile(t *testing.T, extra string) string {
	t.Helper()
	return writeAWSScopeTestFile(t, testDefaultScopeRef+`:
  default: true
  clusterType: kubeadm
  region: ap-southeast-2
  scopeTagPolicyVersion: 0
  vpc:
    mode: capi
`+extra)
}

func exactAWSRegistrySeedPlan(scopeRef string) *tfjson.Plan {
	target := `terraform_data.scope_registry["` + scopeRef + `"]`
	return &tfjson.Plan{
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address:      target,
				Mode:         tfjson.ManagedResourceMode,
				Type:         "terraform_data",
				Name:         "scope_registry",
				ProviderName: terraformBuiltinProviderName,
				Index:        scopeRef,
				Change:       &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}},
			},
		},
	}
}

func forceAWSRegistrySeedConfirmation(t *testing.T, interactive, confirm bool) {
	t.Helper()
	originalInteractive := awsScopeRegistrySeedInputIsInteractive
	originalConfirm := awsScopeRegistrySeedConfirm
	awsScopeRegistrySeedInputIsInteractive = func() bool { return interactive }
	awsScopeRegistrySeedConfirm = func() bool { return confirm }
	t.Cleanup(func() {
		awsScopeRegistrySeedInputIsInteractive = originalInteractive
		awsScopeRegistrySeedConfirm = originalConfirm
	})
}

func registrySeedCommandArgs(statePath, scopesPath string, additional ...string) []string {
	args := []string{
		"aws", "scopes", "migrate", "seed-registry",
		"--state=" + statePath,
		"--scopes-file=" + scopesPath,
	}
	return append(args, additional...)
}

func TestAWSScopesSeedRegistryAppliesOnlyReviewedPlanAndBacksUpState(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, true, true)
	fixedTime := time.Date(2026, time.July, 16, 13, 0, 0, 0, time.UTC)
	originalNow := terraformStateBackupNow
	terraformStateBackupNow = func() time.Time { return fixedTime }
	t.Cleanup(func() { terraformStateBackupNow = originalNow })

	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read legacy state: %v", err)
	}
	scopesPath := writeAWSRegistrySeedScopesFile(t, "")
	originalScopes, err := os.ReadFile(scopesPath)
	if err != nil {
		t.Fatalf("unable to read scopes fixture: %v", err)
	}
	cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
	mock.showPlanReturn = exactAWSRegistrySeedPlan(testDefaultScopeRef)
	mock.applyState = marshalTerraformStateFixture(t, rawAWSRegistrySeedAppliedState())
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected registry-seed error: %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 1)
	if mock.showPlanCallCount != 1 {
		t.Fatalf("expected one ShowPlanFile call, got %d", mock.showPlanCallCount)
	}
	if mock.showPlanStdout != io.Discard {
		t.Fatal("registry-seed plan JSON was not suppressed during internal validation")
	}
	if mock.applyStdout != io.Discard {
		t.Fatal("registry-seed apply output was not suppressed")
	}
	if mock.stdout != &output {
		t.Fatal("registry-seed command output was not restored after apply")
	}
	expectedTarget := `terraform_data.scope_registry["` + testDefaultScopeRef + `"]`
	if !slices.Equal(mock.planTargets, []string{expectedTarget}) {
		t.Fatalf("plan targets: got %v, want only %q", mock.planTargets, expectedTarget)
	}
	if !strings.HasSuffix(mock.planOutPath, "scope-registry-seed.tfplan") || mock.applyPlanPath != mock.planOutPath {
		t.Fatalf("saved plan was not applied exactly: planned=%q applied=%q", mock.planOutPath, mock.applyPlanPath)
	}
	if _, exists := mock.PlanVars["deployment_scopes"]; !exists || len(mock.PlanVars) != 1 {
		t.Fatalf("unexpected registry-seed variables: %#v", mock.PlanVars)
	}
	if err := validateAppliedAWSRegistrySeedState(statePath, testDefaultScopeRef, originalState); err != nil {
		t.Fatalf("persisted state is not the expected registry seed: %v", err)
	}
	scopesAfter, err := os.ReadFile(scopesPath)
	if err != nil || !bytes.Equal(scopesAfter, originalScopes) {
		t.Fatalf("registry seed changed scopes YAML: err=%v", err)
	}
	backupPath := statePath + ".dittocloud-backup-20260716T130000.000000000Z"
	backupState, err := os.ReadFile(backupPath)
	if err != nil {
		t.Fatalf("unable to read expected state backup: %v", err)
	}
	if !bytes.Equal(backupState, originalState) {
		t.Fatal("pre-migration state backup does not match the original state")
	}
	if _, err := os.Stat(backupPath + ".manifest.json"); err != nil {
		t.Fatalf("migration manifest is missing: %v", err)
	}
	if !strings.Contains(output.String(), "Validated registry-seed plan") || !strings.Contains(output.String(), "Review a separate untargeted scope-mode plan") {
		t.Fatalf("registry-seed output omitted safety guidance:\n%s", output.String())
	}
}

func TestAWSScopesSeedRegistryDryRunDoesNotBackUpOrApply(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, false, false)
	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	scopesPath := writeAWSRegistrySeedScopesFile(t, "")
	cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath, "--dry-run"))
	mock.showPlanReturn = exactAWSRegistrySeedPlan(testDefaultScopeRef)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected dry-run error: %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 0)
	if mock.showPlanCallCount != 1 {
		t.Fatalf("expected one ShowPlanFile call, got %d", mock.showPlanCallCount)
	}
	if mock.showPlanStdout != io.Discard {
		t.Fatal("registry-seed dry-run plan JSON was not suppressed during internal validation")
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(stateAfter, originalState) {
		t.Fatalf("dry run changed state: err=%v", err)
	}
	matches, err := filepath.Glob(statePath + ".dittocloud-backup-*")
	if err != nil || len(matches) != 0 {
		t.Fatalf("dry run created migration backup artifacts: %v err=%v", matches, err)
	}
}

func TestAWSScopesSeedRegistryRejectsUnexpectedPlanActions(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, true, true)
	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	scopesPath := writeAWSRegistrySeedScopesFile(t, "")
	cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
	plan := exactAWSRegistrySeedPlan(testDefaultScopeRef)
	plan.ResourceChanges = append(plan.ResourceChanges, &tfjson.ResourceChange{
		Address:      "aws_iam_role.unexpected",
		Mode:         tfjson.ManagedResourceMode,
		Type:         "aws_iam_role",
		Name:         "unexpected",
		ProviderName: "registry.terraform.io/hashicorp/aws",
		Change:       &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionUpdate}},
	})
	mock.showPlanReturn = plan

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "exactly one resource action; found 2") {
		t.Fatalf("expected exact-plan rejection, got %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 0)
	stateAfter, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(stateAfter, originalState) {
		t.Fatalf("rejected plan changed state: err=%v", readErr)
	}
	matches, globErr := filepath.Glob(statePath + ".dittocloud-backup-*")
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("rejected plan created backup artifacts: %v err=%v", matches, globErr)
	}
}

func TestAWSScopesSeedRegistryRequiresInteractiveApplyConfirmation(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, false, false)
	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	scopesPath := writeAWSRegistrySeedScopesFile(t, "")
	cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
	mock.showPlanReturn = exactAWSRegistrySeedPlan(testDefaultScopeRef)

	err := cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "requires an interactive confirmation") {
		t.Fatalf("expected interactive confirmation error, got %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 0)
}

func TestAWSScopesSeedRegistryCancellationDoesNotCreateBackup(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, true, false)
	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	scopesPath := writeAWSRegistrySeedScopesFile(t, "")
	cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
	mock.showPlanReturn = exactAWSRegistrySeedPlan(testDefaultScopeRef)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected cancellation error: %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 0)
	matches, err := filepath.Glob(statePath + ".dittocloud-backup-*")
	if err != nil || len(matches) != 0 {
		t.Fatalf("cancelled seed created backup artifacts: %v err=%v", matches, err)
	}
}

func TestAWSScopesSeedRegistryPersistsValidPartialStateAfterApplyFailure(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, true, true)
	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read legacy state: %v", err)
	}
	scopesPath := writeAWSRegistrySeedScopesFile(t, "")
	cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
	mock.showPlanReturn = exactAWSRegistrySeedPlan(testDefaultScopeRef)
	mock.applyState = marshalTerraformStateFixture(t, rawAWSRegistrySeedAppliedState())
	mock.applyReturnError = errors.New("injected apply failure")

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "valid partial state was saved") {
		t.Fatalf("expected persisted partial-state error, got %v", err)
	}
	if err := validateAppliedAWSRegistrySeedState(statePath, testDefaultScopeRef, originalState); err != nil {
		t.Fatalf("valid partial registry state was not persisted: %v", err)
	}
}

func TestAWSScopesSeedRegistryRejectsPartialStateThatLostLegacyResources(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, true, true)
	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read legacy state: %v", err)
	}
	scopesPath := writeAWSRegistrySeedScopesFile(t, "")
	cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
	mock.showPlanReturn = exactAWSRegistrySeedPlan(testDefaultScopeRef)
	invalidPartialState := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	)
	invalidPartialState["serial"] = int64(2)
	invalidPartialState["outputs"] = rawAWSRegistrySeedLegacyState()["outputs"]
	mock.applyState = marshalTerraformStateFixture(t, invalidPartialState)
	mock.applyReturnError = errors.New("injected apply failure")

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "changed non-registry resources") || !strings.Contains(err.Error(), "temporary state retained at") {
		t.Fatalf("expected rejected partial-state error with recovery path, got %v", err)
	}
	stateAfter, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(stateAfter, originalState) {
		t.Fatalf("invalid partial state replaced the selected state: err=%v", readErr)
	}
}

func TestAWSScopesSeedRegistryBackupFailureStopsApply(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, true, true)
	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read legacy state: %v", err)
	}
	scopesPath := writeAWSRegistrySeedScopesFile(t, "")
	originalSync := syncStateBackupDirectory
	syncStateBackupDirectory = func(directoryPath string) error { return errors.New("injected backup sync failure") }
	t.Cleanup(func() { syncStateBackupDirectory = originalSync })
	cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
	mock.showPlanReturn = exactAWSRegistrySeedPlan(testDefaultScopeRef)
	mock.applyState = marshalTerraformStateFixture(t, rawAWSRegistrySeedAppliedState())

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "directory could not be flushed") {
		t.Fatalf("expected backup failure, got %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 0)
	stateAfter, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(stateAfter, originalState) {
		t.Fatalf("backup failure changed state: err=%v", readErr)
	}
}

func TestAWSScopesSeedRegistryLockOrdering(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, true, true)
	t.Run("state lock contention happens before scopes-file locking", func(t *testing.T) {
		statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
		scopesPath := writeAWSRegistrySeedScopesFile(t, "")
		operationLock, err := acquireStateOperationLock(statePath, "test owner")
		if err != nil {
			t.Fatalf("unable to acquire state lock: %v", err)
		}
		defer func() { _ = operationLock.Release() }()

		cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
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
		statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
		scopesPath := writeAWSRegistrySeedScopesFile(t, "")
		fileLock, err := acquireScopesFileLock(scopesPath, "test owner")
		if err != nil {
			t.Fatalf("unable to acquire scopes-file lock: %v", err)
		}
		defer func() { _ = fileLock.Release() }()

		cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(statePath, scopesPath))
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

func TestValidateAWSRegistrySeedPlanRejectsUnsafeMetadata(t *testing.T) {
	target := `terraform_data.scope_registry["` + testDefaultScopeRef + `"]`
	tests := []struct {
		name      string
		mutate    func(*tfjson.Plan)
		wantError string
	}{
		{
			name: "wrong provider",
			mutate: func(plan *tfjson.Plan) {
				plan.ResourceChanges[0].ProviderName = "registry.terraform.io/hashicorp/aws"
			},
			wantError: "not the exact built-in create",
		},
		{
			name: "import action",
			mutate: func(plan *tfjson.Plan) {
				plan.ResourceChanges[0].Change.Importing = &tfjson.Importing{ID: "existing"}
			},
			wantError: "not the exact built-in create",
		},
		{
			name: "resource drift",
			mutate: func(plan *tfjson.Plan) {
				plan.ResourceDrift = []*tfjson.ResourceChange{{
					Address: "aws_vpc.legacy",
					Change:  &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionUpdate}},
				}}
			},
			wantError: "contains resource drift",
		},
		{
			name: "deferred action",
			mutate: func(plan *tfjson.Plan) {
				plan.DeferredChanges = []*tfjson.DeferredResourceChange{{}}
			},
			wantError: "contains deferred resource changes",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := exactAWSRegistrySeedPlan(testDefaultScopeRef)
			test.mutate(plan)
			err := validateAWSRegistrySeedPlan(plan, target, testDefaultScopeRef)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestAWSScopesSeedRegistryPreflightGuards(t *testing.T) {
	forceAWSRegistrySeedConfirmation(t, true, true)
	legacyStatePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedLegacyState())
	registryStatePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedAppliedState())
	validScopesPath := writeAWSRegistrySeedScopesFile(t, "")
	twoScopesPath := writeAWSScopeTestFile(t, testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`+testSecondaryScopeRef+`:
  region: us-west-2
  vpc:
    mode: capi
`)
	mismatchedScopesPath := writeAWSScopeTestFile(t, testDefaultScopeRef+`:
  default: true
  clusterType: kubeadm
  region: us-west-2
  scopeTagPolicyVersion: 0
  vpc:
    mode: capi
`)
	clusterNameScopesPath := writeAWSScopeTestFile(t, testDefaultScopeRef+`:
  default: true
  clusterName: not-in-state
  clusterType: kubeadm
  region: ap-southeast-2
  scopeTagPolicyVersion: 0
  vpc:
    mode: capi
`)
	tagPolicyScopesPath := writeAWSScopeTestFile(t, testDefaultScopeRef+`:
  default: true
  clusterType: kubeadm
  region: ap-southeast-2
  scopeTagPolicyVersion: 1
  vpc:
    mode: capi
`)

	tests := []struct {
		name       string
		statePath  string
		scopesPath string
		extraArgs  []string
		wantError  string
	}{
		{name: "requires default-only YAML", statePath: legacyStatePath, scopesPath: twoScopesPath, wantError: "exactly one default scope"},
		{name: "rejects an existing registry", statePath: registryStatePath, scopesPath: validScopesPath, wantError: "already contains a scope registry"},
		{name: "rejects evidence mismatch", statePath: legacyStatePath, scopesPath: mismatchedScopesPath, wantError: "conflicts with state evidence"},
		{name: "rejects unsupported cluster name", statePath: legacyStatePath, scopesPath: clusterNameScopesPath, wantError: "must omit clusterName"},
		{name: "requires policy version zero", statePath: legacyStatePath, scopesPath: tagPolicyScopesPath, wantError: "scopeTagPolicyVersion: 0"},
		{name: "rejects import flags", statePath: legacyStatePath, scopesPath: validScopesPath, extraArgs: []string{"--import-resource=example=id"}, wantError: "cannot be used"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd, mock := setupBootstrapTest(t, registrySeedCommandArgs(test.statePath, test.scopesPath, test.extraArgs...))
			err := cmd.Execute()
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
			assertNoTerraformLifecycleCalls(t, mock)
		})
	}
}
