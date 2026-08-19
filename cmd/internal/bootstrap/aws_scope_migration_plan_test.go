package bootstrap

import (
	"bytes"
	"io"
	"reflect"
	"strings"
	"testing"

	tfjson "github.com/hashicorp/terraform-json"
)

const testNATGatewayName = "active-cluster-nat"

func terraformTags(values map[string]string) map[string]any {
	tags := make(map[string]any, len(values))
	for key, value := range values {
		tags[key] = value
	}
	return tags
}

func scopeMigrationTagChange(address, resourceType, resourceName string, beforeTags map[string]string) *tfjson.ResourceChange {
	afterTags := make(map[string]string, len(beforeTags)+1)
	for key, value := range beforeTags {
		afterTags[key] = value
	}
	afterTags["ditto.live/scope-ref"] = testDefaultScopeRef
	return &tfjson.ResourceChange{
		Address: address,
		Mode:    tfjson.ManagedResourceMode,
		Type:    resourceType,
		Name:    resourceName,
		Change: &tfjson.Change{
			Actions: tfjson.Actions{tfjson.ActionUpdate},
			Before: map[string]any{
				"id":       address + "-id",
				"tags":     terraformTags(beforeTags),
				"tags_all": terraformTags(beforeTags),
			},
			After: map[string]any{
				"id":       address + "-id",
				"tags":     terraformTags(afterTags),
				"tags_all": terraformTags(afterTags),
			},
		},
	}
}

func exactAWSInitialScopeMigrationPlan() *tfjson.Plan {
	complete := true
	natTags := map[string]string{
		"Name": testNATGatewayName,
		"sigs.k8s.io/cluster-api-provider-aws/cluster/cluster-a": "owned",
		"sigs.k8s.io/cluster-api-provider-aws/cluster/cluster-b": "owned",
		"sigs.k8s.io/cluster-api-provider-aws/role":              "common",
	}
	subnetTags := map[string]string{
		"Name":                            "public-ap-southeast-2a",
		"kubernetes.io/cluster/cluster-a": "shared",
		"kubernetes.io/cluster/cluster-b": "shared",
		"kubernetes.io/role/elb":          "1",
		"ditto.live/managed_by":           "dittocloud",
	}
	legacyOutput := map[string]any{
		"account_id": "123456789012",
		"region":     "ap-southeast-2",
		"vpc":        []any{map[string]any{"vpc_id": "vpc-00000000000000001"}},
	}
	scopeOutput := map[string]any{
		"account_id":        legacyOutput["account_id"],
		"region":            legacyOutput["region"],
		"vpc":               legacyOutput["vpc"],
		"regionalResources": map[string]any{"ap-southeast-2": map[string]any{}},
		"scopes":            map[string]any{testDefaultScopeRef: map[string]any{"region": "ap-southeast-2"}},
	}

	return &tfjson.Plan{
		Complete: &complete,
		ResourceChanges: []*tfjson.ResourceChange{
			{
				Address:      `terraform_data.scope_tag_policy["` + testDefaultScopeRef + `"]`,
				Mode:         tfjson.ManagedResourceMode,
				Type:         "terraform_data",
				Name:         "scope_tag_policy",
				ProviderName: terraformBuiltinProviderName,
				Index:        testDefaultScopeRef,
				Change: &tfjson.Change{
					Actions: tfjson.Actions{tfjson.ActionCreate},
					After: map[string]any{
						"input": map[string]any{
							"schema_version": 1,
							"scope_ref":      testDefaultScopeRef,
							"policy_version": 0,
						},
					},
				},
			},
			scopeMigrationTagChange(
				"module.vpc[0].module.vpc.aws_nat_gateway.this[0]",
				"aws_nat_gateway",
				"this",
				natTags,
			),
			scopeMigrationTagChange(
				"module.vpc[0].module.vpc.aws_subnet.public[0]",
				"aws_subnet",
				"public",
				subnetTags,
			),
			{
				Address:      `terraform_data.scope_configuration["` + testDefaultScopeRef + `"]`,
				Mode:         tfjson.ManagedResourceMode,
				Type:         "terraform_data",
				Name:         "scope_configuration",
				ProviderName: terraformBuiltinProviderName,
				Index:        testDefaultScopeRef,
				Change: &tfjson.Change{
					Actions: tfjson.Actions{tfjson.ActionCreate},
					After: map[string]any{
						"input": map[string]any{
							"schema_version": awsScopeConfigurationSchemaVersion,
							"scope_ref":      testDefaultScopeRef,
							"configuration": map[string]any{
								"default":                  true,
								"cluster_name":             nil,
								"cluster_type":             awsClusterTypeKubeadm,
								"region":                   "ap-southeast-2",
								"scope_tag_policy_version": 0,
								"vpc": map[string]any{
									"mode":                           awsVPCModeDittocloud,
									"name":                           "ditto-k8s",
									"cidr":                           "10.210.0.0/16",
									"secondary_cidr":                 nil,
									"public_subnet_netmask":          awsLegacyPublicSubnetNetmask,
									"private_subnet_netmask":         awsLegacyPrivateSubnetNetmask,
									"id":                             nil,
									"nat_gateway_name":               testNATGatewayName,
									"nat_gateway_eip_allocation_ids": []any{},
								},
							},
						},
					},
				},
			},
		},
		OutputChanges: map[string]*tfjson.Change{
			"aws": {
				Actions: tfjson.Actions{tfjson.ActionUpdate},
				Before:  legacyOutput,
				After:   scopeOutput,
			},
		},
	}
}

