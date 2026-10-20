package bootstrap

import (
	"fmt"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestAWSScopeWorkloadBlockRoundTrip(t *testing.T) {
	path := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  clusterName: valet-dev
  clusterType: eks
  region: ap-southeast-2
  vpc:
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    secondaryCidr: 100.64.0.0/16
    enablePrivateNatGateways: true
    natGatewayEipAllocationIds:
      - eipalloc-0123456789abcdef0
      - eipalloc-0123456789abcdef1
      - eipalloc-0123456789abcdef2
`)

	scopes, err := loadAWSDeploymentScopes(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	scope := scopes[testDefaultScopeRef]
	if scope.VPC.SecondaryCIDR != "100.64.0.0/16" {
		t.Errorf("secondary CIDR: got %q, want %q", scope.VPC.SecondaryCIDR, "100.64.0.0/16")
	}
	// Terraform applies these defaults itself, and the CLI compares its own view
	// of a scope against the snapshot Terraform plans, so it has to hold them too.
	if scope.VPC.PublicSubnetNetmask != awsDefaultPublicSubnetNetmask {
		t.Errorf("public subnet netmask: got %d, want %d", scope.VPC.PublicSubnetNetmask, awsDefaultPublicSubnetNetmask)
	}
	if scope.VPC.PrivateSubnetNetmask != awsDefaultPrivateSubnetNetmask {
		t.Errorf("private subnet netmask: got %d, want %d", scope.VPC.PrivateSubnetNetmask, awsDefaultPrivateSubnetNetmask)
	}
	if len(scope.VPC.NATGatewayEIPAllocationIDs) != 3 {
		t.Fatalf("NAT Elastic IP allocations: got %v, want three entries", scope.VPC.NATGatewayEIPAllocationIDs)
	}

	encoded, err := marshalAWSDeploymentScopes(scopes)
	if err != nil {
		t.Fatalf("unexpected marshal error: %v", err)
	}
	if !scope.VPC.EnablePrivateNATGateways {
		t.Fatal("private NAT opt-in was lost while loading YAML")
	}
	for _, want := range []string{
		`"enable_private_nat_gateways":true`,
		`"secondary_cidr":"100.64.0.0/16"`,
		`"public_subnet_netmask":24`,
		`"private_subnet_netmask":23`,
		`"nat_gateway_eip_allocation_ids":["eipalloc-0123456789abcdef0","eipalloc-0123456789abcdef1","eipalloc-0123456789abcdef2"]`,
	} {
		if !strings.Contains(encoded, want) {
			t.Errorf("encoded scopes %q do not contain %q", encoded, want)
		}
	}
}

func TestAWSScopeWorkloadBlockRejectsUnusableConfiguration(t *testing.T) {
	tests := []struct {
		name      string
		vpc       string
		wantError string
	}{
		{
			name: "secondary CIDR outside the shared pool",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    secondaryCidr: 10.128.0.0/16`,
			wantError: "must fall inside 100.64.0.0/10",
		},
		{
			name: "secondary CIDR reserved for in-cluster addressing",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    secondaryCidr: 100.66.0.0/16`,
			wantError: "reserved for in-cluster pod and Service addressing",
		},
		{
			name: "secondary CIDR reserved for self-managed cluster pods",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    secondaryCidr: 100.80.0.0/16`,
			wantError: "reserved for in-cluster pod and Service addressing",
		},
		{
			name: "secondary CIDR reserved for self-managed cluster services",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    secondaryCidr: 100.81.0.0/16`,
			wantError: "reserved for in-cluster pod and Service addressing",
		},
		{
			name: "secondary CIDR that is not a /16",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    secondaryCidr: 100.64.0.0/18`,
			wantError: "must be a /16 IPv4 CIDR",
		},
		{
			name: "load-balancer subnet smaller than a /24",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    publicSubnetNetmask: 25`,
			wantError: "must be 24 or lower",
		},
		{
			name: "public tier that cannot hold three subnets",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/22
    publicSubnetNetmask: 24`,
			wantError: "must be at least 4 bits longer",
		},
		{
			name: "private tier overlapping the public tier",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    privateSubnetNetmask: 21`,
			wantError: "must be at least 2 bits longer",
		},
		{
			name: "incomplete set of NAT addresses",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    natGatewayEipAllocationIds:
      - eipalloc-0123456789abcdef0`,
			wantError: "must contain exactly 3 entries",
		},
		{
			name: "malformed NAT address",
			vpc: `
    mode: dittocloud
    name: valet
    cidr: 10.214.0.0/20
    natGatewayEipAllocationIds:
      - eipalloc-0123456789abcdef0
      - eipalloc-0123456789abcdef1
      - not-an-allocation`,
			wantError: "is not a valid Elastic IP allocation ID",
		},
		{
			name: "workload block on a VPC Dittocloud does not manage",
			vpc: `
    mode: existing
    id: vpc-09e877f9012f52241
    secondaryCidr: 100.64.0.0/16`,
			wantError: "cannot set vpc.secondaryCidr",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			path := writeAWSScopeTestFile(t, "\n"+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:`+test.vpc+"\n")

			_, err := loadAWSDeploymentScopes(path)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

// A snapshot written before the DMZ split has to recover with the sizing that
// module used, not today's defaults, or the next apply renumbers live subnets.
func TestLoadAWSStateScopeRegistryPinsLegacySizingForSchemaOneSnapshots(t *testing.T) {
	legacyScope := AWSDeploymentScope{
		Default:     true,
		ClusterType: awsClusterTypeKubeadm,
		Region:      "ap-southeast-2",
		VPC: AWSScopeVPC{
			Mode: awsVPCModeDittocloud,
			Name: "valet",
			CIDR: "10.214.0.0/16",
		},
	}
	state := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	)
	appendRawTerraformStateResource(state, rawScopeConfigurationResource(
		rawLegacyScopeConfigurationInstance(testDefaultScopeRef, testDefaultScopeRef, legacyScope),
	))
	statePath := writeTerraformStateTestFile(t, state)

	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		t.Fatalf("unexpected registry error: %v", err)
	}
	recovered := registry.Configurations[testDefaultScopeRef]
	if recovered.VPC.PublicSubnetNetmask != awsLegacyPublicSubnetNetmask {
		t.Errorf("public subnet netmask: got %d, want %d", recovered.VPC.PublicSubnetNetmask, awsLegacyPublicSubnetNetmask)
	}
	if recovered.VPC.PrivateSubnetNetmask != awsLegacyPrivateSubnetNetmask {
		t.Errorf("private subnet netmask: got %d, want %d", recovered.VPC.PrivateSubnetNetmask, awsLegacyPrivateSubnetNetmask)
	}
	if recovered.VPC.SecondaryCIDR != "" {
		t.Errorf("secondary CIDR: got %q, want none", recovered.VPC.SecondaryCIDR)
	}
}

func TestAWSLegacyVPCFlagsForwardWorkloadConfiguration(t *testing.T) {
	t.Run("forwards only what the operator set", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--aws-region=us-east-1",
			"--aws-vpc-name=valet",
			"--aws-vpc-cidr=10.214.0.0/20",
			"--aws-vpc-secondary-cidr=100.64.0.0/16",
			"--aws-vpc-private-subnet-netmask=23",
			"--karpenter-discovery-tag-value=valet-dev",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}
		wantVars := map[string]string{
			"vpc_cidr":                      "10.214.0.0/20",
			"vpc_secondary_cidr":            "100.64.0.0/16",
			"private_subnet_netmask":        "23",
			"karpenter_discovery_tag_value": "valet-dev",
		}
		for key, want := range wantVars {
			if got := mock.PlanVars[key]; got != want {
				t.Errorf("%s: got %q, want %q", key, got, want)
			}
		}
		// An unset netmask has to stay unset. Sending a value an existing
		// deployment was not built with renumbers its subnets.
		if got, ok := mock.PlanVars["public_subnet_netmask"]; ok {
			t.Errorf("public_subnet_netmask should not be sent when unset, got %q", got)
		}
	})

	t.Run("rejects workload configuration without a managed VPC", func(t *testing.T) {
		cmd, _ := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--create-vpc=false",
			"--aws-vpc-secondary-cidr=100.64.0.0/16",
			"--dry-run",
		})

		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--aws-vpc-secondary-cidr can only be used when --create-vpc=true") {
			t.Fatalf("got %v, want a managed-VPC-only flag error", err)
		}
	})

	t.Run("rejects workload configuration in scope mode", func(t *testing.T) {
		scopesPath := writeAWSScopeTestFile(t, "\n"+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`)
		cmd, _ := setupBootstrapTest(t, []string{
			"aws",
			"--scopes=true",
			"--scopes-file=" + scopesPath,
			"--aws-vpc-secondary-cidr=100.64.0.0/16",
			"--dry-run",
		})

		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "--aws-vpc-secondary-cidr cannot be used with --scopes=true") {
			t.Fatalf("got %v, want a scope-mode flag rejection", err)
		}
	})
}

