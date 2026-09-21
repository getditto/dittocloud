package bootstrap

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type mockAWSScopeTagVerifier struct {
	request awsScopeTagVerificationRequest
	report  awsScopeTagVerificationReport
	err     error
	errAt   int
	calls   int
}

func (mock *mockAWSScopeTagVerifier) Verify(
	_ context.Context,
	request awsScopeTagVerificationRequest,
) (awsScopeTagVerificationReport, error) {
	mock.calls++
	mock.request = request
	if mock.err != nil && (mock.errAt == 0 || mock.errAt == mock.calls) {
		return awsScopeTagVerificationReport{}, mock.err
	}
	return mock.report, nil
}

func setMockAWSScopeTagVerifier(t *testing.T, mock *mockAWSScopeTagVerifier) {
	t.Helper()
	originalFactory := newAWSScopeTagVerifier
	newAWSScopeTagVerifier = func(_ context.Context, profile, region string) (awsScopeTagVerifier, error) {
		if profile != "test-profile" {
			t.Fatalf("AWS profile: got %q, want test-profile", profile)
		}
		if region != "ap-southeast-2" {
			t.Fatalf("AWS Region: got %q, want ap-southeast-2", region)
		}
		return mock, nil
	}
	t.Cleanup(func() { newAWSScopeTagVerifier = originalFactory })
}

func testTagVerificationScope(clusterType, clusterName string) AWSDeploymentScope {
	return AWSDeploymentScope{
		Default:               true,
		ClusterName:           clusterName,
		ClusterType:           clusterType,
		Region:                "ap-southeast-2",
		ScopeTagPolicyVersion: 0,
		VPC: AWSScopeVPC{
			Mode:                 awsVPCModeDittocloud,
			Name:                 "ditto-k8s",
			CIDR:                 "10.210.0.0/16",
			PublicSubnetNetmask:  awsLegacyPublicSubnetNetmask,
			PrivateSubnetNetmask: awsLegacyPrivateSubnetNetmask,
			NATGatewayName:       "iam-test-timc-nat",
		},
	}
}

func rawScopeTaggedEC2Resource(scopeRef string) map[string]any {
	return map[string]any{
		"module": "module.vpc[0].module.vpc",
		"mode":   "managed",
		"type":   "aws_vpc",
		"name":   "this",
		"instances": []any{map[string]any{
			"schema_version": 1,
			"attributes": map[string]any{
				"id":       "vpc-0123456789abcdef0",
				"arn":      "arn:aws:ec2:ap-southeast-2:123456789012:vpc/vpc-0123456789abcdef0",
				"region":   "ap-southeast-2",
				"tags":     map[string]any{awsScopeIdentityTagKey: scopeRef},
				"tags_all": map[string]any{awsScopeIdentityTagKey: scopeRef},
			},
		}},
	}
}

func rawTagVerificationState(scope AWSDeploymentScope) map[string]any {
	state := rawRecoverableScopeState(AWSDeploymentScopes{testDefaultScopeRef: scope})
	appendRawTerraformStateResource(state, rawScopeTaggedEC2Resource(testDefaultScopeRef))
	return state
}

func executeAWSScopesTagsVerifyTest(
	t *testing.T,
	statePath string,
	scopesPath string,
	extraArgs ...string,
) (*mockTerraformExecutor, string, error) {
	t.Helper()
	args := []string{
		"aws", "scopes", "tags", "verify",
		"--state=" + statePath,
		"--scopes-file=" + scopesPath,
		"--scope-ref=" + testDefaultScopeRef,
		"--aws-profile=test-profile",
	}
	args = append(args, extraArgs...)
	cmd, terraformMock := setupBootstrapTest(t, args)
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)
	err := cmd.Execute()
	return terraformMock, output.String(), err
}