func testInitialScopeMigrationConfiguration() awsInitialScopeMigrationPlanConfiguration {
	return awsInitialScopeMigrationPlanConfiguration{
		ScopeRef:       testDefaultScopeRef,
		NATGatewayName: testNATGatewayName,
		Scope: AWSDeploymentScope{
			Default:               true,
			ClusterType:           awsClusterTypeKubeadm,
			Region:                "ap-southeast-2",
			ScopeTagPolicyVersion: 0,
			VPC: AWSScopeVPC{
				Mode:                 awsVPCModeDittocloud,
				Name:                 "ditto-k8s",
				CIDR:                 "10.210.0.0/16",
				PublicSubnetNetmask:  awsLegacyPublicSubnetNetmask,
				PrivateSubnetNetmask: awsLegacyPrivateSubnetNetmask,
				NATGatewayName:       testNATGatewayName,
			},
		},
	}
}

func TestValidateAWSInitialScopeMigrationPlanAcceptsOnlyAdditiveMigration(t *testing.T) {
	plan := exactAWSInitialScopeMigrationPlan()
	plan.ResourceDrift = []*tfjson.ResourceChange{
		{
			Address: "module.vpc[0].module.vpc.aws_nat_gateway.this[0]",
			Change: &tfjson.Change{
				Actions: tfjson.Actions{tfjson.ActionUpdate},
				Before: map[string]any{
					"id": "nat-1",
					"tags": terraformTags(map[string]string{
						"Name": "legacy-vpc-ap-southeast-2a",
					}),
					"tags_all": terraformTags(map[string]string{
						"Name": "legacy-vpc-ap-southeast-2a",
					}),
				},
				After: map[string]any{
					"id": "nat-1",
					"tags": terraformTags(map[string]string{
						"Name": testNATGatewayName,
						"sigs.k8s.io/cluster-api-provider-aws/cluster/cluster-a": "owned",
						"sigs.k8s.io/cluster-api-provider-aws/cluster/cluster-b": "owned",
					}),
					"tags_all": terraformTags(map[string]string{
						"Name": testNATGatewayName,
						"sigs.k8s.io/cluster-api-provider-aws/cluster/cluster-a": "owned",
						"sigs.k8s.io/cluster-api-provider-aws/cluster/cluster-b": "owned",
					}),
				},
			},
		},
	}

	if err := validateAWSInitialScopeMigrationPlan(plan, testInitialScopeMigrationConfiguration()); err != nil {
		t.Fatalf("expected exact additive migration plan to pass: %v", err)
	}
}