func writeAWSLegacySubnetStateFile(t *testing.T, publicCIDR, privateCIDR string) string {
	t.Helper()
	return writeTerraformStateTestFile(t, rawTerraformStateWithResources([]any{
		rawAWSLegacyManagedSubnetResource("public", publicCIDR),
		rawAWSLegacyManagedSubnetResource("private", privateCIDR),
	}))
}

func TestAWSLegacySubnetRenumberingPreflight(t *testing.T) {
	t.Run("refuses to renumber the subnets already in state", func(t *testing.T) {
		statePath := writeAWSLegacySubnetStateFile(t, "10.214.0.0/22", "10.214.64.0/18")
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--state=" + statePath,
			"--dry-run",
		})

		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected a renumbering refusal")
		}
		for _, want := range []string{
			"would renumber the private and public subnets",
			"public subnets: /22 applied, /24 requested",
			"private subnets: /18 applied, /23 requested",
			"--aws-vpc-public-subnet-netmask 22",
			"--aws-vpc-private-subnet-netmask 18",
			"--allow-subnet-renumbering",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err.Error(), want)
			}
		}
		assertCallCounts(t, mock, 0, 0, 0)
	})

	t.Run("accepts the sizing that state already has", func(t *testing.T) {
		statePath := writeAWSLegacySubnetStateFile(t, "10.214.0.0/22", "10.214.64.0/18")
		cmd, _ := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--state=" + statePath,
			"--aws-vpc-public-subnet-netmask=22",
			"--aws-vpc-private-subnet-netmask=18",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error for pinned sizing: %v", err)
		}
	})

	t.Run("allows a deliberate renumbering", func(t *testing.T) {
		statePath := writeAWSLegacySubnetStateFile(t, "10.214.0.0/22", "10.214.64.0/18")
		cmd, _ := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--state=" + statePath,
			"--allow-subnet-renumbering",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error for authorized renumbering: %v", err)
		}
	})

	t.Run("stays quiet when Dittocloud does not manage the VPC", func(t *testing.T) {
		statePath := writeAWSLegacySubnetStateFile(t, "10.214.0.0/22", "10.214.64.0/18")
		cmd, _ := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--state=" + statePath,
			"--create-vpc=false",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error when Cluster API owns the VPC: %v", err)
		}
	})
}

