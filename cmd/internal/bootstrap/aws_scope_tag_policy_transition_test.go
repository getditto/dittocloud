package bootstrap

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeAWSScopeTagPolicyTransitionFixture(
	t *testing.T,
	appliedScope AWSDeploymentScope,
	desiredScope AWSDeploymentScope,
) (string, string) {
	t.Helper()
	statePath := writeTerraformStateTestFile(t, rawTagVerificationState(appliedScope))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	encoded, err := encodeAWSDeploymentScopesDocument(
		scopesPath,
		AWSDeploymentScopes{testDefaultScopeRef: desiredScope},
	)
	if err != nil {
		t.Fatalf("unable to encode scopes fixture: %v", err)
	}
	if err := os.WriteFile(scopesPath, encoded, 0600); err != nil {
		t.Fatalf("unable to write scopes fixture: %v", err)
	}
	return statePath, scopesPath
}

func executeAWSScopeTagPolicyTransition(
	t *testing.T,
	statePath string,
	scopesPath string,
	dryRun bool,
) (*mockTerraformExecutor, error) {
	t.Helper()
	args := []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
		"--aws-profile=test-profile",
		"--no-color",
	}
	if dryRun {
		args = append(args, "--dry-run")
	}
	cmd, terraformMock := setupBootstrapTest(t, args)
	return terraformMock, cmd.Execute()
}

func TestAWSScopeTagPolicyTransitionVerifiesBeforePlanAndApply(t *testing.T) {
	appliedScope := testTagVerificationScope(awsClusterTypeKubeadm, "")
	desiredScope := appliedScope
	desiredScope.ClusterName = "iam-test-timc"
	desiredScope.ScopeTagPolicyVersion = 1
	statePath, scopesPath := writeAWSScopeTagPolicyTransitionFixture(t, appliedScope, desiredScope)
	mockVerifier := &mockAWSScopeTagVerifier{}
	setMockAWSScopeTagVerifier(t, mockVerifier)
	originalPrompt := terraformApplyPrompt
	terraformApplyPrompt = func(label, defaultValue string) string { return "yes" }
	t.Cleanup(func() { terraformApplyPrompt = originalPrompt })

	terraformMock, err := executeAWSScopeTagPolicyTransition(t, statePath, scopesPath, false)
	if err != nil {
		t.Fatalf("unexpected version-1 transition error: %v", err)
	}
	assertCallCounts(t, terraformMock, 1, 1, 1)
	if mockVerifier.calls != 2 {
		t.Fatalf("AWS verifier calls: got %d, want pre-plan and pre-apply calls", mockVerifier.calls)
	}
	if got := terraformMock.PlanVars["scope_tag_policy_cli_authorized_refs"]; got != `["`+testDefaultScopeRef+`"]` {
		t.Fatalf("Terraform CLI authorization: got %q", got)
	}
	if !strings.Contains(terraformMock.PlanVars["deployment_scopes"], `"scope_tag_policy_version":1`) {
		t.Fatalf("Terraform deployment_scopes did not contain the requested version-1 transition: %s", terraformMock.PlanVars["deployment_scopes"])
	}
}

func TestAWSScopeTagPolicyTransitionDryRunVerifiesOnce(t *testing.T) {
	appliedScope := testTagVerificationScope(awsClusterTypeEKS, "")
	desiredScope := appliedScope
	desiredScope.ClusterName = "iam-test-eks"
	desiredScope.ScopeTagPolicyVersion = 1
	statePath, scopesPath := writeAWSScopeTagPolicyTransitionFixture(t, appliedScope, desiredScope)
	mockVerifier := &mockAWSScopeTagVerifier{}
	setMockAWSScopeTagVerifier(t, mockVerifier)

	terraformMock, err := executeAWSScopeTagPolicyTransition(t, statePath, scopesPath, true)
	if err != nil {
		t.Fatalf("unexpected dry-run transition error: %v", err)
	}
	assertCallCounts(t, terraformMock, 1, 1, 0)
	if mockVerifier.calls != 1 {
		t.Fatalf("AWS verifier calls: got %d, want one pre-plan call", mockVerifier.calls)
	}
}

func TestAWSScopeTagPolicyTransitionFailsBeforeTerraformPlan(t *testing.T) {
	appliedScope := testTagVerificationScope(awsClusterTypeKubeadm, "")
	desiredScope := appliedScope
	desiredScope.ClusterName = "iam-test-timc"
	desiredScope.ScopeTagPolicyVersion = 1
	statePath, scopesPath := writeAWSScopeTagPolicyTransitionFixture(t, appliedScope, desiredScope)
	mockVerifier := &mockAWSScopeTagVerifier{err: errors.New("injected pre-plan verification failure")}
	setMockAWSScopeTagVerifier(t, mockVerifier)

	terraformMock, err := executeAWSScopeTagPolicyTransition(t, statePath, scopesPath, true)
	if err == nil || !strings.Contains(err.Error(), "version-1 tag verification before Terraform plan failed") {
		t.Fatalf("got %v, want pre-plan verification failure", err)
	}
	assertCallCounts(t, terraformMock, 0, 0, 0)
	if mockVerifier.calls != 1 {
		t.Fatalf("AWS verifier calls: got %d, want 1", mockVerifier.calls)
	}
}

