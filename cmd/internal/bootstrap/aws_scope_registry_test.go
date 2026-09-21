package bootstrap

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	testDefaultScopeRef   = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
	testSecondaryScopeRef = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z4"
	testAddedScopeRef     = "dsc-01k2m8g7n4p6q9r3t5v8x1y2z5"
)

func testScope(defaultScope bool) AWSDeploymentScope {
	return AWSDeploymentScope{
		Default:     defaultScope,
		ClusterType: awsClusterTypeKubeadm,
		Region:      "ap-southeast-2",
		VPC:         AWSScopeVPC{Mode: awsVPCModeCAPI},
	}
}

func rawScopeRegistryInstance(addressScopeRef, storedScopeRef string, defaultScope bool) map[string]any {
	identity := map[string]any{
		"schema_version": 1,
		"scope_ref":      storedScopeRef,
		"default":        defaultScope,
	}
	dynamicIdentity := map[string]any{
		"value": identity,
		"type": []any{
			"object",
			map[string]string{
				"schema_version": "number",
				"scope_ref":      "string",
				"default":        "bool",
			},
		},
	}
	return map[string]any{
		"index_key": addressScopeRef,
		"attributes": map[string]any{
			"id":               "registry-id",
			"input":            dynamicIdentity,
			"output":           dynamicIdentity,
			"triggers_replace": nil,
		},
	}
}

func rawScopeTagPolicyInstance(addressScopeRef, storedScopeRef string, policyVersion int) map[string]any {
	policy := map[string]any{
		"schema_version": 1,
		"scope_ref":      storedScopeRef,
		"policy_version": policyVersion,
	}
	dynamicPolicy := map[string]any{
		"value": policy,
		"type": []any{
			"object",
			map[string]string{
				"schema_version": "number",
				"scope_ref":      "string",
				"policy_version": "number",
			},
		},
	}
	return map[string]any{
		"index_key": addressScopeRef,
		"attributes": map[string]any{
			"id":               "tag-policy-id",
			"input":            dynamicPolicy,
			"output":           dynamicPolicy,
			"triggers_replace": nil,
		},
	}
}

func rawScopeConfigurationInstance(addressScopeRef, storedScopeRef string, scope AWSDeploymentScope) map[string]any {
	optionalString := func(value string) any {
		if value == "" {
			return nil
		}
		return value
	}
	optionalNumber := func(value int) any {
		if value == 0 {
			return nil
		}
		return value
	}
	configuration := map[string]any{
		"default":                  scope.Default,
		"cluster_name":             optionalString(scope.ClusterName),
		"cluster_type":             scope.ClusterType,
		"region":                   scope.Region,
		"scope_tag_policy_version": scope.ScopeTagPolicyVersion,
		"vpc": map[string]any{
			"mode":                   scope.VPC.Mode,
			"name":                   optionalString(scope.VPC.Name),
			"cidr":                   optionalString(scope.VPC.CIDR),
			"secondary_cidr":         optionalString(scope.VPC.SecondaryCIDR),
			"public_subnet_netmask":  optionalNumber(scope.VPC.PublicSubnetNetmask),
			"private_subnet_netmask": optionalNumber(scope.VPC.PrivateSubnetNetmask),
			"id":                     optionalString(scope.VPC.ID),
			"nat_gateway_name":       optionalString(scope.VPC.NATGatewayName),
			"nat_gateway_eip_allocation_ids": func() []any {
				allocations := make([]any, 0, len(scope.VPC.NATGatewayEIPAllocationIDs))
				for _, allocationID := range scope.VPC.NATGatewayEIPAllocationIDs {
					allocations = append(allocations, allocationID)
				}
				return allocations
			}(),
		},
	}
	dynamicConfiguration := map[string]any{
		"value": map[string]any{
			"schema_version": awsScopeConfigurationSchemaVersion,
			"scope_ref":      storedScopeRef,
			"configuration":  configuration,
		},
		"type": []any{
			"object",
			map[string]any{
				"schema_version": "number",
				"scope_ref":      "string",
				"configuration": []any{
					"object",
					map[string]any{
						"default":                  "bool",
						"cluster_name":             "string",
						"cluster_type":             "string",
						"region":                   "string",
						"scope_tag_policy_version": "number",
						"vpc": []any{
							"object",
							map[string]any{
								"mode":                           "string",
								"name":                           "string",
								"cidr":                           "string",
								"secondary_cidr":                 "string",
								"public_subnet_netmask":          "number",
								"private_subnet_netmask":         "number",
								"id":                             "string",
								"nat_gateway_name":               "string",
								"nat_gateway_eip_allocation_ids": []any{"list", "string"},
							},
						},
					},
				},
			},
		},
	}
	return map[string]any{
		"index_key": addressScopeRef,
		"attributes": map[string]any{
			"id":               "configuration-id",
			"input":            dynamicConfiguration,
			"output":           dynamicConfiguration,
			"triggers_replace": nil,
		},
	}
}