func TestAWSScopeSubnetRenumberingPreflight(t *testing.T) {
	appliedScope := AWSDeploymentScope{
		Default:     true,
		ClusterType: awsClusterTypeKubeadm,
		Region:      "ap-southeast-2",
		VPC: AWSScopeVPC{
			Mode:                 awsVPCModeDittocloud,
			Name:                 "valet",
			CIDR:                 "10.214.0.0/16",
			PublicSubnetNetmask:  awsLegacyPublicSubnetNetmask,
			PrivateSubnetNetmask: awsLegacyPrivateSubnetNetmask,
		},
	}
	state := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	)
	appendRawTerraformStateResource(state, rawScopeConfigurationResource(
		rawScopeConfigurationInstance(testDefaultScopeRef, testDefaultScopeRef, appliedScope),
	))
	statePath := writeTerraformStateTestFile(t, state)

	t.Run("refuses sizing that differs from the applied configuration", func(t *testing.T) {
		desired := appliedScope
		desired.VPC.PublicSubnetNetmask = awsDefaultPublicSubnetNetmask
		desired.VPC.PrivateSubnetNetmask = awsDefaultPrivateSubnetNetmask

		err := validateAWSScopeSubnetRenumbering(
			statePath,
			AWSDeploymentScopes{testDefaultScopeRef: desired},
			false,
		)
		if err == nil {
			t.Fatal("expected a renumbering refusal")
		}
		for _, want := range []string{
			"would renumber its private and public subnets",
			"vpc.publicSubnetNetmask: 22",
			"vpc.privateSubnetNetmask: 18",
		} {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("error %q does not contain %q", err.Error(), want)
			}
		}
	})

	t.Run("accepts the applied sizing", func(t *testing.T) {
		if err := validateAWSScopeSubnetRenumbering(
			statePath,
			AWSDeploymentScopes{testDefaultScopeRef: appliedScope},
			false,
		); err != nil {
			t.Fatalf("unexpected error for unchanged sizing: %v", err)
		}
	})

	t.Run("allows a deliberate renumbering", func(t *testing.T) {
		desired := appliedScope
		desired.VPC.PublicSubnetNetmask = awsDefaultPublicSubnetNetmask

		if err := validateAWSScopeSubnetRenumbering(
			statePath,
			AWSDeploymentScopes{testDefaultScopeRef: desired},
			true,
		); err != nil {
			t.Fatalf("unexpected error for authorized renumbering: %v", err)
		}
	})
}

