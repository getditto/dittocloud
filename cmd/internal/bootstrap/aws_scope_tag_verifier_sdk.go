package bootstrap

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/ec2"
	ec2types "github.com/aws/aws-sdk-go-v2/service/ec2/types"
	"github.com/aws/aws-sdk-go-v2/service/eks"
	"github.com/aws/aws-sdk-go-v2/service/eventbridge"
	"github.com/aws/aws-sdk-go-v2/service/iam"
	"github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi"
	rgtatypes "github.com/aws/aws-sdk-go-v2/service/resourcegroupstaggingapi/types"
	"github.com/aws/aws-sdk-go-v2/service/sqs"
	"github.com/aws/aws-sdk-go-v2/service/sts"
)

type awsSDKScopeTagVerifier struct {
	ec2     *ec2.Client
	eks     *eks.Client
	events  *eventbridge.Client
	iam     *iam.Client
	tagging *resourcegroupstaggingapi.Client
	sqs     *sqs.Client
	sts     *sts.Client
	region  string
}

func newAWSSDKScopeTagVerifier(ctx context.Context, profile, region string) (*awsSDKScopeTagVerifier, error) {
	options := []func(*config.LoadOptions) error{config.WithRegion(region)}
	if strings.TrimSpace(profile) != "" {
		options = append(options, config.WithSharedConfigProfile(profile))
	}
	configuration, err := config.LoadDefaultConfig(ctx, options...)
	if err != nil {
		return nil, err
	}
	return &awsSDKScopeTagVerifier{
		ec2:     ec2.NewFromConfig(configuration),
		eks:     eks.NewFromConfig(configuration),
		events:  eventbridge.NewFromConfig(configuration),
		iam:     iam.NewFromConfig(configuration),
		tagging: resourcegroupstaggingapi.NewFromConfig(configuration),
		sqs:     sqs.NewFromConfig(configuration),
		sts:     sts.NewFromConfig(configuration),
		region:  region,
	}, nil
}

func (verifier *awsSDKScopeTagVerifier) Verify(
	ctx context.Context,
	request awsScopeTagVerificationRequest,
) (awsScopeTagVerificationReport, error) {
	identity, err := verifier.sts.GetCallerIdentity(ctx, &sts.GetCallerIdentityInput{})
	if err != nil {
		return awsScopeTagVerificationReport{}, fmt.Errorf("unable to verify active AWS account identity: %w", err)
	}
	if actual := aws.ToString(identity.Account); actual != request.AccountID {
		return awsScopeTagVerificationReport{}, fmt.Errorf(
			"active AWS credentials resolve to account %q but scope state belongs to account %q",
			actual,
			request.AccountID,
		)
	}
	stateResources, err := verifier.verifyStateResources(ctx, request)
	if err != nil {
		return awsScopeTagVerificationReport{}, err
	}
	clusterResources, discoveryKeys, err := verifier.verifyClusterResources(ctx, request)
	if err != nil {
		return awsScopeTagVerificationReport{}, err
	}
	return awsScopeTagVerificationReport{
		ScopeRef:            request.ScopeRef,
		AccountID:           request.AccountID,
		ClusterName:         request.Scope.ClusterName,
		ClusterType:         request.Scope.ClusterType,
		Region:              request.Scope.Region,
		StateResources:      stateResources,
		ClusterResources:    clusterResources,
		ExplicitExclusions:  append([]string(nil), request.Exclusions...),
		NativeDiscoveryKeys: discoveryKeys,
	}, nil
}

