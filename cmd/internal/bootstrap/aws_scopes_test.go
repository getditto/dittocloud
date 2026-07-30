package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func writeAWSScopeTestFile(t *testing.T, content string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "scopes.yaml")
	if err := os.WriteFile(path, []byte(content), 0600); err != nil {
		t.Fatalf("unable to write scopes test file: %v", err)
	}
	return path
}

func TestLoadAWSDeploymentScopes(t *testing.T) {
	t.Run("accepts all supported VPC modes and defaults cluster type", func(t *testing.T) {
		path := writeAWSScopeTestFile(t, `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  clusterName: cluster-x
  clusterType: eks
  region: ap-southeast-2
  scopeTagPolicyVersion: 1
  vpc:
    mode: dittocloud
    name: ditto-k8s
    cidr: 10.210.0.0/16
    natGatewayName: founding-cluster-nat
dsc-01k2m8g7n4p6q9r3t5v8x1y2z4:
  region: us-east-1
  vpc:
    mode: existing
    id: vpc-09e877f9012f52241
dsc-01k2m8g7n4p6q9r3t5v8x1y2z5:
  region: us-gov-west-1
  vpc:
    mode: capi
    id: vpc-01234567
`)

		scopes, err := loadAWSDeploymentScopes(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(scopes) != 3 {
			t.Fatalf("got %d scopes, want 3", len(scopes))
		}
		if got := scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].ClusterType; got != awsClusterTypeEKS {
			t.Errorf("default scope cluster type: got %q, want %q", got, awsClusterTypeEKS)
		}
		if got := scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"].ClusterType; got != awsClusterTypeKubeadm {
			t.Errorf("existing VPC scope default cluster type: got %q, want %q", got, awsClusterTypeKubeadm)
		}
		if got := scopes["dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"].VPC.NATGatewayName; got != "founding-cluster-nat" {
			t.Errorf("default scope NAT gateway name: got %q, want %q", got, "founding-cluster-nat")
		}
	})

	t.Run("marshals Terraform field names", func(t *testing.T) {
		path := writeAWSScopeTestFile(t, `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  clusterName: cluster-x
  region: ap-southeast-2
  vpc:
    mode: dittocloud
    name: ditto-k8s
    cidr: 10.210.0.0/16
    natGatewayName: founding-cluster-nat
`)
		scopes, err := loadAWSDeploymentScopes(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		encoded, err := marshalAWSDeploymentScopes(scopes)
		if err != nil {
			t.Fatalf("unexpected marshal error: %v", err)
		}
		for _, want := range []string{`"default":true`, `"cluster_name":"cluster-x"`, `"cluster_type":"kubeadm"`, `"scope_tag_policy_version":0`, `"nat_gateway_name":"founding-cluster-nat"`} {
			if !strings.Contains(encoded, want) {
				t.Errorf("encoded scopes %q do not contain %q", encoded, want)
			}
		}
	})

	t.Run("accepts repeated cluster names across scopes", func(t *testing.T) {
		path := writeAWSScopeTestFile(t, `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  clusterName: shared-cluster
  region: ap-southeast-2
  vpc:
    mode: capi
dsc-01k2m8g7n4p6q9r3t5v8x1y2z4:
  clusterName: shared-cluster
  region: us-east-1
  vpc:
    mode: capi
`)
		if _, err := loadAWSDeploymentScopes(path); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	tests := []struct {
		name      string
		content   string
		wantError string
	}{
		{
			name:      "rejects an empty file",
			content:   ``,
			wantError: "at least one deployment scope is required",
		},
		{
			name: "rejects an unknown field",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  unexpected: true
  vpc:
    mode: capi
`,
			wantError: "field unexpected not found",
		},
		{
			name: "rejects multiple YAML documents",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
---
dsc-01k2m8g7n4p6q9r3t5v8x1y2z4:
  default: true
  region: us-east-1
  vpc:
    mode: capi
`,
			wantError: "must contain exactly one YAML document",
		},
		{
			name: "requires one default scope",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  region: ap-southeast-2
  vpc:
    mode: capi
`,
			wantError: "exactly one deployment scope must set default: true; found 0",
		},
		{
			name: "rejects multiple default scopes",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
dsc-01k2m8g7n4p6q9r3t5v8x1y2z4:
  default: true
  region: us-east-1
  vpc:
    mode: capi
`,
			wantError: "exactly one deployment scope must set default: true; found 2",
		},
		{
			name: "rejects uppercase and underscore in scope reference",
			content: `
Sydney_EKS:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`,
			wantError: "must be exactly 30 characters in generated dsc-<lowercase-crockford-ulid> form",
		},
		{
			name: "rejects over-length scope reference",
			content: `
this-scope-reference-is-over-limit:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`,
			wantError: "must be exactly 30 characters in generated dsc-<lowercase-crockford-ulid> form",
		},
		{
			name: "rejects removed scope display name field",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  name: "Legacy Sydney"
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`,
			wantError: "field name not found",
		},
		{
			name: "rejects unsupported cluster type",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  clusterType: kops
  region: ap-southeast-2
  vpc:
    mode: capi
`,
			wantError: "clusterType must be \"kubeadm\" or \"eks\"",
		},
		{
			name: "validates supplied Kubernetes cluster name",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  clusterName: cluster_x
  region: ap-southeast-2
  vpc:
    mode: capi
`,
			wantError: "must be a lowercase DNS label",
		},
		{
			name: "rejects unsupported scope tag policy version",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  scopeTagPolicyVersion: 2
  vpc:
    mode: capi
`,
			wantError: "scopeTagPolicyVersion must be 0 or 1",
		},
		{
			name: "requires a single named cluster for policy version one",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  scopeTagPolicyVersion: 1
  vpc:
    mode: capi
`,
			wantError: "scopeTagPolicyVersion 1 requires one exact clusterName",
		},
		{
			name: "rejects invalid region",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: sydney
  vpc:
    mode: capi
`,
			wantError: "is not a valid AWS region name",
		},
		{
			name: "requires Dittocloud VPC settings",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: dittocloud
`,
			wantError: "requires vpc.name",
		},
		{
			name: "requires IPv4 CIDR for Dittocloud VPC",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: dittocloud
    name: ditto
    cidr: 2001:db8::/32
`,
			wantError: "must be a valid IPv4 CIDR",
		},
		{
			name: "rejects managed settings in CAPI mode",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
    cidr: 10.210.0.0/16
`,
			wantError: "cannot set vpc.name, vpc.cidr, or vpc.natGatewayName",
		},
		{
			name: "rejects NAT gateway name outside Dittocloud mode",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: existing
    id: vpc-09e877f9012f52241
    natGatewayName: founding-cluster-nat
`,
			wantError: "cannot set vpc.name, vpc.cidr, or vpc.natGatewayName",
		},
		{
			name: "rejects whitespace-bounded NAT gateway name",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: dittocloud
    name: ditto-k8s
    cidr: 10.210.0.0/16
    natGatewayName: " founding-cluster-nat"
`,
			wantError: "vpc.natGatewayName must contain 1 to 256",
		},
		{
			name: "requires existing VPC ID",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: existing
`,
			wantError: "requires a valid vpc.id",
		},
		{
			name: "rejects unsupported VPC mode",
			content: `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: other
`,
			wantError: "vpc.mode must be one of",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeAWSScopeTestFile(t, test.content)
			_, err := loadAWSDeploymentScopes(path)
			if err == nil {
				t.Fatalf("expected error containing %q", test.wantError)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got error %q, want it to contain %q", err, test.wantError)
			}
		})
	}
}

func TestAWSDeploymentScopesValidateScopeReference(t *testing.T) {
	validScope := AWSDeploymentScope{
		Default:     true,
		ClusterType: awsClusterTypeKubeadm,
		Region:      "ap-southeast-2",
		VPC: AWSScopeVPC{
			Mode: awsVPCModeCAPI,
		},
	}
	validReference := "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"

	tests := []struct {
		name      string
		scopeRef  string
		wantError bool
	}{
		{name: "accepts generated reference", scopeRef: validReference},
		{name: "accepts maximum ULID timestamp prefix", scopeRef: "dsc-71k2m8g7n4p6q9r3t5v8x1y2z3"},
		{name: "rejects wrong prefix", scopeRef: "vpc-01k2m8g7n4p6q9r3t5v8x1y2z3", wantError: true},
		{name: "rejects uppercase alphabet", scopeRef: strings.Replace(validReference, "k", "K", 1), wantError: true},
		{name: "rejects forbidden Crockford alphabet", scopeRef: strings.Replace(validReference, "k", "i", 1), wantError: true},
		{name: "rejects out of range ULID timestamp prefix", scopeRef: strings.Replace(validReference, "0", "8", 1), wantError: true},
		{name: "rejects short reference", scopeRef: validReference[:len(validReference)-1], wantError: true},
		{name: "rejects long reference", scopeRef: validReference + "0", wantError: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := (AWSDeploymentScopes{test.scopeRef: validScope}).Validate()
			if !test.wantError && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if test.wantError {
				if err == nil {
					t.Fatal("expected scope reference validation error")
				}
				if !strings.Contains(err.Error(), "generated dsc-<lowercase-crockford-ulid> form") {
					t.Fatalf("got error %q, want strict generated scope reference error", err)
				}
			}
		})
	}
}