func TestAWSScopesTagsVerifyReadOnlyKubeadmReport(t *testing.T) {
	scope := testTagVerificationScope(awsClusterTypeKubeadm, "iam-test-timc")
	statePath := writeTerraformStateTestFile(t, rawTagVerificationState(scope))
	scopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  clusterType: kubeadm
  region: ap-southeast-2
  scopeTagPolicyVersion: 0
  vpc:
    mode: dittocloud
    name: ditto-k8s
    cidr: 10.210.0.0/16
    publicSubnetNetmask: 22
    privateSubnetNetmask: 18
    natGatewayName: iam-test-timc-nat
`)
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	scopesBefore, err := os.ReadFile(scopesPath)
	if err != nil {
		t.Fatalf("unable to read scopes fixture: %v", err)
	}
	mockVerifier := &mockAWSScopeTagVerifier{report: awsScopeTagVerificationReport{
		ScopeRef:            testDefaultScopeRef,
		ClusterName:         "iam-test-timc",
		ClusterType:         awsClusterTypeKubeadm,
		Region:              "ap-southeast-2",
		StateResources:      []awsScopeTagVerifiedResource{{Identity: "module.vpc[0].module.vpc.aws_vpc.this", Type: "aws_vpc"}},
		ClusterResources:    []awsScopeTagVerifiedResource{{Identity: "arn:aws:ec2:ap-southeast-2:123456789012:instance/i-123", Type: "ec2"}},
		NativeDiscoveryKeys: []string{"sigs.k8s.io/cluster-api-provider-aws/cluster/iam-test-timc"},
	}}
	setMockAWSScopeTagVerifier(t, mockVerifier)

	terraformMock, output, err := executeAWSScopesTagsVerifyTest(t, statePath, scopesPath, "--cluster-name=iam-test-timc")
	if err != nil {
		t.Fatalf("unexpected tag-verification error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, terraformMock)
	if mockVerifier.calls != 1 {
		t.Fatalf("AWS verifier calls: got %d, want 1", mockVerifier.calls)
	}
	if mockVerifier.request.ScopeRef != testDefaultScopeRef || mockVerifier.request.Scope.ClusterName != "iam-test-timc" {
		t.Fatalf("unexpected verification request: %#v", mockVerifier.request)
	}
	if len(mockVerifier.request.State) != 1 || mockVerifier.request.State[0].Identifier != "vpc-0123456789abcdef0" {
		t.Fatalf("unexpected state-backed inventory: %#v", mockVerifier.request.State)
	}
	for _, want := range []string{
		"AWS scope tag verification passed",
		"cluster=iam-test-timc",
		"Dittocloud-managed resources: 1",
		"Cluster-native resources: 1",
		"does not enable version 1",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("verification output does not contain %q:\n%s", want, output)
		}
	}
	stateAfter, _ := os.ReadFile(statePath)
	scopesAfter, _ := os.ReadFile(scopesPath)
	if !bytes.Equal(stateBefore, stateAfter) || !bytes.Equal(scopesBefore, scopesAfter) {
		t.Fatal("read-only tag verification modified state or scopes YAML")
	}
}

func TestAWSScopesTagsVerifyPassesEKSIdentityToBuiltInVerifier(t *testing.T) {
	scope := testTagVerificationScope(awsClusterTypeEKS, "iam-test-eks")
	statePath := writeTerraformStateTestFile(t, rawTagVerificationState(scope))
	scopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  clusterName: iam-test-eks
  clusterType: eks
  region: ap-southeast-2
  scopeTagPolicyVersion: 0
  vpc:
    mode: dittocloud
    name: ditto-k8s
    cidr: 10.210.0.0/16
    publicSubnetNetmask: 22
    privateSubnetNetmask: 18
    natGatewayName: iam-test-timc-nat
`)
	mockVerifier := &mockAWSScopeTagVerifier{report: awsScopeTagVerificationReport{
		ScopeRef:         testDefaultScopeRef,
		ClusterName:      "iam-test-eks",
		ClusterType:      awsClusterTypeEKS,
		Region:           "ap-southeast-2",
		StateResources:   []awsScopeTagVerifiedResource{{Identity: "state", Type: "aws_vpc"}},
		ClusterResources: []awsScopeTagVerifiedResource{{Identity: "arn:aws:eks:ap-southeast-2:123456789012:cluster/iam-test-eks", Type: "eks"}},
	}}
	setMockAWSScopeTagVerifier(t, mockVerifier)

	terraformMock, _, err := executeAWSScopesTagsVerifyTest(t, statePath, scopesPath)
	if err != nil {
		t.Fatalf("unexpected EKS tag-verification error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, terraformMock)
	if mockVerifier.request.Scope.ClusterType != awsClusterTypeEKS || mockVerifier.request.Scope.ClusterName != "iam-test-eks" {
		t.Fatalf("unexpected EKS request: %#v", mockVerifier.request.Scope)
	}
}

func TestAWSScopesTagsVerifyEnablePersistsSingleClusterConfigurationOnly(t *testing.T) {
	appliedScope := testTagVerificationScope(awsClusterTypeKubeadm, "")
	statePath := writeTerraformStateTestFile(t, rawTagVerificationState(appliedScope))
	scopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  # Preserve this operator comment while enabling the policy.
  clusterType: kubeadm
  region: ap-southeast-2
  scopeTagPolicyVersion: 0
  vpc:
    mode: dittocloud
    name: ditto-k8s
    cidr: 10.210.0.0/16
    publicSubnetNetmask: 22
    privateSubnetNetmask: 18
    natGatewayName: iam-test-timc-nat
`)
	stateBefore, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	mockVerifier := &mockAWSScopeTagVerifier{report: awsScopeTagVerificationReport{
		StateResources:   []awsScopeTagVerifiedResource{{Identity: "state", Type: "aws_vpc"}},
		ClusterResources: []awsScopeTagVerifiedResource{{Identity: "cluster", Type: "ec2"}},
	}}
	setMockAWSScopeTagVerifier(t, mockVerifier)

	terraformMock, output, err := executeAWSScopesTagsVerifyTest(
		t,
		statePath,
		scopesPath,
		"--cluster-name=iam-test-timc",
		"--enable",
	)
	if err != nil {
		t.Fatalf("unexpected enable error: %v", err)
	}
	assertNoTerraformLifecycleCalls(t, terraformMock)
	if mockVerifier.calls != 1 {
		t.Fatalf("AWS verifier calls: got %d, want 1", mockVerifier.calls)
	}
	stateAfter, err := os.ReadFile(statePath)
	if err != nil || !bytes.Equal(stateBefore, stateAfter) {
		t.Fatalf("configuration-only enable modified Terraform state: err=%v", err)
	}
	updated, err := loadAWSDeploymentScopes(scopesPath)
	if err != nil {
		t.Fatalf("unable to load enabled scopes file: %v", err)
	}
	scope := updated[testDefaultScopeRef]
	if scope.ClusterName != "iam-test-timc" || scope.ScopeTagPolicyVersion != 1 {
		t.Fatalf("enabled scope: got %#v", scope)
	}
	content, err := os.ReadFile(scopesPath)
	if err != nil {
		t.Fatalf("unable to read enabled scopes file: %v", err)
	}
	if !bytes.Contains(content, []byte("Preserve this operator comment")) {
		t.Fatalf("enablement discarded existing YAML comments:\n%s", content)
	}
	for _, want := range []string{
		"Enabled configuration for version 1",
		"clusterName=iam-test-timc",
		"scopeTagPolicyVersion=1",
		"Run normal scope mode",
	} {
		if !strings.Contains(output, want) {
			t.Errorf("enable output does not contain %q:\n%s", want, output)
		}
	}
}

func TestAWSScopesTagsVerifyRejectsClusterNameFlagMismatch(t *testing.T) {
	scope := testTagVerificationScope(awsClusterTypeKubeadm, "iam-test-timc")
	statePath := writeTerraformStateTestFile(t, rawTagVerificationState(scope))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	encoded, err := encodeAWSDeploymentScopesDocument(scopesPath, AWSDeploymentScopes{testDefaultScopeRef: scope})
	if err != nil {
		t.Fatalf("unable to encode scopes fixture: %v", err)
	}
	if err := os.WriteFile(scopesPath, encoded, 0600); err != nil {
		t.Fatalf("unable to write scopes fixture: %v", err)
	}
	originalFactory := newAWSScopeTagVerifier
	factoryCalls := 0
	newAWSScopeTagVerifier = func(context.Context, string, string) (awsScopeTagVerifier, error) {
		factoryCalls++
		return &mockAWSScopeTagVerifier{}, nil
	}
	t.Cleanup(func() { newAWSScopeTagVerifier = originalFactory })

	terraformMock, _, err := executeAWSScopesTagsVerifyTest(t, statePath, scopesPath, "--cluster-name=other-cluster")
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("got %v, want cluster-name mismatch error", err)
	}
	assertNoTerraformLifecycleCalls(t, terraformMock)
	if factoryCalls != 0 {
		t.Fatalf("cluster-name mismatch initialized AWS verifier %d time(s)", factoryCalls)
	}
}

func TestAWSScopesTagsVerifyRejectsUnsafePreflight(t *testing.T) {
	baseScope := testTagVerificationScope(awsClusterTypeKubeadm, "iam-test-timc")
	tests := []struct {
		name      string
		scope     AWSDeploymentScope
		state     map[string]any
		scopeRef  string
		wantError string
	}{
		{
			name:      "shared multi-cluster scope has no cluster name",
			scope:     testTagVerificationScope(awsClusterTypeKubeadm, ""),
			state:     rawTagVerificationState(testTagVerificationScope(awsClusterTypeKubeadm, "")),
			wantError: "version 0 supports shared multi-cluster operation",
		},
		{
			name:      "scope is absent from state",
			scope:     baseScope,
			state:     rawScopeRegistryState(rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, true)),
			wantError: "is immutable and cannot be removed or reassigned",
		},
		{
			name:      "requested policy version differs from applied marker",
			scope:     func() AWSDeploymentScope { changed := baseScope; changed.ScopeTagPolicyVersion = 1; return changed }(),
			state:     rawTagVerificationState(baseScope),
			wantError: "expected applied tag-policy version 1 but Terraform state records version 0",
		},
		{
			name:      "state has no tagged managed inventory",
			scope:     baseScope,
			state:     rawRecoverableScopeState(AWSDeploymentScopes{testDefaultScopeRef: baseScope}),
			wantError: "contains no Dittocloud-managed resources tagged for scope",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			statePath := writeTerraformStateTestFile(t, test.state)
			scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
			encoded, err := encodeAWSDeploymentScopesDocument(scopesPath, AWSDeploymentScopes{testDefaultScopeRef: test.scope})
			if err != nil {
				t.Fatalf("unable to encode scopes fixture: %v", err)
			}
			if err := os.WriteFile(scopesPath, encoded, 0600); err != nil {
				t.Fatalf("unable to write scopes fixture: %v", err)
			}
			factoryCalls := 0
			originalFactory := newAWSScopeTagVerifier
			newAWSScopeTagVerifier = func(context.Context, string, string) (awsScopeTagVerifier, error) {
				factoryCalls++
				return &mockAWSScopeTagVerifier{}, nil
			}
			t.Cleanup(func() { newAWSScopeTagVerifier = originalFactory })

			terraformMock, _, err := executeAWSScopesTagsVerifyTest(t, statePath, scopesPath)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
			assertNoTerraformLifecycleCalls(t, terraformMock)
			if factoryCalls != 0 {
				t.Fatalf("unsafe preflight initialized AWS verifier %d time(s)", factoryCalls)
			}
		})
	}
}

func TestAWSScopesTagsVerifyPropagatesLiveFailureAndReleasesLocks(t *testing.T) {
	scope := testTagVerificationScope(awsClusterTypeKubeadm, "iam-test-timc")
	statePath := writeTerraformStateTestFile(t, rawTagVerificationState(scope))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	encoded, err := encodeAWSDeploymentScopesDocument(scopesPath, AWSDeploymentScopes{testDefaultScopeRef: scope})
	if err != nil {
		t.Fatalf("unable to encode scopes fixture: %v", err)
	}
	if err := os.WriteFile(scopesPath, encoded, 0600); err != nil {
		t.Fatalf("unable to write scopes fixture: %v", err)
	}
	mockVerifier := &mockAWSScopeTagVerifier{err: errors.New("injected live tag failure")}
	setMockAWSScopeTagVerifier(t, mockVerifier)

	terraformMock, _, err := executeAWSScopesTagsVerifyTest(t, statePath, scopesPath)
	if err == nil || !strings.Contains(err.Error(), "injected live tag failure") {
		t.Fatalf("expected live verification failure, got %v", err)
	}
	assertNoTerraformLifecycleCalls(t, terraformMock)
	operationLock, err := acquireStateOperationLock(statePath, "test lock release")
	if err != nil {
		t.Fatalf("state lock was not released: %v", err)
	}
	_ = operationLock.Release()
	fileLock, err := acquireScopesFileLock(scopesPath, "test lock release")
	if err != nil {
		t.Fatalf("scopes-file lock was not released: %v", err)
	}
	_ = fileLock.Release()
}

func TestAWSScopesTagsVerifyRejectsTerraformLifecycleFlags(t *testing.T) {
	scope := testTagVerificationScope(awsClusterTypeKubeadm, "iam-test-timc")
	statePath := writeTerraformStateTestFile(t, rawTagVerificationState(scope))
	scopesPath := filepath.Join(t.TempDir(), "scopes.yaml")
	encoded, err := encodeAWSDeploymentScopesDocument(scopesPath, AWSDeploymentScopes{testDefaultScopeRef: scope})
	if err != nil {
		t.Fatalf("unable to encode scopes fixture: %v", err)
	}
	if err := os.WriteFile(scopesPath, encoded, 0600); err != nil {
		t.Fatalf("unable to write scopes fixture: %v", err)
	}
	cmd, terraformMock := setupBootstrapTest(t, []string{
		"aws", "scopes", "tags", "verify",
		"--state=" + statePath,
		"--scopes-file=" + scopesPath,
		"--scope-ref=" + testDefaultScopeRef,
		"--dry-run",
	})
	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "--dry-run cannot be used with read-only scopes tags verify") {
		t.Fatalf("got %v, want lifecycle flag rejection", err)
	}
	assertNoTerraformLifecycleCalls(t, terraformMock)
}

func TestAWSClusterNativeIdentityTagValidation(t *testing.T) {
	valid := map[string]string{
		"kubernetes.io/cluster/iam-test-timc":                        "owned",
		"sigs.k8s.io/cluster-api-provider-aws/cluster/iam-test-timc": "owned",
		"elbv2.k8s.aws/cluster":                                      "iam-test-timc",
		awsScopeIdentityTagKey:                                       testDefaultScopeRef,
	}
	if err := validateAWSClusterNativeIdentityTags("arn:valid", valid, testDefaultScopeRef, "iam-test-timc"); err != nil {
		t.Fatalf("valid native identity tags were rejected: %v", err)
	}

	tests := []struct {
		name      string
		tags      map[string]string
		wantError string
	}{
		{
			name:      "cross scope tag",
			tags:      map[string]string{awsScopeIdentityTagKey: testSecondaryScopeRef},
			wantError: "conflicting tag",
		},
		{
			name:      "other kubernetes owner",
			tags:      map[string]string{"kubernetes.io/cluster/other": "owned"},
			wantError: "conflicting owned-cluster tag",
		},
		{
			name:      "other capa owner",
			tags:      map[string]string{"sigs.k8s.io/cluster-api-provider-aws/cluster/other": "owned"},
			wantError: "conflicting CAPA cluster tag",
		},
		{
			name:      "other load balancer cluster",
			tags:      map[string]string{"elbv2.k8s.aws/cluster": "other"},
			wantError: "conflicting elbv2.k8s.aws/cluster",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAWSClusterNativeIdentityTags("arn:test", test.tags, testDefaultScopeRef, "iam-test-timc")
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestAWSStateScopeTagInventoryRejectsUnknownCatalogType(t *testing.T) {
	scope := testTagVerificationScope(awsClusterTypeKubeadm, "iam-test-timc")
	state := rawRecoverableScopeState(AWSDeploymentScopes{testDefaultScopeRef: scope})
	appendRawTerraformStateResource(state, map[string]any{
		"mode": "managed",
		"type": "aws_new_taggable_resource",
		"name": "future",
		"instances": []any{map[string]any{
			"attributes": map[string]any{
				"id":       "future-1",
				"tags_all": map[string]any{awsScopeIdentityTagKey: testDefaultScopeRef},
			},
		}},
	})
	statePath := writeTerraformStateTestFile(t, state)
	_, _, err := loadAWSStateScopeTagInventory(statePath, testDefaultScopeRef, scope)
	if err == nil || !strings.Contains(err.Error(), "unsupported verification type") {
		t.Fatalf("got %v, want unsupported catalog type error", err)
	}
}

func TestRequireExactAWSScopeIdentityTag(t *testing.T) {
	if err := requireExactAWSScopeIdentityTag("resource", map[string]string{awsScopeIdentityTagKey: testDefaultScopeRef}, testDefaultScopeRef); err != nil {
		t.Fatalf("valid scope identity tag rejected: %v", err)
	}
	for name, tags := range map[string]map[string]string{
		"missing":  {},
		"conflict": {awsScopeIdentityTagKey: testSecondaryScopeRef},
	} {
		t.Run(name, func(t *testing.T) {
			if err := requireExactAWSScopeIdentityTag("resource", tags, testDefaultScopeRef); err == nil {
				t.Fatal("expected scope identity tag failure")
			}
		})
	}
}

func TestAWSRawTerraformResourceAddress(t *testing.T) {
	resource := rawTerraformResource{Module: "module.example[0]", Type: "aws_vpc", Name: "this"}
	instance := rawTerraformInstance{IndexKey: []byte(`"key"`)}
	if got, want := awsRawTerraformResourceAddress(resource, instance), `module.example[0].aws_vpc.this["key"]`; got != want {
		t.Fatalf("address: got %q, want %q", got, want)
	}
	if !reflect.DeepEqual(awsARNService("arn:aws:eks:region:account:cluster/name"), "eks") {
		t.Fatal("AWS ARN service parsing failed")
	}
}

func TestAWSScopeTagInventoryAccountID(t *testing.T) {
	resources := []awsScopeTagExpectedResource{
		{Address: "vpc", ARN: "arn:aws:ec2:ap-southeast-2:123456789012:vpc/vpc-1"},
		{Address: "role", ARN: "arn:aws:iam::123456789012:role/example"},
	}
	accountID, err := awsScopeTagInventoryAccountID(resources)
	if err != nil || accountID != "123456789012" {
		t.Fatalf("account identity: got %q, error %v", accountID, err)
	}
	resources[1].ARN = "arn:aws:iam::210987654321:role/example"
	if _, err := awsScopeTagInventoryAccountID(resources); err == nil || !strings.Contains(err.Error(), "exactly one AWS account") {
		t.Fatalf("got %v, want cross-account inventory error", err)
	}
}