func TestValidateAWSInitialScopeMigrationPlanAcceptsExistingAppliedConfigurationSnapshot(t *testing.T) {
	plan := exactAWSInitialScopeMigrationPlan()
	plan.ResourceChanges = plan.ResourceChanges[:len(plan.ResourceChanges)-1]
	configuration := testInitialScopeMigrationConfiguration()
	configuration.ConfigurationSnapshotPresent = true

	if err := validateAWSInitialScopeMigrationPlan(plan, configuration); err != nil {
		t.Fatalf("expected a safely retained configuration snapshot to pass: %v", err)
	}
}

func TestPrepareAWSInitialScopeMigrationPlanRetainsPartialSnapshotEvidence(t *testing.T) {
	configuration := testInitialScopeMigrationConfiguration()
	state := rawScopeRegistryState(
		rawScopeRegistryInstance(testDefaultScopeRef, testDefaultScopeRef, true),
	)
	appendRawTerraformStateResource(state, rawScopeConfigurationResource(
		rawScopeConfigurationInstance(testDefaultScopeRef, testDefaultScopeRef, configuration.Scope),
	))
	statePath := writeTerraformStateTestFile(t, state)

	prepared, initialMigration, err := prepareAWSInitialScopeMigrationPlanConfiguration(
		statePath,
		AWSDeploymentScopes{testDefaultScopeRef: configuration.Scope},
	)
	if err != nil {
		t.Fatalf("unexpected partial migration preparation error: %v", err)
	}
	if !initialMigration || !prepared.ConfigurationSnapshotPresent || !reflect.DeepEqual(prepared.Scope, configuration.Scope) {
		t.Fatalf("unexpected prepared migration configuration: %#v", prepared)
	}
}

func TestValidateAWSInitialScopeMigrationPlanAcceptsEmptyTagMapNormalization(t *testing.T) {
	plan := exactAWSInitialScopeMigrationPlan()
	plan.ResourceDrift = []*tfjson.ResourceChange{
		{
			Address: "module.cross_account_iam[0].aws_iam_instance_profile.capa_control_plane",
			Change: &tfjson.Change{
				Actions: tfjson.Actions{tfjson.ActionUpdate},
				Before: map[string]any{
					"id":       "control-plane.cluster-api-provider-aws.sigs.k8s.io",
					"tags":     nil,
					"tags_all": map[string]any{},
				},
				After: map[string]any{
					"id":       "control-plane.cluster-api-provider-aws.sigs.k8s.io",
					"tags":     map[string]any{},
					"tags_all": map[string]any{},
				},
			},
		},
	}

	if err := validateAWSInitialScopeMigrationPlan(plan, testInitialScopeMigrationConfiguration()); err != nil {
		t.Fatalf("expected empty tag map normalization to pass: %v", err)
	}
}

func TestValidateAWSInitialScopeMigrationPlanAcceptsComputedPolicyAttachmentCountDrift(t *testing.T) {
	plan := exactAWSInitialScopeMigrationPlan()
	address := "module.cross_account_iam[0].aws_iam_policy.capa_control_plane"
	plannedChange := scopeMigrationTagChange(
		address,
		"aws_iam_policy",
		"capa_control_plane",
		map[string]string{"ditto.live/managed_by": "dittocloud"},
	)
	plannedChange.Change.Before.(map[string]any)["attachment_count"] = 1
	plannedChange.Change.Before.(map[string]any)["policy"] = `{"Version":"2012-10-17"}`
	plannedChange.Change.After.(map[string]any)["attachment_count"] = 1
	plannedChange.Change.After.(map[string]any)["policy"] = `{"Version":"2012-10-17"}`
	plan.ResourceChanges = append(plan.ResourceChanges, plannedChange)
	plan.ResourceDrift = []*tfjson.ResourceChange{
		{
			Address: address,
			Mode:    tfjson.ManagedResourceMode,
			Type:    "aws_iam_policy",
			Name:    "capa_control_plane",
			Change: &tfjson.Change{
				Actions: tfjson.Actions{tfjson.ActionUpdate},
				Before: map[string]any{
					"id":               address + "-id",
					"attachment_count": 0,
					"policy":           `{"Version":"2012-10-17"}`,
					"tags":             terraformTags(map[string]string{"ditto.live/managed_by": "dittocloud"}),
					"tags_all":         terraformTags(map[string]string{"ditto.live/managed_by": "dittocloud"}),
				},
				After: plannedChange.Change.Before,
			},
		},
	}

	if err := validateAWSInitialScopeMigrationPlan(plan, testInitialScopeMigrationConfiguration()); err != nil {
		t.Fatalf("expected computed IAM policy attachment count drift to pass: %v", err)
	}
}

