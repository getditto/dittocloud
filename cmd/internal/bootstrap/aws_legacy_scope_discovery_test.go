package bootstrap

import (
	"encoding/json"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func rawAWSLegacyOutput(region string, vpcIDs ...string) map[string]any {
	vpcs := make([]any, 0, len(vpcIDs))
	for _, vpcID := range vpcIDs {
		vpcs = append(vpcs, map[string]any{
			"vpc_id": vpcID,
			"vpc":    map[string]any{"vpc_id": vpcID},
		})
	}
	return map[string]any{
		"value": map[string]any{
			"region": region,
			"vpc":    vpcs,
			"secret": "must-not-be-read",
		},
	}
}

func rawAWSLegacyVPCValidationResource(customerManaged bool, vpcID any) map[string]any {
	return map[string]any{
		"mode": "managed",
		"type": "terraform_data",
		"name": "validate_vpc_mode",
		"instances": []any{
			map[string]any{
				"attributes": map[string]any{
					"input": map[string]any{
						"value": map[string]any{
							"customer_managed_vpc": customerManaged,
							"vpc_id":               vpcID,
						},
						"type": []any{
							"object",
							map[string]string{
								"customer_managed_vpc": "bool",
								"vpc_id":               "string",
							},
						},
					},
				},
			},
		},
	}
}

func rawAWSLegacyManagedVPCResource(name, cidr string) map[string]any {
	return map[string]any{
		"module": "module.vpc[0].module.vpc",
		"mode":   "managed",
		"type":   "aws_vpc",
		"name":   "this",
		"instances": []any{
			map[string]any{
				"attributes": map[string]any{
					"cidr_block": cidr,
					"tags":       map[string]any{"Name": name},
					"secret":     "must-not-be-read",
				},
			},
		},
	}
}

func rawAWSLegacyEKSMarkerResource() map[string]any {
	return map[string]any{
		"module": "module.cross_account_iam[0]",
		"mode":   "managed",
		"type":   "aws_iam_role",
		"name":   "capa_eks_control_plane",
		"instances": []any{
			map[string]any{"attributes": map[string]any{}},
		},
	}
}

func rawAWSLegacyPhaseTwoPolicyResource(name, clusterName string) map[string]any {
	policy, _ := json.Marshal(map[string]any{
		"Version": "2012-10-17",
		"Statement": []any{
			map[string]any{
				"Effect": "Allow",
				"Condition": map[string]any{
					"StringEquals": map[string]any{
						"aws:RequestTag/kubernetes.io/cluster/" + clusterName: "owned",
					},
				},
			},
		},
	})
	return map[string]any{
		"module": "module.cross_account_iam[0]",
		"mode":   "managed",
		"type":   "aws_iam_policy",
		"name":   name,
		"instances": []any{
			map[string]any{
				"attributes": map[string]any{"policy": string(policy)},
			},
		},
	}
}

func decodeRawTerraformStateFixture(t *testing.T, state map[string]any) rawTerraformState {
	t.Helper()
	encoded, err := json.Marshal(state)
	if err != nil {
		t.Fatalf("unable to encode state fixture: %v", err)
	}
	var decoded rawTerraformState
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		t.Fatalf("unable to decode raw state fixture: %v", err)
	}
	return decoded
}

func legacyStateFixture(outputs map[string]any, resources ...map[string]any) map[string]any {
	rawResources := make([]any, 0, len(resources))
	for _, resource := range resources {
		rawResources = append(rawResources, resource)
	}
	state := rawTerraformStateWithResources(rawResources)
	state["outputs"] = outputs
	return state
}

func TestDiscoverAWSLegacyScopeUsesOnlyAcceptedEvidence(t *testing.T) {
	state := decodeRawTerraformStateFixture(t, legacyStateFixture(
		map[string]any{"aws": rawAWSLegacyOutput("ap-southeast-2", "vpc-09e877f9012f52241")},
		rawAWSLegacyVPCValidationResource(false, nil),
		rawAWSLegacyManagedVPCResource("ditto-default", "10.210.0.0/16"),
		rawAWSLegacyEKSMarkerResource(),
		rawAWSLegacyPhaseTwoPolicyResource("capa_controller_base", "legacy-cluster"),
	))

	discovery, err := discoverAWSLegacyScope(state)
	if err != nil {
		t.Fatalf("unexpected discovery error: %v", err)
	}
	if len(discovery.Missing) != 0 {
		t.Fatalf("unexpected missing fields: %v", discovery.Missing)
	}
	want := AWSDeploymentScope{
		Default:               true,
		ClusterName:           "legacy-cluster",
		ClusterType:           awsClusterTypeEKS,
		Region:                "ap-southeast-2",
		ScopeTagPolicyVersion: 0,
		VPC: AWSScopeVPC{
			Mode: awsVPCModeDittocloud,
			Name: "ditto-default",
			CIDR: "10.210.0.0/16",
		},
	}
	if !reflect.DeepEqual(discovery.Scope, want) {
		t.Fatalf("discovered scope: got %#v, want %#v", discovery.Scope, want)
	}
	if strings.Contains(strings.Join(discovery.Evidence[legacyScopeFieldRegion].Sources, " "), "secret") {
		t.Fatalf("unexpected unrelated state evidence: %#v", discovery.Evidence)
	}
}