func (verifier *awsSDKScopeTagVerifier) verifyStateResources(
	ctx context.Context,
	request awsScopeTagVerificationRequest,
) ([]awsScopeTagVerifiedResource, error) {
	ec2Resources := make([]awsScopeTagExpectedResource, 0)
	otherResources := make([]awsScopeTagExpectedResource, 0)
	for _, resource := range request.State {
		if resource.Region != "" && resource.Region != request.Scope.Region {
			return nil, fmt.Errorf(
				"dittocloud-managed resource %q is recorded in Region %q, outside scope Region %q",
				resource.Address,
				resource.Region,
				request.Scope.Region,
			)
		}
		if _, isEC2 := awsScopeTagEC2StateResourceTypes[resource.Type]; isEC2 {
			ec2Resources = append(ec2Resources, resource)
		} else {
			otherResources = append(otherResources, resource)
		}
	}

	verified := make([]awsScopeTagVerifiedResource, 0, len(request.State))
	if len(ec2Resources) > 0 {
		liveTags, err := verifier.ec2Tags(ctx, ec2Resources)
		if err != nil {
			return nil, err
		}
		for _, resource := range ec2Resources {
			tags := liveTags[resource.Identifier]
			if err := requireExactAWSScopeIdentityTag(resource.Address, tags, request.ScopeRef); err != nil {
				return nil, err
			}
			verified = append(verified, awsScopeTagVerifiedResource{Identity: resource.Address, Type: resource.Type, Tags: tags})
		}
	}
	for _, resource := range otherResources {
		tags, err := verifier.nonEC2StateResourceTags(ctx, resource)
		if err != nil {
			return nil, err
		}
		if err := requireExactAWSScopeIdentityTag(resource.Address, tags, request.ScopeRef); err != nil {
			return nil, err
		}
		verified = append(verified, awsScopeTagVerifiedResource{Identity: resource.Address, Type: resource.Type, Tags: tags})
	}
	sort.Slice(verified, func(i, j int) bool { return verified[i].Identity < verified[j].Identity })
	return verified, nil
}