// rawLegacyScopeConfigurationInstance reproduces a snapshot written before the
// DMZ split, when the configuration schema had no workload block or subnet
// sizing.
func rawLegacyScopeConfigurationInstance(addressScopeRef, storedScopeRef string, scope AWSDeploymentScope) map[string]any {
	optionalString := func(value string) any {
		if value == "" {
			return nil
		}
		return value
	}
	configuration := map[string]any{
		"default":                  scope.Default,
		"cluster_name":             optionalString(scope.ClusterName),
		"cluster_type":             scope.ClusterType,
		"region":                   scope.Region,
		"scope_tag_policy_version": scope.ScopeTagPolicyVersion,
		"vpc": map[string]any{
			"mode":             scope.VPC.Mode,
			"name":             optionalString(scope.VPC.Name),
			"cidr":             optionalString(scope.VPC.CIDR),
			"id":               optionalString(scope.VPC.ID),
			"nat_gateway_name": optionalString(scope.VPC.NATGatewayName),
		},
	}
	dynamicConfiguration := map[string]any{
		"value": map[string]any{
			"schema_version": 1,
			"scope_ref":      storedScopeRef,
			"configuration":  configuration,
		},
		"type": []any{
			"object",
			map[string]any{
				"schema_version": "number",
				"scope_ref":      "string",
				"configuration": []any{
					"object",
					map[string]any{
						"default":                  "bool",
						"cluster_name":             "string",
						"cluster_type":             "string",
						"region":                   "string",
						"scope_tag_policy_version": "number",
						"vpc": []any{
							"object",
							map[string]any{
								"mode":             "string",
								"name":             "string",
								"cidr":             "string",
								"id":               "string",
								"nat_gateway_name": "string",
							},
						},
					},
				},
			},
		},
	}
	return map[string]any{
		"index_key": addressScopeRef,
		"attributes": map[string]any{
			"id":               "configuration-id",
			"input":            dynamicConfiguration,
			"output":           dynamicConfiguration,
			"triggers_replace": nil,
		},
	}
}

func rawTerraformStateWithResources(resources []any) map[string]any {
	return map[string]any{
		"version":           supportedTerraformStateVersion,
		"terraform_version": "1.15.2",
		"serial":            1,
		"lineage":           "test-lineage",
		"outputs":           map[string]any{},
		"resources":         resources,
	}
}

func rawScopeRegistryState(instances ...map[string]any) map[string]any {
	rawInstances := make([]any, 0, len(instances))
	for _, instance := range instances {
		rawInstances = append(rawInstances, instance)
	}
	return rawTerraformStateWithResources([]any{
		map[string]any{
			"mode":      "managed",
			"type":      "terraform_data",
			"name":      "scope_registry",
			"provider":  "terraform.io/builtin/terraform",
			"instances": rawInstances,
		},
	})
}

func rawScopeTagPolicyResource(instances ...map[string]any) map[string]any {
	rawInstances := make([]any, 0, len(instances))
	for _, instance := range instances {
		rawInstances = append(rawInstances, instance)
	}
	return map[string]any{
		"mode":      "managed",
		"type":      "terraform_data",
		"name":      "scope_tag_policy",
		"provider":  "terraform.io/builtin/terraform",
		"instances": rawInstances,
	}
}

func rawScopeConfigurationResource(instances ...map[string]any) map[string]any {
	rawInstances := make([]any, 0, len(instances))
	for _, instance := range instances {
		rawInstances = append(rawInstances, instance)
	}
	return map[string]any{
		"mode":      "managed",
		"type":      "terraform_data",
		"name":      "scope_configuration",
		"provider":  "terraform.io/builtin/terraform",
		"instances": rawInstances,
	}
}

func appendRawTerraformStateResource(state map[string]any, resource map[string]any) {
	state["resources"] = append(state["resources"].([]any), resource)
}