func TestValidateAWSInitialScopeMigrationPlanAcceptsRefreshOnlyNoOpDrift(t *testing.T) {
	plan := exactAWSInitialScopeMigrationPlan()
	address := "module.cross_account_iam[0].module.iam_admin_view_role.aws_iam_role.this[0]"
	refreshed := map[string]any{
		"id":                  "iam-admin-view.ditto.live",
		"managed_policy_arns": []any{"arn:aws:iam::aws:policy/ReadOnlyAccess"},
		"tags":                map[string]any{},
		"tags_all":            map[string]any{},
	}
	plan.ResourceChanges = append(plan.ResourceChanges, &tfjson.ResourceChange{
		Address: address,
		Mode:    tfjson.ManagedResourceMode,
		Type:    "aws_iam_role",
		Name:    "this",
		Change: &tfjson.Change{
			Actions: tfjson.Actions{tfjson.ActionNoop},
			Before:  refreshed,
			After:   refreshed,
		},
	})
	plan.ResourceDrift = []*tfjson.ResourceChange{
		{
			Address: address,
			Mode:    tfjson.ManagedResourceMode,
			Type:    "aws_iam_role",
			Name:    "this",
			Change: &tfjson.Change{
				Actions: tfjson.Actions{tfjson.ActionUpdate},
				Before: map[string]any{
					"id":                  "iam-admin-view.ditto.live",
					"managed_policy_arns": nil,
					"tags":                nil,
					"tags_all":            map[string]any{},
				},
				After: refreshed,
			},
		},
	}

	if err := validateAWSInitialScopeMigrationPlan(plan, testInitialScopeMigrationConfiguration()); err != nil {
		t.Fatalf("expected refresh-only no-op drift to pass: %v", err)
	}
}