func TestAWSScopePrivateNATConfigurationRecovery(t *testing.T) {
	for _, version := range []int{2, 3} {
		t.Run(fmt.Sprintf("schema_%d", version), func(t *testing.T) {
			scope := AWSDeploymentScope{
				Default:     true,
				ClusterType: awsClusterTypeEKS,
				Region:      "us-west-2",
				VPC: AWSScopeVPC{
					Mode:                     awsVPCModeDittocloud,
					Name:                     "valet",
					CIDR:                     "10.214.0.0/20",
					PublicSubnetNetmask:      24,
					PrivateSubnetNetmask:     23,
					EnablePrivateNATGateways: true,
				},
			}
			instance := rawScopeConfigurationInstance(testDefaultScopeRef, testDefaultScopeRef, scope)
			if version == 2 {
				input := instance["attributes"].(map[string]any)["input"].(map[string]any)
				value := input["value"].(map[string]any)
				value["schema_version"] = 2
				delete(value["configuration"].(map[string]any)["vpc"].(map[string]any), "enable_private_nat_gateways")
				configurationType := input["type"].([]any)[1].(map[string]any)["configuration"].([]any)[1].(map[string]any)
				delete(configurationType["vpc"].([]any)[1].(map[string]any), "enable_private_nat_gateways")
			}
			state := rawScopeRegistryState(rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true))
			appendRawTerraformStateResource(state, rawScopeConfigurationResource(instance))
			registry, err := loadAWSStateScopeRegistry(writeTerraformStateTestFile(t, state))
			if err != nil {
				t.Fatalf("recover configuration: %v", err)
			}
			recovered := registry.Configurations[testDefaultScopeRef]
			if recovered.VPC.EnablePrivateNATGateways != (version == 3) {
				t.Fatalf("private NAT enabled = %t for schema %d", recovered.VPC.EnablePrivateNATGateways, version)
			}
			// Recovery's YAML writer must preserve an enabled opt-in too.
			encoded, err := yaml.Marshal(awsDeploymentScopeYAMLNode(recovered))
			if err != nil {
				t.Fatalf("encode recovered scope: %v", err)
			}
			if strings.Contains(string(encoded), "enablePrivateNatGateways: true") != (version == 3) {
				t.Fatalf("unexpected recovered YAML: %s", encoded)
			}
		})
	}
}

func TestAWSScopePrivateNATRejectedForUnmanagedVPCs(t *testing.T) {
	for _, mode := range []string{awsVPCModeCAPI, awsVPCModeExisting} {
		err := rejectAWSManagedVPCFields(testDefaultScopeRef, AWSScopeVPC{
			Mode:                     mode,
			EnablePrivateNATGateways: true,
		}, mode)
		if err == nil || !strings.Contains(err.Error(), "enablePrivateNatGateways") {
			t.Errorf("mode %s: got %v, want private NAT validation error", mode, err)
		}
	}
}

func TestAWSPrivateNATBootstrapFlag(t *testing.T) {
	for _, test := range []struct {
		name      string
		args      []string
		want      string
		wantError string
	}{
		{name: "unset leaves Terraform default", want: ""},
		{name: "enabled", args: []string{"--aws-vpc-enable-private-nat-gateways"}, want: "true"},
		{name: "explicitly disabled", args: []string{"--aws-vpc-enable-private-nat-gateways=false"}, want: "false"},
		{name: "VPC creation disabled", args: []string{"--aws-vpc-enable-private-nat-gateways", "--create-vpc=false"}, wantError: "can only be used when --create-vpc=true"},
		{name: "customer managed VPC", args: []string{"--aws-vpc-enable-private-nat-gateways", "--customer-managed-vpc", "--vpc-id=vpc-0123456789abcdef0"}, wantError: "can only be used when --create-vpc=true"},
		{name: "scope mode uses YAML", args: []string{"--aws-vpc-enable-private-nat-gateways", "--scopes", "--scopes-file=unused.yaml"}, wantError: "configure it in the scopes YAML"},
	} {
		t.Run(test.name, func(t *testing.T) {
			args := append([]string{"aws", "--aws-profile=test-profile", "--dry-run"}, test.args...)
			cmd, mock := setupBootstrapTest(t, args)
			err := cmd.Execute()
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got %v, want error containing %q", err, test.wantError)
				}
				assertNoTerraformLifecycleCalls(t, mock)
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got := mock.PlanVars["enable_private_nat_gateways"]; got != test.want {
				t.Fatalf("Terraform input = %q, want %q", got, test.want)
			}
		})
	}
}