func rawScopedAWSResource(scopeRef string) map[string]any {
	return map[string]any{
		"module":   `module.scoped_cross_account_iam["` + scopeRef + `"]`,
		"mode":     "managed",
		"type":     "aws_iam_role",
		"name":     "capa_nodes",
		"provider": `provider["registry.terraform.io/hashicorp/aws"]`,
		"instances": []any{map[string]any{
			"attributes": map[string]any{
				"id":   "ditto-capa-nodes-" + scopeRef,
				"name": "ditto-capa-nodes-" + scopeRef,
			},
		}},
	}
}

func writeTerraformStateTestFile(t *testing.T, state any) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "terraform.tfstate")
	content, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("unable to encode Terraform state fixture: %v", err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatalf("unable to write Terraform state fixture: %v", err)
	}
	return path
}

func TestLoadAWSStateScopeRegistry(t *testing.T) {
	t.Run("loads exact versioned identities", func(t *testing.T) {
		state := rawScopeRegistryState(
			rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
			rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, false),
		)
		appendRawTerraformStateResource(state, rawScopeTagPolicyResource(
			rawScopeTagPolicyInstance(testDefaultScopeRef, testDefaultScopeRef, 0),
			rawScopeTagPolicyInstance(testSecondaryScopeRef, testSecondaryScopeRef, 1),
		))
		defaultConfiguration := testScope(true)
		secondaryConfiguration := testScope(false)
		secondaryConfiguration.ClusterName = "secondary-eks"
		secondaryConfiguration.ClusterType = awsClusterTypeEKS
		secondaryConfiguration.ScopeTagPolicyVersion = 1
		appendRawTerraformStateResource(state, rawScopeConfigurationResource(
			rawScopeConfigurationInstance(testDefaultScopeRef, testDefaultScopeRef, defaultConfiguration),
			rawScopeConfigurationInstance(testSecondaryScopeRef, testSecondaryScopeRef, secondaryConfiguration),
		))
		statePath := writeTerraformStateTestFile(t, state)

		registry, err := loadAWSStateScopeRegistry(statePath)
		if err != nil {
			t.Fatalf("unexpected registry error: %v", err)
		}
		if !registry.Present || registry.StateEmpty || registry.DefaultScopeRef != testDefaultScopeRef || len(registry.Scopes) != 2 {
			t.Fatalf("unexpected registry: %#v", registry)
		}
		if registry.AppliedTagPolicyVersions[testDefaultScopeRef] != 0 || registry.AppliedTagPolicyVersions[testSecondaryScopeRef] != 1 {
			t.Fatalf("unexpected applied tag-policy versions: %#v", registry.AppliedTagPolicyVersions)
		}
		if !registry.ConfigurationPresent || len(registry.Configurations) != 2 || registry.Configurations[testSecondaryScopeRef].ClusterType != awsClusterTypeEKS {
			t.Fatalf("unexpected configuration snapshots: %#v", registry.Configurations)
		}
	})

	t.Run("treats an absent marker as applied policy version zero", func(t *testing.T) {
		statePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
			rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
		))

		registry, err := loadAWSStateScopeRegistry(statePath)
		if err != nil {
			t.Fatalf("unexpected registry error: %v", err)
		}
		if registry.AppliedTagPolicyVersions[testDefaultScopeRef] != 0 {
			t.Fatalf("absent marker did not default to policy version zero: %#v", registry.AppliedTagPolicyVersions)
		}
	})

	t.Run("treats missing state as empty greenfield state", func(t *testing.T) {
		registry, err := loadAWSStateScopeRegistry(filepath.Join(t.TempDir(), "missing.tfstate"))
		if err != nil {
			t.Fatalf("unexpected registry error: %v", err)
		}
		if registry.Present || !registry.StateEmpty {
			t.Fatalf("unexpected registry: %#v", registry)
		}
	})

	tests := []struct {
		name      string
		state     map[string]any
		mutate    func(map[string]any)
		wantError string
	}{
		{
			name: "rejects unsupported Terraform state version",
			state: rawScopeRegistryState(
				rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
			),
			mutate:    func(state map[string]any) { state["version"] = 5 },
			wantError: "unsupported format version 5",
		},
		{
			name: "rejects an address and stored reference mismatch",
			state: rawScopeRegistryState(
				rawScopeRegistryInstance(testDefaultScopeRef, testSecondaryScopeRef, true),
			),
			wantError: "does not match stored scope_ref",
		},
		{
			name: "rejects an unsupported registry schema version",
			state: rawScopeRegistryState(
				rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
			),
			mutate: func(state map[string]any) {
				resources := state["resources"].([]any)
				resource := resources[0].(map[string]any)
				instances := resource["instances"].([]any)
				instance := instances[0].(map[string]any)
				attributes := instance["attributes"].(map[string]any)
				input := attributes["input"].(map[string]any)
				identity := input["value"].(map[string]any)
				identity["schema_version"] = 2
			},
			wantError: "unsupported schema_version",
		},
		{
			name: "rejects duplicate registry keys",
			state: rawScopeRegistryState(
				rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
				rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, false),
			),
			wantError: "duplicate scope registry key",
		},
		{
			name: "rejects multiple defaults",
			state: rawScopeRegistryState(
				rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
				rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, true),
			),
			wantError: "exactly one default scope; found 2",
		},
		{
			name: "rejects apparent scoped resources without a registry",
			state: rawTerraformStateWithResources([]any{
				map[string]any{
					"module":    `module.scoped_vpc[\"` + testSecondaryScopeRef + `\"]`,
					"mode":      "managed",
					"type":      "aws_vpc",
					"name":      "this",
					"instances": []any{},
				},
			}),
			wantError: "apparent scope-mode resources but no valid root terraform_data.scope_registry",
		},
		{
			name: "rejects an applied tag-policy marker without a registry",
			state: rawTerraformStateWithResources([]any{
				rawScopeTagPolicyResource(
					rawScopeTagPolicyInstance(testDefaultScopeRef, testDefaultScopeRef, 0),
				),
			}),
			wantError: "apparent scope-mode resources but no valid root terraform_data.scope_registry",
		},
		{
			name: "rejects an applied tag-policy address and stored reference mismatch",
			state: func() map[string]any {
				state := rawScopeRegistryState(
					rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
				)
				appendRawTerraformStateResource(state, rawScopeTagPolicyResource(
					rawScopeTagPolicyInstance(testDefaultScopeRef, testSecondaryScopeRef, 0),
				))
				return state
			}(),
			wantError: "applied tag-policy address key",
		},
		{
			name: "rejects an unsupported applied tag-policy version",
			state: func() map[string]any {
				state := rawScopeRegistryState(
					rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
				)
				appendRawTerraformStateResource(state, rawScopeTagPolicyResource(
					rawScopeTagPolicyInstance(testDefaultScopeRef, testDefaultScopeRef, 2),
				))
				return state
			}(),
			wantError: "policy_version must be 0 or 1",
		},
		{
			name: "rejects an applied tag-policy marker for an unknown scope",
			state: func() map[string]any {
				state := rawScopeRegistryState(
					rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
				)
				appendRawTerraformStateResource(state, rawScopeTagPolicyResource(
					rawScopeTagPolicyInstance(testSecondaryScopeRef, testSecondaryScopeRef, 0),
				))
				return state
			}(),
			wantError: "applied tag-policy marker for unknown scope",
		},
		{
			name: "rejects a configuration snapshot without a registry",
			state: rawTerraformStateWithResources([]any{
				rawScopeConfigurationResource(
					rawScopeConfigurationInstance(testDefaultScopeRef, testDefaultScopeRef, testScope(true)),
				),
			}),
			wantError: "apparent scope-mode resources but no valid root terraform_data.scope_registry",
		},
		{
			name: "rejects a configuration address and stored reference mismatch",
			state: func() map[string]any {
				state := rawScopeRegistryState(
					rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
				)
				appendRawTerraformStateResource(state, rawScopeConfigurationResource(
					rawScopeConfigurationInstance(testDefaultScopeRef, testSecondaryScopeRef, testScope(true)),
				))
				return state
			}(),
			wantError: "scope configuration address key",
		},
		{
			name: "rejects an unsupported configuration snapshot schema",
			state: func() map[string]any {
				state := rawScopeRegistryState(
					rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
				)
				configurationResource := rawScopeConfigurationResource(
					rawScopeConfigurationInstance(testDefaultScopeRef, testDefaultScopeRef, testScope(true)),
				)
				instance := configurationResource["instances"].([]any)[0].(map[string]any)
				attributes := instance["attributes"].(map[string]any)
				input := attributes["input"].(map[string]any)
				input["value"].(map[string]any)["schema_version"] = awsScopeConfigurationSchemaVersion + 1
				appendRawTerraformStateResource(state, configurationResource)
				return state
			}(),
			wantError: "unsupported schema_version",
		},
		{
			name: "rejects a configuration snapshot for an unknown scope",
			state: func() map[string]any {
				state := rawScopeRegistryState(
					rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
				)
				appendRawTerraformStateResource(state, rawScopeConfigurationResource(
					rawScopeConfigurationInstance(testSecondaryScopeRef, testSecondaryScopeRef, testScope(false)),
				))
				return state
			}(),
			wantError: "configuration snapshot for unknown scope",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if test.mutate != nil {
				test.mutate(test.state)
			}
			statePath := writeTerraformStateTestFile(t, test.state)
			_, err := loadAWSStateScopeRegistry(statePath)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestAWSLegacyModeStatePreflightRunsBeforeTerraform(t *testing.T) {
	legacyStatePath := writeTerraformStateTestFile(t, rawTerraformStateWithResources([]any{
		map[string]any{
			"mode": "managed", "type": "aws_iam_role", "name": "legacy",
			"instances": []any{map[string]any{"attributes": map[string]any{"name": "legacy"}}},
		},
	}))
	registryStatePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	))
	apparentScopeStatePath := writeTerraformStateTestFile(t, rawTerraformStateWithResources([]any{
		map[string]any{
			"module":    `module.scoped_vpc[\"` + testSecondaryScopeRef + `\"]`,
			"mode":      "managed",
			"type":      "aws_vpc",
			"name":      "this",
			"instances": []any{},
		},
	}))

	tests := []struct {
		name          string
		statePath     string
		wantError     string
		wantTerraform bool
	}{
		{
			name:          "allows a genuine pre-scope state through the legacy path",
			statePath:     legacyStatePath,
			wantTerraform: true,
		},
		{
			name:      "rejects a registry-backed state when scope mode is omitted",
			statePath: registryStatePath,
			wantError: "is managed in AWS scope mode; rerun with --scopes=true",
		},
		{
			name:      "rejects apparent scope resources without a registry",
			statePath: apparentScopeStatePath,
			wantError: "apparent scope-mode resources but no valid root terraform_data.scope_registry",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			cmd, mock := setupBootstrapTest(t, []string{
				"aws",
				"--state=" + test.statePath,
				"--aws-profile=test-profile",
				"--dry-run",
			})
			err := cmd.Execute()
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got %v, want error containing %q", err, test.wantError)
				}
				assertCallCounts(t, mock, 0, 0, 0)
				return
			}
			if err != nil {
				t.Fatalf("unexpected legacy-mode error: %v", err)
			}
			if !test.wantTerraform {
				t.Fatal("test case must declare either wantError or wantTerraform")
			}
			assertCallCounts(t, mock, 1, 1, 0)
			if _, exists := mock.PlanVars["deployment_scopes"]; exists {
				t.Fatalf("legacy mode unexpectedly passed deployment_scopes: %#v", mock.PlanVars)
			}
		})
	}
}