func TestAWSScopesFlags(t *testing.T) {
	validScopesPath := writeAWSScopeTestFile(t, `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  region: ap-southeast-2
  vpc:
    mode: existing
    id: vpc-09e877f9012f52241
`)
	versionOneScopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  clusterName: secure-cluster
  region: ap-southeast-2
  scopeTagPolicyVersion: 1
  vpc:
    mode: capi
`)

	tests := []struct {
		name          string
		args          []string
		wantError     string
		wantTerraform bool
	}{
		{
			name:      "requires scopes file in scope mode",
			args:      []string{"aws", "--scopes=true", "--dry-run"},
			wantError: "--scopes-file is required with --scopes=true",
		},
		{
			name:      "rejects scopes file outside scope mode",
			args:      []string{"aws", "--scopes-file=" + validScopesPath, "--dry-run"},
			wantError: "--scopes-file requires --scopes=true",
		},
		{
			name:      "rejects removal authorization outside scope mode",
			args:      []string{"aws", "--allow-scope-removal=" + testSecondaryScopeRef, "--dry-run"},
			wantError: "--allow-scope-removal requires --scopes=true",
		},
		{
			name: "rejects legacy per-scope flags",
			args: []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + validScopesPath,
				"--aws-region=us-east-1",
				"--dry-run",
			},
			wantError: "--aws-region cannot be used with --scopes=true",
		},
		{
			name: "rejects hidden Terraform variable overrides",
			args: []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + validScopesPath,
				"--tf-var=region=us-east-1",
				"--dry-run",
			},
			wantError: "--tf-var cannot be used with --scopes=true",
		},
		{
			name: "accepts account-level profile and trusted role flags",
			args: []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + validScopesPath,
				"--aws-profile=test-profile",
				"--controller-trusted-role-arns=arn:aws:iam::123456789012:role/controller",
				"--iam-trusted-role-arns=arn:aws:iam::123456789012:role/trust-editor",
				"--dry-run",
			},
			wantTerraform: true,
		},
		{
			name: "validates then executes the normal Terraform lifecycle",
			args: []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + validScopesPath,
				"--dry-run",
			},
			wantTerraform: true,
		},
		{
			name: "requires a seeded registry for scope-mode imports",
			args: []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + validScopesPath,
				"--import-resource=aws_iam_role.example=example",
			},
			wantError: "scope-mode imports require a seeded scope registry",
		},
		{
			name: "rejects starting a new scope at tag policy version one",
			args: []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + versionOneScopesPath,
				"--dry-run",
			},
			wantError: "cannot start at scopeTagPolicyVersion 1",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd, mock := setupBootstrapTest(t, test.args)
			err := cmd.Execute()
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got %v, want error containing %q", err, test.wantError)
				}
				assertCallCounts(t, mock, 0, 0, 0)
				return
			}
			if err != nil {
				t.Fatalf("unexpected scope-mode lifecycle error: %v", err)
			}
			if !test.wantTerraform {
				t.Fatal("test case must declare either wantError or wantTerraform")
			}
			assertCallCounts(t, mock, 1, 1, 0)
		})
	}

	legacyScopeFlags := map[string]string{
		"aws-vpc-name":         "ditto-secondary",
		"aws-vpc-cidr":         "10.220.0.0/16",
		"create-vpc":           "false",
		"enable-eks":           "true",
		"customer-managed-vpc": "true",
		"vpc-id":               "vpc-09e877f9012f52241",
		"cluster-name":         "cluster-x",
	}
	for flagName, flagValue := range legacyScopeFlags {
		t.Run("rejects explicit "+flagName, func(t *testing.T) {
			cmd, mock := setupBootstrapTest(t, []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + validScopesPath,
				"--" + flagName + "=" + flagValue,
				"--dry-run",
			})
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected --%s to be rejected", flagName)
			}
			wantError := "--" + flagName + " cannot be used with --scopes=true"
			if !strings.Contains(err.Error(), wantError) {
				t.Fatalf("got error %q, want it to contain %q", err, wantError)
			}
			assertCallCounts(t, mock, 0, 0, 0)
		})
	}
}

func TestAWSScopesNormalExecutionPassesValidatedScopesAndPrintsSummary(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
		rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, false),
	))
	scopesPath := writeAWSScopeTestFile(t, `
`+testSecondaryScopeRef+`:
  clusterType: eks
  region: us-west-2
  vpc:
    mode: existing
    id: vpc-09e877f9012f52241
`+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`)
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
		"--aws-profile=test-profile",
		"--controller-trusted-role-arns=arn:aws:iam::123456789012:role/controller",
		"--iam-trusted-role-arns=arn:aws:iam::123456789012:role/trust-editor",
		"--dry-run",
	})
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected normal scope-mode error: %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 0)
	if mock.PlanVars["profile"] != "test-profile" ||
		mock.PlanVars["controller_trusted_role_arns"] != `["arn:aws:iam::123456789012:role/controller"]` ||
		mock.PlanVars["iam_trusted_role_arns"] != `["arn:aws:iam::123456789012:role/trust-editor"]` {
		t.Fatalf("scope-mode account variables were not passed exactly: %#v", mock.PlanVars)
	}
	for _, legacyVariable := range []string{"region", "vpc_name", "vpc_cidr", "create_vpc", "enable_eks", "customer_managed_vpc", "vpc_id", "cluster_name"} {
		if _, exists := mock.PlanVars[legacyVariable]; exists {
			t.Errorf("scope mode passed legacy variable %q", legacyVariable)
		}
	}
	var plannedScopes AWSDeploymentScopes
	if err := json.Unmarshal([]byte(mock.PlanVars["deployment_scopes"]), &plannedScopes); err != nil {
		t.Fatalf("unable to decode planned deployment_scopes: %v", err)
	}
	if len(plannedScopes) != 2 || !plannedScopes[testDefaultScopeRef].Default || plannedScopes[testSecondaryScopeRef].Region != "us-west-2" {
		t.Fatalf("unexpected planned deployment scopes: %#v", plannedScopes)
	}

	wantSummaryLines := []string{
		"AWS deployment scopes (2):",
		"  - " + testDefaultScopeRef + " [default]: region=ap-southeast-2 clusterType=kubeadm vpcMode=capi",
		"  - " + testSecondaryScopeRef + ": region=us-west-2 clusterType=eks vpcMode=existing",
	}
	lastOffset := -1
	for _, line := range wantSummaryLines {
		offset := strings.Index(output.String(), line)
		if offset < 0 {
			t.Errorf("scope summary omitted %q:\n%s", line, output.String())
		}
		if offset <= lastOffset {
			t.Errorf("scope summary is not in deterministic order:\n%s", output.String())
		}
		lastOffset = offset
	}
}