func TestDiscoverAWSLegacyScopeModesAndMissingFields(t *testing.T) {
	tests := []struct {
		name        string
		state       map[string]any
		wantMode    string
		wantID      string
		wantMissing []string
	}{
		{
			name: "existing VPC with EKS evidence is complete",
			state: legacyStateFixture(
				map[string]any{"aws": rawAWSLegacyOutput("us-west-2")},
				rawAWSLegacyVPCValidationResource(true, "vpc-09e877f9012f52241"),
				rawAWSLegacyEKSMarkerResource(),
			),
			wantMode: awsVPCModeExisting,
			wantID:   "vpc-09e877f9012f52241",
		},
		{
			name: "CAPI mode still requires explicit kubeadm confirmation",
			state: legacyStateFixture(
				map[string]any{"aws": rawAWSLegacyOutput("ap-southeast-2")},
				rawAWSLegacyVPCValidationResource(false, nil),
			),
			wantMode:    awsVPCModeCAPI,
			wantMissing: []string{legacyScopeFieldClusterType},
		},
		{
			name: "absent accepted evidence reports required fields",
			state: legacyStateFixture(
				map[string]any{},
				map[string]any{
					"mode": "managed", "type": "aws_iam_role", "name": "unrelated",
					"instances": []any{map[string]any{"attributes": map[string]any{"secret": "ignored"}}},
				},
			),
			wantMissing: []string{legacyScopeFieldRegion, legacyScopeFieldVPCMode, legacyScopeFieldClusterType},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			discovery, err := discoverAWSLegacyScope(decodeRawTerraformStateFixture(t, test.state))
			if err != nil {
				t.Fatalf("unexpected discovery error: %v", err)
			}
			if discovery.Scope.VPC.Mode != test.wantMode || discovery.Scope.VPC.ID != test.wantID {
				t.Fatalf("unexpected VPC discovery: %#v", discovery.Scope.VPC)
			}
			if !slices.Equal(discovery.Missing, test.wantMissing) {
				t.Fatalf("missing fields: got %v, want %v", discovery.Missing, test.wantMissing)
			}
		})
	}
}

func TestDiscoverAWSLegacyScopeRejectsConflictingOrMalformedEvidence(t *testing.T) {
	tests := []struct {
		name      string
		state     map[string]any
		wantError string
	}{
		{
			name: "conflicting VPC IDs",
			state: legacyStateFixture(
				map[string]any{"aws": rawAWSLegacyOutput("us-west-2", "vpc-09e877f9012f52241")},
				rawAWSLegacyVPCValidationResource(true, "vpc-08bb5e202f9111034"),
				rawAWSLegacyEKSMarkerResource(),
			),
			wantError: "conflicting legacy state evidence for vpc.id",
		},
		{
			name: "managed VPC conflicts with customer-managed validation",
			state: legacyStateFixture(
				map[string]any{"aws": rawAWSLegacyOutput("us-west-2")},
				rawAWSLegacyVPCValidationResource(true, "vpc-09e877f9012f52241"),
				rawAWSLegacyManagedVPCResource("ditto", "10.210.0.0/16"),
				rawAWSLegacyEKSMarkerResource(),
			),
			wantError: "conflicting legacy state evidence for vpc.mode",
		},
		{
			name: "conflicting phase-two cluster names",
			state: legacyStateFixture(
				map[string]any{"aws": rawAWSLegacyOutput("us-west-2")},
				rawAWSLegacyVPCValidationResource(false, nil),
				rawAWSLegacyEKSMarkerResource(),
				rawAWSLegacyPhaseTwoPolicyResource("capa_controller_base", "cluster-one"),
				rawAWSLegacyPhaseTwoPolicyResource("capa_controller_elb", "cluster-two"),
			),
			wantError: "conflicting legacy state evidence for clusterName",
		},
		{
			name: "malformed accepted output",
			state: legacyStateFixture(
				map[string]any{"aws": map[string]any{"value": map[string]any{"region": 42, "vpc": []any{}}}},
				rawAWSLegacyVPCValidationResource(false, nil),
				rawAWSLegacyEKSMarkerResource(),
			),
			wantError: "output aws.region must be a string",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := discoverAWSLegacyScope(decodeRawTerraformStateFixture(t, test.state))
			if err == nil || !strings.Contains(err.Error(), test.wantError) {
				t.Fatalf("got %v, want error containing %q", err, test.wantError)
			}
		})
	}
}
