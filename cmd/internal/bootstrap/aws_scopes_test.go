package bootstrap

import (
	"os"
	"path/filepath"
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
	})

	t.Run("marshals Terraform field names", func(t *testing.T) {
		path := writeAWSScopeTestFile(t, `
dsc-01k2m8g7n4p6q9r3t5v8x1y2z3:
  default: true
  clusterName: cluster-x
  region: ap-southeast-2
  vpc:
    mode: existing
    id: vpc-09e877f9012f52241
`)
		scopes, err := loadAWSDeploymentScopes(path)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		encoded, err := marshalAWSDeploymentScopes(scopes)
		if err != nil {
			t.Fatalf("unexpected marshal error: %v", err)
		}
		for _, want := range []string{`"default":true`, `"cluster_name":"cluster-x"`, `"cluster_type":"kubeadm"`, `"scope_tag_policy_version":0`} {
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
			wantError: "cannot set vpc.name or vpc.cidr",
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

	tests := []struct {
		name      string
		args      []string
		wantError string
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
			wantError: "Terraform resource wiring is not implemented yet; no plan or apply was run",
		},
		{
			name: "validates then fails closed before Terraform",
			args: []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + validScopesPath,
				"--dry-run",
			},
			wantError: "Terraform resource wiring is not implemented yet; no plan or apply was run",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd, mock := setupBootstrapTest(t, test.args)
			err := cmd.Execute()
			if err == nil {
				t.Fatalf("expected error containing %q", test.wantError)
			}
			if !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got error %q, want it to contain %q", err, test.wantError)
			}
			assertCallCounts(t, mock, 0, 0, 0)
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
	values, err := awsScopeTerraformVariables(awsCommand.Flags(), encodedScopes)
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
		"deployment_scopes":            encodedScopes,
		"profile":                      "test-profile",
		"controller_trusted_role_arns": `["arn:aws:iam::123456789012:role/controller","arn:aws:iam::123456789012:role/valet-controller"]`,
		"iam_trusted_role_arns":        `["arn:aws:iam::123456789012:role/trust-editor"]`,
	}
	for name, wantValue := range want {
		if gotValue := got[name]; gotValue != wantValue {
			t.Errorf("%s: got %q, want %q", name, gotValue, wantValue)
		}
	}
	if len(got) != len(want) {
		t.Fatalf("got Terraform variables %v, want exactly %v", got, want)
	}
}