func TestAWSScopesNormalApplyPersistsPartialStateAfterFailure(t *testing.T) {
	originalPrompt := terraformApplyPrompt
	terraformApplyPrompt = func(label, defaultValue string) string { return "yes" }
	t.Cleanup(func() { terraformApplyPrompt = originalPrompt })

	initialStateValue := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	)
	appendRawTerraformStateResource(initialStateValue, rawScopeTagPolicyResource(
		rawScopeTagPolicyInstance(testDefaultScopeRef, testDefaultScopeRef, 0),
	))
	statePath := writeTerraformStateTestFile(t, initialStateValue)
	scopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`+testSecondaryScopeRef+`:
  region: us-west-2
  vpc:
    mode: capi
`)
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
	})
	partialStateValue := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
		rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, false),
	)
	appendRawTerraformStateResource(partialStateValue, rawScopeTagPolicyResource(
		rawScopeTagPolicyInstance(testDefaultScopeRef, testDefaultScopeRef, 0),
	))
	appendRawTerraformStateResource(partialStateValue, rawScopedAWSResource(testSecondaryScopeRef))
	partialStateValue["serial"] = int64(2)
	partialState, err := json.Marshal(partialStateValue)
	if err != nil {
		t.Fatalf("unable to encode partial state: %v", err)
	}
	mock.applyState = partialState
	mock.applyReturnError = errors.New("injected scope apply failure")

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "partial Terraform state was saved") {
		t.Fatalf("expected failed-apply persistence error, got %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 1)
	persistedState, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(persistedState, partialState) {
		t.Fatalf("partial scope state was not persisted exactly: err=%v", readErr)
	}
	registry, registryErr := loadAWSStateScopeRegistry(statePath)
	if registryErr != nil || !registry.Present || !slices.Equal(sortedScopeRefs(registry.Scopes), []string{testDefaultScopeRef, testSecondaryScopeRef}) {
		t.Fatalf("persisted partial registry is invalid: registry=%#v err=%v", registry, registryErr)
	}
	operationLock, lockErr := acquireStateOperationLock(statePath, "test verification")
	if lockErr != nil {
		t.Fatalf("state operation lock was not released after failed apply: %v", lockErr)
	}
	_ = operationLock.Release()
}

func TestAWSScopesFailedRemovalPersistsScopeSentinelUntilResourcesAreGone(t *testing.T) {
	originalPrompt := terraformApplyPrompt
	terraformApplyPrompt = func(label, defaultValue string) string { return "yes" }
	t.Cleanup(func() { terraformApplyPrompt = originalPrompt })

	initialStateValue := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
		rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, false),
	)
	appendRawTerraformStateResource(initialStateValue, rawScopedAWSResource(testSecondaryScopeRef))
	statePath := writeTerraformStateTestFile(t, initialStateValue)
	scopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`)
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
		"--allow-scope-removal=" + testSecondaryScopeRef,
	})

	partialStateValue := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
		rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, false),
	)
	appendRawTerraformStateResource(partialStateValue, rawScopedAWSResource(testSecondaryScopeRef))
	partialStateValue["serial"] = int64(2)
	partialState, err := json.Marshal(partialStateValue)
	if err != nil {
		t.Fatalf("unable to encode partial removal state: %v", err)
	}
	mock.applyState = partialState
	mock.applyReturnError = errors.New("injected scope removal failure")

	err = cmd.Execute()
	if err == nil || !strings.Contains(err.Error(), "partial Terraform state was saved") {
		t.Fatalf("expected failed-removal persistence error, got %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 1)
	persistedState, readErr := os.ReadFile(statePath)
	if readErr != nil || !bytes.Equal(persistedState, partialState) {
		t.Fatalf("partial removal state was not persisted exactly: err=%v", readErr)
	}
	registry, registryErr := loadAWSStateScopeRegistry(statePath)
	if registryErr != nil || !registry.Present || !slices.Equal(sortedScopeRefs(registry.Scopes), []string{testDefaultScopeRef, testSecondaryScopeRef}) {
		t.Fatalf("failed removal did not retain both scope sentinels: registry=%#v err=%v", registry, registryErr)
	}
	desiredScopes := AWSDeploymentScopes{testDefaultScopeRef: testScope(true)}
	if lifecycleErr := validateAWSStateScopeLifecycle(statePath, desiredScopes, []string{testSecondaryScopeRef}); lifecycleErr != nil {
		t.Fatalf("persisted failed-removal state cannot be retried safely: %v", lifecycleErr)
	}
}

func sortedScopeRefs(scopes map[string]awsStateScopeIdentity) []string {
	refs := make([]string, 0, len(scopes))
	for scopeRef := range scopes {
		refs = append(refs, scopeRef)
	}
	slices.Sort(refs)
	return refs
}

func TestAWSScopeTerraformVariables(t *testing.T) {
	cmd, _ := setupBootstrapTest(t, nil)
	awsCommand, _, err := cmd.Find([]string{"aws"})
	if err != nil {
		t.Fatalf("unable to find AWS command: %v", err)
	}
	if err := awsCommand.ParseFlags([]string{
		"--scopes=true",
		"--scopes-file=unused-by-this-test",
		"--aws-profile=test-profile",
		"--controller-trusted-role-arns=arn:aws:iam::123456789012:role/controller",
		"--controller-trusted-role-arns=arn:aws:iam::123456789012:role/valet-controller",
		"--iam-trusted-role-arns=arn:aws:iam::123456789012:role/trust-editor",
	}); err != nil {
		t.Fatalf("unable to parse AWS flags: %v", err)
	}

	encodedScopes := `{"dsc-01k2m8g7n4p6q9r3t5v8x1y2z3":{"default":true,"cluster_type":"kubeadm","region":"ap-southeast-2","vpc":{"mode":"capi"}}}`
	values, err := awsScopeTerraformVariables(
		awsCommand.Flags(),
		encodedScopes,
		[]string{testDefaultScopeRef},
		nil,
	)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	got := make(map[string]string, len(values))
	for _, value := range values {
		name, encodedValue, found := strings.Cut(value, "=")
		if !found {
			t.Fatalf("Terraform variable %q is not in name=value form", value)
		}
		got[name] = encodedValue
	}

	want := map[string]string{
		"deployment_scopes":                    encodedScopes,
		"scope_tag_policy_cli_authorized_refs": `["` + testDefaultScopeRef + `"]`,
		"profile":                              "test-profile",
		"controller_trusted_role_arns":         `["arn:aws:iam::123456789012:role/controller","arn:aws:iam::123456789012:role/valet-controller"]`,
		"iam_trusted_role_arns":                `["arn:aws:iam::123456789012:role/trust-editor"]`,
	}
	for name, wantValue := range want {
		if gotValue := got[name]; gotValue != wantValue {
			t.Errorf("%s: got %q, want %q", name, gotValue, wantValue)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got Terraform variables %v, want exactly %v", got, want)
	}

	legacyValues, err := awsScopeTerraformVariables(
		awsCommand.Flags(),
		encodedScopes,
		nil,
		[]string{testDefaultScopeRef},
	)
	if err != nil {
		t.Fatalf("unexpected legacy bridge variable error: %v", err)
	}
	if !slices.Contains(legacyValues, `scope_tag_policy_v0_legacy_cluster_refs=["`+testDefaultScopeRef+`"]`) {
		t.Fatalf("legacy bridge Terraform variable missing from %v", legacyValues)
	}
}