func TestValidateAWSStateScopeLifecycle(t *testing.T) {
	registryStatePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
		rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, false),
	))
	allScopes := AWSDeploymentScopes{
		testDefaultScopeRef:   testScope(true),
		testSecondaryScopeRef: testScope(false),
	}
	legacyStatePath := writeTerraformStateTestFile(t, rawTerraformStateWithResources([]any{
		map[string]any{
			"mode": "managed", "type": "aws_iam_role", "name": "legacy",
			"instances": []any{map[string]any{"attributes": map[string]any{"name": "legacy"}}},
		},
	}))

	tests := []struct {
		name            string
		statePath       string
		desiredScopes   AWSDeploymentScopes
		allowedRemovals []string
		wantError       string
	}{
		{
			name:          "accepts greenfield scopes",
			statePath:     filepath.Join(t.TempDir(), "missing.tfstate"),
			desiredScopes: allScopes,
		},
		{
			name:          "accepts unchanged registry",
			statePath:     registryStatePath,
			desiredScopes: allScopes,
		},
		{
			name:          "requires the dedicated registry seed for legacy state",
			statePath:     legacyStatePath,
			desiredScopes: AWSDeploymentScopes{testDefaultScopeRef: testScope(true)},
			wantError:     "run bootstrap aws scopes migrate seed-registry",
		},
		{
			name:      "accepts additive change",
			statePath: registryStatePath,
			desiredScopes: AWSDeploymentScopes{
				testDefaultScopeRef:   testScope(true),
				testSecondaryScopeRef: testScope(false),
				testAddedScopeRef:     testScope(false),
			},
		},
		{
			name:      "requires exact removal authorization",
			statePath: registryStatePath,
			desiredScopes: AWSDeploymentScopes{
				testDefaultScopeRef: testScope(true),
			},
			wantError: "missing authorization: [" + testSecondaryScopeRef + "]",
		},
		{
			name:      "accepts exact non-default removal authorization",
			statePath: registryStatePath,
			desiredScopes: AWSDeploymentScopes{
				testDefaultScopeRef: testScope(true),
			},
			allowedRemovals: []string{testSecondaryScopeRef},
		},
		{
			name:            "rejects unused removal authorization",
			statePath:       registryStatePath,
			desiredScopes:   allScopes,
			allowedRemovals: []string{testSecondaryScopeRef},
			wantError:       "unused authorization: [" + testSecondaryScopeRef + "]",
		},
		{
			name:            "rejects duplicate removal authorization",
			statePath:       registryStatePath,
			desiredScopes:   allScopes,
			allowedRemovals: []string{testSecondaryScopeRef, testSecondaryScopeRef},
			wantError:       "duplicate --allow-scope-removal",
		},
		{
			name:            "rejects invalid removal authorization",
			statePath:       registryStatePath,
			desiredScopes:   allScopes,
			allowedRemovals: []string{"vpc-09e877f9012f52241"},
			wantError:       "is not a valid generated Dittocloud scope reference",
		},
		{
			name:      "rejects default reassignment",
			statePath: registryStatePath,
			desiredScopes: AWSDeploymentScopes{
				testDefaultScopeRef:   testScope(false),
				testSecondaryScopeRef: testScope(true),
			},
			wantError: "default scope " + `"` + testDefaultScopeRef + `"` + " is immutable",
		},
		{
			name:      "rejects simultaneous removal and addition",
			statePath: registryStatePath,
			desiredScopes: AWSDeploymentScopes{
				testDefaultScopeRef: testScope(true),
				testAddedScopeRef:   testScope(false),
			},
			allowedRemovals: []string{testSecondaryScopeRef},
			wantError:       "scope removal and addition cannot occur together",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := validateAWSStateScopeLifecycle(test.statePath, test.desiredScopes, test.allowedRemovals)
			if test.wantError == "" {
				if err != nil {
					t.Fatalf("unexpected lifecycle error: %v", err)
				}
				return
			}
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
		})
	}
}