func TestValidateAWSInitialScopeMigrationPlanRejectsUnsafeChanges(t *testing.T) {
	tests := []struct {
		name      string
		mutate    func(*tfjson.Plan, *awsInitialScopeMigrationPlanConfiguration)
		wantError string
	}{
		{
			name: "cluster tag removal",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				natAfter := plan.ResourceChanges[1].Change.After.(map[string]any)
				delete(natAfter["tags"].(map[string]any), "sigs.k8s.io/cluster-api-provider-aws/cluster/cluster-b")
			},
			wantError: "removes or changes tag",
		},
		{
			name: "non-tag resource change",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				plan.ResourceChanges[2].Change.After.(map[string]any)["id"] = "replacement-id"
			},
			wantError: "attribute other than tags",
		},
		{
			name: "resource create",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				plan.ResourceChanges = append(plan.ResourceChanges, &tfjson.ResourceChange{
					Address: "aws_vpc.unexpected",
					Mode:    tfjson.ManagedResourceMode,
					Change:  &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionCreate}},
				})
			},
			wantError: "unsupported action",
		},
		{
			name: "missing applied configuration snapshot",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				plan.ResourceChanges = plan.ResourceChanges[:len(plan.ResourceChanges)-1]
			},
			wantError: "exactly one applied configuration snapshot; found 0",
		},
		{
			name: "configuration snapshot differs from reviewed scope",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				snapshot := plan.ResourceChanges[len(plan.ResourceChanges)-1]
				input := snapshot.Change.After.(map[string]any)["input"].(map[string]any)
				input["configuration"].(map[string]any)["cluster_type"] = awsClusterTypeEKS
			},
			wantError: "does not exactly match the reviewed scope configuration",
		},
		{
			name: "legacy output mutation",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				plan.OutputChanges["aws"].After.(map[string]any)["region"] = "us-east-1"
			},
			wantError: "changes legacy aws output field",
		},
		{
			name: "wrong NAT gateway name",
			mutate: func(_ *tfjson.Plan, configuration *awsInitialScopeMigrationPlanConfiguration) {
				configuration.NATGatewayName = "different-nat-name"
			},
			wantError: "expected NAT gateway Name",
		},
		{
			name: "unreviewed live drift",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				plan.ResourceDrift = []*tfjson.ResourceChange{{
					Address: "aws_iam_role.unexpected",
					Change:  &tfjson.Change{Actions: tfjson.Actions{tfjson.ActionUpdate}},
				}}
			},
			wantError: "non-tag live drift",
		},
		{
			name: "non-vpc live tag drift",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				plan.ResourceDrift = []*tfjson.ResourceChange{{
					Address: "module.cross_account_iam[0].aws_iam_instance_profile.capa_control_plane",
					Change: &tfjson.Change{
						Actions: tfjson.Actions{tfjson.ActionUpdate},
						Before: map[string]any{
							"id":   "control-plane.cluster-api-provider-aws.sigs.k8s.io",
							"tags": map[string]any{"owner": "platform"},
						},
						After: map[string]any{
							"id":   "control-plane.cluster-api-provider-aws.sigs.k8s.io",
							"tags": map[string]any{},
						},
					},
				}}
			},
			wantError: "unreviewed live drift",
		},
		{
			name: "non-vpc non-tag live drift",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				plan.ResourceDrift = []*tfjson.ResourceChange{{
					Address: "module.cross_account_iam[0].aws_iam_instance_profile.capa_control_plane",
					Change: &tfjson.Change{
						Actions: tfjson.Actions{tfjson.ActionUpdate},
						Before:  map[string]any{"id": "profile-before", "tags": nil},
						After:   map[string]any{"id": "profile-after", "tags": map[string]any{}},
					},
				}}
			},
			wantError: "non-tag live drift",
		},
		{
			name: "iam policy document reconciliation",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				address := "module.cross_account_iam[0].aws_iam_policy.capa_control_plane"
				plannedChange := scopeMigrationTagChange(address, "aws_iam_policy", "capa_control_plane", nil)
				plannedChange.Change.Before.(map[string]any)["attachment_count"] = 1
				plannedChange.Change.Before.(map[string]any)["policy"] = `{"Effect":"Allow"}`
				plannedChange.Change.After.(map[string]any)["attachment_count"] = 1
				plannedChange.Change.After.(map[string]any)["policy"] = `{"Effect":"Deny"}`
				plan.ResourceChanges = append(plan.ResourceChanges, plannedChange)
				plan.ResourceDrift = []*tfjson.ResourceChange{{
					Address: address,
					Mode:    tfjson.ManagedResourceMode,
					Type:    "aws_iam_policy",
					Change: &tfjson.Change{
						Actions: tfjson.Actions{tfjson.ActionUpdate},
						Before: map[string]any{
							"id":               address + "-id",
							"attachment_count": 0,
							"policy":           `{"Effect":"Deny"}`,
							"tags":             map[string]any{},
							"tags_all":         map[string]any{},
						},
						After: plannedChange.Change.Before,
					},
				}}
			},
			wantError: "non-tag live drift",
		},
		{
			name: "iam policy refreshed value mismatch",
			mutate: func(plan *tfjson.Plan, _ *awsInitialScopeMigrationPlanConfiguration) {
				address := "module.cross_account_iam[0].aws_iam_policy.capa_control_plane"
				plannedChange := scopeMigrationTagChange(address, "aws_iam_policy", "capa_control_plane", nil)
				plannedChange.Change.Before.(map[string]any)["attachment_count"] = 2
				plannedChange.Change.After.(map[string]any)["attachment_count"] = 2
				plan.ResourceChanges = append(plan.ResourceChanges, plannedChange)
				plan.ResourceDrift = []*tfjson.ResourceChange{{
					Address: address,
					Mode:    tfjson.ManagedResourceMode,
					Type:    "aws_iam_policy",
					Change: &tfjson.Change{
						Actions: tfjson.Actions{tfjson.ActionUpdate},
						Before: map[string]any{
							"id":               address + "-id",
							"attachment_count": 0,
							"tags":             map[string]any{},
							"tags_all":         map[string]any{},
						},
						After: map[string]any{
							"id":               address + "-id",
							"attachment_count": 1,
							"tags":             map[string]any{},
							"tags_all":         map[string]any{},
						},
					},
				}}
			},
			wantError: "non-tag live drift",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			plan := exactAWSInitialScopeMigrationPlan()
			configuration := testInitialScopeMigrationConfiguration()
			test.mutate(plan, &configuration)
			err := validateAWSInitialScopeMigrationPlan(plan, configuration)
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("expected error containing %q, got %v", test.wantError, err)
			}
		})
	}
}