func (verifier *awsSDKScopeTagVerifier) ec2Tags(
	ctx context.Context,
	resources []awsScopeTagExpectedResource,
) (map[string]map[string]string, error) {
	ids := make([]string, 0, len(resources))
	for _, resource := range resources {
		ids = append(ids, resource.Identifier)
	}
	tagsByID := make(map[string]map[string]string, len(ids))
	paginator := ec2.NewDescribeTagsPaginator(verifier.ec2, &ec2.DescribeTagsInput{
		Filters: []ec2types.Filter{{Name: aws.String("resource-id"), Values: ids}},
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to read live EC2 tags for scope inventory: %w", err)
		}
		for _, tag := range page.Tags {
			resourceID := aws.ToString(tag.ResourceId)
			if tagsByID[resourceID] == nil {
				tagsByID[resourceID] = map[string]string{}
			}
			tagsByID[resourceID][aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
	}
	return tagsByID, nil
}

func (verifier *awsSDKScopeTagVerifier) nonEC2StateResourceTags(
	ctx context.Context,
	resource awsScopeTagExpectedResource,
) (map[string]string, error) {
	switch resource.Type {
	case "aws_iam_role":
		return verifier.iamRoleTags(ctx, resource)
	case "aws_iam_policy":
		return verifier.iamPolicyTags(ctx, resource)
	case "aws_iam_instance_profile":
		return verifier.iamInstanceProfileTags(ctx, resource)
	case "aws_sqs_queue":
		output, err := verifier.sqs.ListQueueTags(ctx, &sqs.ListQueueTagsInput{QueueUrl: aws.String(resource.QueueURL)})
		if err != nil {
			return nil, fmt.Errorf("unable to read live SQS tags for %q: %w", resource.Address, err)
		}
		return output.Tags, nil
	case "aws_cloudwatch_event_rule":
		output, err := verifier.events.ListTagsForResource(ctx, &eventbridge.ListTagsForResourceInput{ResourceARN: aws.String(resource.ARN)})
		if err != nil {
			return nil, fmt.Errorf("unable to read live EventBridge tags for %q: %w", resource.Address, err)
		}
		tags := make(map[string]string, len(output.Tags))
		for _, tag := range output.Tags {
			tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		return tags, nil
	default:
		return nil, fmt.Errorf("unsupported live AWS tag reader for %q at %q", resource.Type, resource.Address)
	}
}

func (verifier *awsSDKScopeTagVerifier) iamRoleTags(ctx context.Context, resource awsScopeTagExpectedResource) (map[string]string, error) {
	tags := map[string]string{}
	var marker *string
	for {
		output, err := verifier.iam.ListRoleTags(ctx, &iam.ListRoleTagsInput{RoleName: aws.String(resource.Identifier), Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("unable to read live IAM role tags for %q: %w", resource.Address, err)
		}
		for _, tag := range output.Tags {
			tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		if !output.IsTruncated {
			return tags, nil
		}
		if output.Marker == nil || aws.ToString(output.Marker) == "" {
			return nil, fmt.Errorf("IAM role tag pagination for %q was truncated without a marker", resource.Address)
		}
		marker = output.Marker
	}
}

func (verifier *awsSDKScopeTagVerifier) iamPolicyTags(ctx context.Context, resource awsScopeTagExpectedResource) (map[string]string, error) {
	tags := map[string]string{}
	var marker *string
	for {
		output, err := verifier.iam.ListPolicyTags(ctx, &iam.ListPolicyTagsInput{PolicyArn: aws.String(resource.ARN), Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("unable to read live IAM policy tags for %q: %w", resource.Address, err)
		}
		for _, tag := range output.Tags {
			tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		if !output.IsTruncated {
			return tags, nil
		}
		if output.Marker == nil || aws.ToString(output.Marker) == "" {
			return nil, fmt.Errorf("IAM policy tag pagination for %q was truncated without a marker", resource.Address)
		}
		marker = output.Marker
	}
}

func (verifier *awsSDKScopeTagVerifier) iamInstanceProfileTags(ctx context.Context, resource awsScopeTagExpectedResource) (map[string]string, error) {
	tags := map[string]string{}
	var marker *string
	for {
		output, err := verifier.iam.ListInstanceProfileTags(ctx, &iam.ListInstanceProfileTagsInput{InstanceProfileName: aws.String(resource.Identifier), Marker: marker})
		if err != nil {
			return nil, fmt.Errorf("unable to read live IAM instance-profile tags for %q: %w", resource.Address, err)
		}
		for _, tag := range output.Tags {
			tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
		}
		if !output.IsTruncated {
			return tags, nil
		}
		if output.Marker == nil || aws.ToString(output.Marker) == "" {
			return nil, fmt.Errorf("IAM instance-profile tag pagination for %q was truncated without a marker", resource.Address)
		}
		marker = output.Marker
	}
}

func requireExactAWSScopeIdentityTag(identity string, tags map[string]string, scopeRef string) error {
	actual, exists := tags[awsScopeIdentityTagKey]
	if !exists {
		return fmt.Errorf("AWS resource %q is missing required tag %s=%s", identity, awsScopeIdentityTagKey, scopeRef)
	}
	if actual != scopeRef {
		return fmt.Errorf("AWS resource %q has conflicting tag %s=%s; expected %s", identity, awsScopeIdentityTagKey, actual, scopeRef)
	}
	return nil
}

func (verifier *awsSDKScopeTagVerifier) verifyClusterResources(
	ctx context.Context,
	request awsScopeTagVerificationRequest,
) ([]awsScopeTagVerifiedResource, []string, error) {
	clusterName := request.Scope.ClusterName
	discovery := []struct {
		key   string
		value string
	}{
		{key: "kubernetes.io/cluster/" + clusterName, value: "owned"},
		{key: "sigs.k8s.io/cluster-api-provider-aws/cluster/" + clusterName, value: "owned"},
		{key: "elbv2.k8s.aws/cluster", value: clusterName},
	}

	resources := map[string]map[string]string{}
	foundKeys := map[string]bool{}
	clusterOwnershipResources := 0
	for index, query := range discovery {
		mappings, err := verifier.resourcesByTag(ctx, query.key, query.value)
		if err != nil {
			return nil, nil, err
		}
		if len(mappings) > 0 {
			foundKeys[query.key] = true
		}
		if index < 2 {
			clusterOwnershipResources += len(mappings)
		}
		for arn, tags := range mappings {
			resources[arn] = tags
		}
	}

	if request.Scope.ClusterType == awsClusterTypeEKS {
		output, err := verifier.eks.DescribeCluster(ctx, &eks.DescribeClusterInput{Name: aws.String(clusterName)})
		if err != nil {
			return nil, nil, fmt.Errorf("unable to describe named EKS cluster %q in Region %q: %w", clusterName, request.Scope.Region, err)
		}
		if output.Cluster == nil || aws.ToString(output.Cluster.Arn) == "" || aws.ToString(output.Cluster.Name) != clusterName {
			return nil, nil, fmt.Errorf("EKS returned incomplete identity for named cluster %q", clusterName)
		}
		resources[aws.ToString(output.Cluster.Arn)] = output.Cluster.Tags
		foundKeys["eks:cluster-name"] = true
		clusterOwnershipResources++
	}
	if clusterOwnershipResources == 0 {
		return nil, nil, fmt.Errorf(
			"no live CAPA or Kubernetes resources were found for cluster %q in Region %q; version 1 inventory is incomplete",
			clusterName,
			request.Scope.Region,
		)
	}

	verified := make([]awsScopeTagVerifiedResource, 0, len(resources))
	for arn, tags := range resources {
		if err := validateAWSClusterNativeIdentityTags(arn, tags, request.ScopeRef, clusterName); err != nil {
			return nil, nil, err
		}
		verified = append(verified, awsScopeTagVerifiedResource{Identity: arn, Type: awsARNService(arn), Tags: tags})
	}
	sort.Slice(verified, func(i, j int) bool { return verified[i].Identity < verified[j].Identity })
	discoveryKeys := make([]string, 0, len(foundKeys))
	for key := range foundKeys {
		discoveryKeys = append(discoveryKeys, key)
	}
	sort.Strings(discoveryKeys)
	return verified, discoveryKeys, nil
}

func (verifier *awsSDKScopeTagVerifier) resourcesByTag(ctx context.Context, key, value string) (map[string]map[string]string, error) {
	resources := map[string]map[string]string{}
	paginator := resourcegroupstaggingapi.NewGetResourcesPaginator(verifier.tagging, &resourcegroupstaggingapi.GetResourcesInput{
		TagFilters: []rgtatypes.TagFilter{{Key: aws.String(key), Values: []string{value}}},
	})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("unable to discover live AWS resources with %s=%s: %w", key, value, err)
		}
		for _, mapping := range page.ResourceTagMappingList {
			arn := aws.ToString(mapping.ResourceARN)
			if arn == "" {
				return nil, fmt.Errorf("AWS tag discovery for %s=%s returned a resource without an ARN", key, value)
			}
			tags := make(map[string]string, len(mapping.Tags))
			for _, tag := range mapping.Tags {
				tags[aws.ToString(tag.Key)] = aws.ToString(tag.Value)
			}
			resources[arn] = tags
		}
	}
	return resources, nil
}

func validateAWSClusterNativeIdentityTags(arn string, tags map[string]string, scopeRef, clusterName string) error {
	if actual, exists := tags[awsScopeIdentityTagKey]; exists && actual != scopeRef {
		return fmt.Errorf("cluster resource %q has conflicting tag %s=%s; expected %s or no scope tag", arn, awsScopeIdentityTagKey, actual, scopeRef)
	}
	for key, value := range tags {
		if value != "owned" {
			continue
		}
		if strings.HasPrefix(key, "kubernetes.io/cluster/") && key != "kubernetes.io/cluster/"+clusterName {
			return fmt.Errorf("cluster resource %q has conflicting owned-cluster tag %s=%s", arn, key, value)
		}
		if strings.HasPrefix(key, "sigs.k8s.io/cluster-api-provider-aws/cluster/") && key != "sigs.k8s.io/cluster-api-provider-aws/cluster/"+clusterName {
			return fmt.Errorf("cluster resource %q has conflicting CAPA cluster tag %s=%s", arn, key, value)
		}
	}
	if value, exists := tags["elbv2.k8s.aws/cluster"]; exists && value != clusterName {
		return fmt.Errorf("cluster resource %q has conflicting elbv2.k8s.aws/cluster=%s; expected %s", arn, value, clusterName)
	}
	return nil
}

func awsARNService(arn string) string {
	parts := strings.SplitN(arn, ":", 6)
	if len(parts) < 6 || parts[0] != "arn" {
		return "unknown"
	}
	return parts[2]
}