func TestAWSScopesStatePreflightRunsBeforeTerraform(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
		rawScopeRegistryInstance(testSecondaryScopeRef, testSecondaryScopeRef, false),
	))
	desiredScopesPath := writeAWSScopeTestFile(t, `
`+testDefaultScopeRef+`:
  default: true
  region: ap-southeast-2
  vpc:
    mode: capi
`)

	tests := []struct {
		name          string
		statePath     string
		extraArgs     []string
		wantError     string
		wantTerraform bool
	}{
		{
			name:      "rejects an unauthorized omission",
			statePath: statePath,
			wantError: "missing authorization: [" + testSecondaryScopeRef + "]",
		},
		{
			name:          "accepts an exactly authorized omission and runs Terraform",
			statePath:     statePath,
			extraArgs:     []string{"--allow-scope-removal=" + testSecondaryScopeRef},
			wantTerraform: true,
		},
		{
			name: "rejects legacy state without a seeded registry",
			statePath: writeTerraformStateTestFile(t, rawTerraformStateWithResources([]any{map[string]any{
				"mode": "managed", "type": "aws_iam_role", "name": "legacy", "instances": []any{},
			}})),
			wantError: "run bootstrap aws scopes migrate seed-registry",
		},
		{
			name:      "rejects malformed state",
			statePath: writeTerraformStateTestFile(t, map[string]any{"version": supportedTerraformStateVersion, "resources": "not-an-array"}),
			wantError: "missing required field",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			args := []string{
				"aws",
				"--scopes=true",
				"--scopes-file=" + desiredScopesPath,
				"--state=" + test.statePath,
				"--dry-run",
			}
			args = append(args, test.extraArgs...)
			cmd, mock := setupBootstrapTest(t, args)
			err := cmd.Execute()
			if test.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), test.wantError) {
					t.Fatalf("got %v, want error containing %q", err, test.wantError)
				}
				assertCallCounts(t, mock, 0, 0, 0)
				return
			}
			if err != nil {
				t.Fatalf("unexpected scope-mode error: %v", err)
			}
			if !test.wantTerraform {
				t.Fatal("test case must declare either wantError or wantTerraform")
			}
			assertCallCounts(t, mock, 1, 1, 0)
		})
	}
}