func writeInitialScopeMigrationScopesFile(t *testing.T) string {
	t.Helper()
	return writeAWSScopeTestFile(t, testDefaultScopeRef+`:
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
    natGatewayName: `+testNATGatewayName+`
`)
}

func TestAWSInitialScopeMigrationDryRunValidatesSavedPlanWithoutApplying(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedAppliedState())
	scopesPath := writeInitialScopeMigrationScopesFile(t)
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
		"--dry-run",
	})
	mock.showPlanReturn = exactAWSInitialScopeMigrationPlan()
	var output bytes.Buffer
	cmd.SetOut(&output)
	cmd.SetErr(&output)

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected initial migration dry-run error: %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 0)
	if mock.showPlanCallCount != 1 || mock.showPlanStdout != io.Discard {
		t.Fatalf("saved plan was not inspected quietly: calls=%d stdout=%T", mock.showPlanCallCount, mock.showPlanStdout)
	}
	if !strings.HasSuffix(mock.planOutPath, "initial-default-scope-migration.tfplan") {
		t.Fatalf("initial migration plan was not saved to the guarded path: %q", mock.planOutPath)
	}
	if !strings.Contains(output.String(), "natGatewayName="+testNATGatewayName) {
		t.Fatalf("scope summary omitted the stable NAT gateway Name:\n%s", output.String())
	}
}

func TestAWSInitialScopeMigrationApplyUsesExactValidatedPlan(t *testing.T) {
	originalPrompt := terraformApplyPrompt
	terraformApplyPrompt = func(label, defaultValue string) string { return "yes" }
	t.Cleanup(func() { terraformApplyPrompt = originalPrompt })

	statePath := writeTerraformStateTestFile(t, rawAWSRegistrySeedAppliedState())
	scopesPath := writeInitialScopeMigrationScopesFile(t)
	cmd, mock := setupBootstrapTest(t, []string{
		"aws",
		"--scopes=true",
		"--scopes-file=" + scopesPath,
		"--state=" + statePath,
	})
	mock.showPlanReturn = exactAWSInitialScopeMigrationPlan()

	if err := cmd.Execute(); err != nil {
		t.Fatalf("unexpected initial migration apply error: %v", err)
	}
	assertCallCounts(t, mock, 1, 1, 1)
	if mock.planOutPath == "" || mock.applyPlanPath != mock.planOutPath {
		t.Fatalf("validated saved plan was not applied exactly: planned=%q applied=%q", mock.planOutPath, mock.applyPlanPath)
	}
}
