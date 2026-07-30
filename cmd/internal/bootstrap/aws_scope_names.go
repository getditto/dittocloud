package bootstrap

import (
	"fmt"
	"regexp"
)

var (
	awsIAMGeneratedNamePattern         = regexp.MustCompile(`^[A-Za-z0-9_+=,.@-]+$`)
	awsSQSGeneratedNamePattern         = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	awsEventBridgeGeneratedNamePattern = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

type awsGeneratedNameTemplate struct {
	Key       string
	Kind      string
	Namespace string
	Prefix    string
	Limit     int
	Pattern   *regexp.Regexp
	EKSOnly   bool
	Regional  bool
}

type awsGeneratedNameContract struct {
	Key       string
	Kind      string
	Namespace string
	Name      string
	Limit     int
	Pattern   *regexp.Regexp
	Regional  bool
}

var awsScopedGeneratedNameTemplates = []awsGeneratedNameTemplate{
	{Key: "controller_role", Kind: "IAM role", Namespace: "iam-role", Prefix: "ditto-capa-controller", Limit: 64, Pattern: awsIAMGeneratedNamePattern},
	{Key: "trust_editor_role", Kind: "IAM role", Namespace: "iam-role", Prefix: "ditto-iam-trust-editor", Limit: 64, Pattern: awsIAMGeneratedNamePattern},
	{Key: "nodes_role", Kind: "IAM role", Namespace: "iam-role", Prefix: "ditto-capa-nodes", Limit: 64, Pattern: awsIAMGeneratedNamePattern},
	{Key: "nodes_instance_profile", Kind: "IAM instance profile", Namespace: "iam-instance-profile", Prefix: "ditto-capa-nodes", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "control_plane_role", Kind: "IAM role", Namespace: "iam-role", Prefix: "ditto-capa-control-plane", Limit: 64, Pattern: awsIAMGeneratedNamePattern},
	{Key: "control_plane_instance_profile", Kind: "IAM instance profile", Namespace: "iam-instance-profile", Prefix: "ditto-capa-control-plane", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "eks_control_plane_role", Kind: "IAM role", Namespace: "iam-role", Prefix: "ditto-capa-eks-control-plane", Limit: 64, Pattern: awsIAMGeneratedNamePattern},
	{Key: "trust_editor_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-iam-trust-editor-policy", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "cluster_resources_boundary", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-cluster-resources-boundary", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "cluster_external_boundary", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-cluster-external-boundary", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "nodes_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-capa-nodes-policy", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "control_plane_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-capa-control-plane-policy", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "control_plane_tags_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-capa-control-plane-tags", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "controller_base_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-capa-controller-base", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "controller_network_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-capa-controller-network", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "controller_elb_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-capa-controller-elb", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "controller_vpc_lifecycle_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-capa-controller-vpc-lifecycle", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "controller_eks_policy", Kind: "IAM managed policy", Namespace: "iam-policy", Prefix: "ditto-capa-controller-eks", Limit: 128, Pattern: awsIAMGeneratedNamePattern},
	{Key: "karpenter_queue", Kind: "SQS queue", Namespace: "sqs-queue", Prefix: "karpenter-interruption", Limit: 80, Pattern: awsSQSGeneratedNamePattern, Regional: true},
	{Key: "karpenter_spot_interruption", Kind: "EventBridge rule", Namespace: "eventbridge-rule", Prefix: "KarpenterSpotInterruption", Limit: 64, Pattern: awsEventBridgeGeneratedNamePattern, EKSOnly: true, Regional: true},
	{Key: "karpenter_rebalance", Kind: "EventBridge rule", Namespace: "eventbridge-rule", Prefix: "KarpenterRebalance", Limit: 64, Pattern: awsEventBridgeGeneratedNamePattern, EKSOnly: true, Regional: true},
	{Key: "karpenter_instance_state", Kind: "EventBridge rule", Namespace: "eventbridge-rule", Prefix: "KarpenterInstanceState", Limit: 64, Pattern: awsEventBridgeGeneratedNamePattern, EKSOnly: true, Regional: true},
	{Key: "karpenter_health", Kind: "EventBridge rule", Namespace: "eventbridge-rule", Prefix: "KarpenterHealth", Limit: 64, Pattern: awsEventBridgeGeneratedNamePattern, EKSOnly: true, Regional: true},
}

func validateAWSGeneratedScopeNames(scopes AWSDeploymentScopes) error {
	generatedNames := map[string]string{}
	for _, scopeRef := range sortedAWSDeploymentScopeRefs(scopes) {
		scope := scopes[scopeRef]
		if scope.Default {
			continue
		}
		for _, contract := range awsGeneratedScopeNameContracts(scopeRef, scope) {
			if err := validateAWSGeneratedScopeName(scopeRef, contract); err != nil {
				return err
			}

			namespace := contract.Namespace
			if contract.Regional {
				namespace += "/" + scope.Region
			}
			identity := namespace + "\x00" + contract.Name
			if existingScopeRef, exists := generatedNames[identity]; exists {
				return fmt.Errorf(
					"scope %q generates duplicate %s name %q already generated for scope %q",
					scopeRef,
					contract.Kind,
					contract.Name,
					existingScopeRef,
				)
			}
			generatedNames[identity] = scopeRef
		}
	}
	return nil
}

func awsGeneratedScopeNameContracts(scopeRef string, scope AWSDeploymentScope) []awsGeneratedNameContract {
	contracts := make([]awsGeneratedNameContract, 0, len(awsScopedGeneratedNameTemplates))
	for _, template := range awsScopedGeneratedNameTemplates {
		if template.EKSOnly && scope.ClusterType != awsClusterTypeEKS {
			continue
		}
		contracts = append(contracts, awsGeneratedNameContract{
			Key:       template.Key,
			Kind:      template.Kind,
			Namespace: template.Namespace,
			Name:      template.Prefix + "-" + scopeRef,
			Limit:     template.Limit,
			Pattern:   template.Pattern,
			Regional:  template.Regional,
		})
	}
	return contracts
}

func validateAWSGeneratedScopeName(scopeRef string, contract awsGeneratedNameContract) error {
	if len(contract.Name) <= contract.Limit && contract.Pattern.MatchString(contract.Name) {
		return nil
	}
	return fmt.Errorf(
		"scope %q generates %s %q with %d characters; the name must match %s and cannot exceed %d characters",
		scopeRef,
		contract.Kind,
		contract.Name,
		len(contract.Name),
		contract.Pattern.String(),
		contract.Limit,
	)
}