func TestAWSScopeTagPolicyTransitionFailsSecondVerificationBeforeApply(t *testing.T) {
	appliedScope := testTagVerificationScope(awsClusterTypeKubeadm, "")
	desiredScope := appliedScope
	desiredScope.ClusterName = "iam-test-timc"
	desiredScope.ScopeTagPolicyVersion = 1
	statePath, scopesPath := writeAWSScopeTagPolicyTransitionFixture(t, appliedScope, desiredScope)
	mockVerifier := &mockAWSScopeTagVerifier{
		err:   errors.New("injected pre-apply verification failure"),
		errAt: 2,
	}
	setMockAWSScopeTagVerifier(t, mockVerifier)
	originalPrompt := terraformApplyPrompt
	terraformApplyPrompt = func(label, defaultValue string) string { return "yes" }
	t.Cleanup(func() { terraformApplyPrompt = originalPrompt })

	terraformMock, err := executeAWSScopeTagPolicyTransition(t, statePath, scopesPath, false)
	if err == nil || !strings.Contains(err.Error(), "version-1 tag verification immediately before Terraform apply failed") {
		t.Fatalf("got %v, want pre-apply verification failure", err)
	}
	assertCallCounts(t, terraformMock, 1, 1, 0)
	if mockVerifier.calls != 2 {
		t.Fatalf("AWS verifier calls: got %d, want 2", mockVerifier.calls)
	}
}

func TestAWSScopeTagPolicyAppliedVersionOneDoesNotReverifyUnchangedCluster(t *testing.T) {
	appliedScope := testTagVerificationScope(awsClusterTypeKubeadm, "iam-test-timc")
	appliedScope.ScopeTagPolicyVersion = 1
	statePath, scopesPath := writeAWSScopeTagPolicyTransitionFixture(t, appliedScope, appliedScope)
	factoryCalls := 0
	originalFactory := newAWSScopeTagVerifier
	newAWSScopeTagVerifier = func(context.Context, string, string) (awsScopeTagVerifier, error) {
		factoryCalls++
		return &mockAWSScopeTagVerifier{}, nil
	}
	t.Cleanup(func() { newAWSScopeTagVerifier = originalFactory })

	terraformMock, err := executeAWSScopeTagPolicyTransition(t, statePath, scopesPath, true)
	if err != nil {
		t.Fatalf("unexpected existing version-1 dry-run error: %v", err)
	}
	assertCallCounts(t, terraformMock, 1, 1, 0)
	if factoryCalls != 0 {
		t.Fatalf("unchanged applied version 1 initialized verifier %d time(s)", factoryCalls)
	}
	if got := terraformMock.PlanVars["scope_tag_policy_cli_authorized_refs"]; got != `["`+testDefaultScopeRef+`"]` {
		t.Fatalf("existing version-1 Terraform CLI authorization: got %q", got)
	}
}

func TestAWSScopeTagPolicyTransitionRejectsDowngradeAndClusterRename(t *testing.T) {
	appliedScope := testTagVerificationScope(awsClusterTypeKubeadm, "iam-test-timc")
	appliedScope.ScopeTagPolicyVersion = 1
	tests := []struct {
		name      string
		desired   AWSDeploymentScope
		wantError string
	}{
		{
			name: "downgrade",
			desired: func() AWSDeploymentScope {
				scope := appliedScope
				scope.ScopeTagPolicyVersion = 0
				return scope
			}(),
			wantError: "cannot downgrade scopeTagPolicyVersion",
		},
		{
			name: "cluster rename",
			desired: func() AWSDeploymentScope {
				scope := appliedScope
				scope.ClusterName = "other-cluster"
				return scope
			}(),
			wantError: "clusterName is immutable",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statePath, scopesPath := writeAWSScopeTagPolicyTransitionFixture(t, appliedScope, test.desired)
			terraformMock, err := executeAWSScopeTagPolicyTransition(t, statePath, scopesPath, true)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
			assertCallCounts(t, terraformMock, 0, 0, 0)
		})
	}
}

func TestDetectAWSLegacyVersionZeroClusterPolicyRefs(t *testing.T) {
	desiredScope := testTagVerificationScope(awsClusterTypeKubeadm, "legacy-cluster")
	desiredScopes := AWSDeploymentScopes{testDefaultScopeRef: desiredScope}

	t.Run("preserves matching legacy phase two policy", func(t *testing.T) {
		statePath := writeTerraformStateTestFile(t, legacyStateFixture(
			map[string]any{},
			rawAWSLegacyPhaseTwoPolicyResource("capa_controller_base", "legacy-cluster"),
		))
		refs, err := detectAWSLegacyVersionZeroClusterPolicyRefs(statePath, desiredScopes)
		if err != nil {
			t.Fatalf("unexpected detection error: %v", err)
		}
		if len(refs) != 1 || refs[0] != testDefaultScopeRef {
			t.Fatalf("preserved refs: got %v", refs)
		}
	})

	t.Run("ordinary named version zero scope stays broad", func(t *testing.T) {
		statePath := writeTerraformStateTestFile(t, legacyStateFixture(map[string]any{}))
		refs, err := detectAWSLegacyVersionZeroClusterPolicyRefs(statePath, desiredScopes)
		if err != nil {
			t.Fatalf("unexpected detection error: %v", err)
		}
		if len(refs) != 0 {
			t.Fatalf("ordinary version-zero scope preserved unexpected refs: %v", refs)
		}
	})

	t.Run("conflicting legacy phase two policy fails closed", func(t *testing.T) {
		statePath := writeTerraformStateTestFile(t, legacyStateFixture(
			map[string]any{},
			rawAWSLegacyPhaseTwoPolicyResource("capa_controller_base", "other-cluster"),
		))
		_, err := detectAWSLegacyVersionZeroClusterPolicyRefs(statePath, desiredScopes)
		if err == nil || !strings.Contains(err.Error(), "conflicts with the existing legacy phase-two IAM cluster") {
			t.Fatalf("got %v, want legacy cluster conflict", err)
		}
	})
}
