package bootstrap

import (
	"fmt"
	"strings"
	"testing"
)

func TestAWSGeneratedScopeNameContractsMatchAcceptedTemplates(t *testing.T) {
	scopeRef := "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
	scope := AWSDeploymentScope{ClusterType: awsClusterTypeEKS, Region: "ap-southeast-2"}
	contracts := awsGeneratedScopeNameContracts(scopeRef, scope)
	wantNames := map[string]string{
		"controller_role":                 "ditto-capa-controller-" + scopeRef,
		"trust_editor_role":               "ditto-iam-trust-editor-" + scopeRef,
		"nodes_role":                      "ditto-capa-nodes-" + scopeRef,
		"nodes_instance_profile":          "ditto-capa-nodes-" + scopeRef,
		"control_plane_role":              "ditto-capa-control-plane-" + scopeRef,
		"control_plane_instance_profile":  "ditto-capa-control-plane-" + scopeRef,
		"eks_control_plane_role":          "ditto-capa-eks-control-plane-" + scopeRef,
		"trust_editor_policy":             "ditto-iam-trust-editor-policy-" + scopeRef,
		"cluster_resources_boundary":      "ditto-cluster-resources-boundary-" + scopeRef,
		"cluster_external_boundary":       "ditto-cluster-external-boundary-" + scopeRef,
		"nodes_policy":                    "ditto-capa-nodes-policy-" + scopeRef,
		"control_plane_policy":            "ditto-capa-control-plane-policy-" + scopeRef,
		"control_plane_tags_policy":       "ditto-capa-control-plane-tags-" + scopeRef,
		"controller_base_policy":          "ditto-capa-controller-base-" + scopeRef,
		"controller_network_policy":       "ditto-capa-controller-network-" + scopeRef,
		"controller_elb_policy":           "ditto-capa-controller-elb-" + scopeRef,
		"controller_vpc_lifecycle_policy": "ditto-capa-controller-vpc-lifecycle-" + scopeRef,
		"controller_eks_policy":           "ditto-capa-controller-eks-" + scopeRef,
		"karpenter_queue":                 "karpenter-interruption-" + scopeRef,
		"karpenter_spot_interruption":     "KarpenterSpotInterruption-" + scopeRef,
		"karpenter_rebalance":             "KarpenterRebalance-" + scopeRef,
		"karpenter_instance_state":        "KarpenterInstanceState-" + scopeRef,
		"karpenter_health":                "KarpenterHealth-" + scopeRef,
	}

	if len(contracts) != len(wantNames) {
		t.Fatalf("got %d generated-name contracts, want %d", len(contracts), len(wantNames))
	}
	for _, contract := range contracts {
		wantName, exists := wantNames[contract.Key]
		if !exists {
			t.Errorf("unexpected generated-name contract %q", contract.Key)
			continue
		}
		if contract.Name != wantName {
			t.Errorf("%s: got name %q, want %q", contract.Key, contract.Name, wantName)
		}
		if err := validateAWSGeneratedScopeName(scopeRef, contract); err != nil {
			t.Errorf("%s rejected its accepted name: %v", contract.Key, err)
		}
		delete(wantNames, contract.Key)
	}
	if len(wantNames) != 0 {
		t.Fatalf("missing generated-name contracts: %v", wantNames)
	}
}

func TestAWSGeneratedScopeNameContractsMatchTerraformForKubeadm(t *testing.T) {
	contracts := awsGeneratedScopeNameContracts(
		"dsc-01k2m8g7n4p6q9r3t5v8x1y2z3",
		AWSDeploymentScope{ClusterType: awsClusterTypeKubeadm, Region: "ap-southeast-2"},
	)
	if len(contracts) != 19 {
		t.Fatalf("got %d kubeadm generated-name contracts, want the 19 contracts validated by the scoped IAM module", len(contracts))
	}
	contractKeys := make(map[string]struct{}, len(contracts))
	for _, contract := range contracts {
		contractKeys[contract.Key] = struct{}{}
		if strings.HasPrefix(contract.Key, "karpenter_") && contract.Key != "karpenter_queue" {
			t.Errorf("kubeadm scope received EventBridge-only generated-name contract %q", contract.Key)
		}
	}
	for _, key := range []string{"eks_control_plane_role", "controller_eks_policy", "karpenter_queue"} {
		if _, exists := contractKeys[key]; !exists {
			t.Errorf("kubeadm scope omitted IAM-module generated-name contract %q", key)
		}
	}
}

func TestAWSGeneratedScopeNameValidationRejectsEveryOverLengthTemplate(t *testing.T) {
	scopeRef := "dsc-01k2m8g7n4p6q9r3t5v8x1y2z3"
	for _, template := range awsScopedGeneratedNameTemplates {
		t.Run(template.Key, func(t *testing.T) {
			contract := awsGeneratedNameContract{
				Key:       template.Key,
				Kind:      template.Kind,
				Namespace: template.Namespace,
				Name:      strings.Repeat("a", template.Limit+1),
				Limit:     template.Limit,
				Pattern:   template.Pattern,
				Regional:  template.Regional,
			}
			err := validateAWSGeneratedScopeName(scopeRef, contract)
			if err == nil {
				t.Fatal("expected over-length generated name to be rejected")
			}
			for _, want := range []string{
				scopeRef,
				contract.Kind,
				fmt.Sprintf("with %d characters", contract.Limit+1),
				fmt.Sprintf("cannot exceed %d characters", contract.Limit),
			} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func TestAWSGeneratedScopeNameValidationRejectsInvalidCharacters(t *testing.T) {
	contract := awsGeneratedNameContract{
		Kind:      "EventBridge rule",
		Namespace: "eventbridge-rule",
		Name:      "invalid name",
		Limit:     64,
		Pattern:   awsEventBridgeGeneratedNamePattern,
		Regional:  true,
	}
	err := validateAWSGeneratedScopeName("dsc-01k2m8g7n4p6q9r3t5v8x1y2z3", contract)
	if err == nil || !strings.Contains(err.Error(), awsEventBridgeGeneratedNamePattern.String()) {
		t.Fatalf("expected generated-name character validation error, got %v", err)
	}
}
