package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"
)

var awsAccountIDPattern = regexp.MustCompile(`^[0-9]{12}$`)

var awsScopeTagEC2StateResourceTypes = map[string]struct{}{
	"aws_default_network_acl":      {},
	"aws_default_route_table":      {},
	"aws_default_security_group":   {},
	"aws_eip":                      {},
	"aws_instance":                 {},
	"aws_internet_gateway":         {},
	"aws_launch_template":          {},
	"aws_nat_gateway":              {},
	"aws_network_acl":              {},
	"aws_network_interface":        {},
	"aws_route_table":              {},
	"aws_security_group":           {},
	"aws_snapshot":                 {},
	"aws_subnet":                   {},
	"aws_volume_attachment":        {},
	"aws_vpc":                      {},
	"aws_vpc_dhcp_options":         {},
	"aws_vpc_endpoint":             {},
	"aws_vpc_peering_connection":   {},
	"aws_vpn_connection":           {},
	"aws_vpn_gateway":              {},
	"aws_ebs_volume":               {},
	"aws_ec2_capacity_reservation": {},
}

func loadAWSStateScopeTagInventory(
	statePath string,
	scopeRef string,
	scope AWSDeploymentScope,
) ([]awsScopeTagExpectedResource, []string, error) {
	state, exists, err := loadRawTerraformState(statePath)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, fmt.Errorf("terraform state %q does not exist", statePath)
	}

	resources := make([]awsScopeTagExpectedResource, 0)
	seen := map[string]string{}
	for _, resource := range state.Resources {
		if resource.Mode != "managed" {
			continue
		}
		for _, instance := range resource.Instances {
			if instance.Deposed != "" || instance.Status != "" {
				continue
			}
			attributes, tags, err := decodeAWSStateTaggableAttributes(instance.Attributes)
			if err != nil {
				return nil, nil, fmt.Errorf("terraform state resource %q has malformed tag attributes: %w", awsRawTerraformResourceAddress(resource, instance), err)
			}
			if tags[awsScopeIdentityTagKey] != scopeRef {
				continue
			}
			expected, err := awsScopeTagExpectedResourceFromState(resource, instance, attributes, scope.Region)
			if err != nil {
				return nil, nil, err
			}
			identity := expected.Type + "\x00" + expected.Identifier
			if prior, duplicate := seen[identity]; duplicate {
				return nil, nil, fmt.Errorf(
					"terraform state %q contains duplicate scope-tag inventory identity %s at %q and %q",
					statePath,
					expected.Identifier,
					prior,
					expected.Address,
				)
			}
			seen[identity] = expected.Address
			resources = append(resources, expected)
		}
	}
	if len(resources) == 0 {
		return nil, nil, fmt.Errorf("terraform state %q contains no Dittocloud-managed resources tagged for scope %q", statePath, scopeRef)
	}
	sort.Slice(resources, func(i, j int) bool { return resources[i].Address < resources[j].Address })

	exclusions := []string{
		"account-wide and Region-owned singleton resources without one-scope ownership",
	}
	switch scope.VPC.Mode {
	case awsVPCModeExisting:
		exclusions = append(exclusions, "customer-owned existing VPC and its discovered subnets")
	case awsVPCModeCAPI:
		exclusions = append(exclusions, "Cluster API-created VPC resources are verified through native cluster ownership tags")
	}
	return resources, exclusions, nil
}

func decodeAWSStateTaggableAttributes(attributesJSON json.RawMessage) (map[string]json.RawMessage, map[string]string, error) {
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
		return nil, nil, err
	}
	for _, field := range []string{"tags_all", "tags"} {
		raw := attributes[field]
		if len(bytes.TrimSpace(raw)) == 0 || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			continue
		}
		var tags map[string]string
		if err := json.Unmarshal(raw, &tags); err != nil {
			return nil, nil, fmt.Errorf("%s is not a string map", field)
		}
		return attributes, tags, nil
	}
	return attributes, map[string]string{}, nil
}

func awsScopeTagExpectedResourceFromState(
	resource rawTerraformResource,
	instance rawTerraformInstance,
	attributes map[string]json.RawMessage,
	defaultRegion string,
) (awsScopeTagExpectedResource, error) {
	address := awsRawTerraformResourceAddress(resource, instance)
	identifier := awsStateStringAttribute(attributes, "id")
	arn := awsStateStringAttribute(attributes, "arn")
	region := awsStateStringAttribute(attributes, "region")
	if region == "" {
		region = defaultRegion
	}
	expected := awsScopeTagExpectedResource{
		Address:    address,
		Type:       resource.Type,
		Identifier: identifier,
		ARN:        arn,
		Region:     region,
		QueueURL:   awsStateStringAttribute(attributes, "url"),
	}

	if _, ec2Resource := awsScopeTagEC2StateResourceTypes[resource.Type]; ec2Resource {
		if identifier == "" {
			return expected, fmt.Errorf("terraform state scope-tag resource %q has no EC2 resource ID", address)
		}
		return expected, nil
	}
	switch resource.Type {
	case "aws_iam_role", "aws_iam_instance_profile":
		if identifier == "" {
			return expected, fmt.Errorf("terraform state scope-tag resource %q has no IAM name", address)
		}
	case "aws_iam_policy":
		if arn == "" {
			return expected, fmt.Errorf("terraform state scope-tag resource %q has no IAM policy ARN", address)
		}
		expected.Identifier = arn
	case "aws_sqs_queue":
		if expected.QueueURL == "" || arn == "" {
			return expected, fmt.Errorf("terraform state scope-tag resource %q has no SQS queue URL or ARN", address)
		}
		expected.Identifier = arn
	case "aws_cloudwatch_event_rule":
		if arn == "" {
			return expected, fmt.Errorf("terraform state scope-tag resource %q has no EventBridge rule ARN", address)
		}
		expected.Identifier = arn
	default:
		return expected, fmt.Errorf(
			"terraform state scope-tag resource %q uses unsupported verification type %q; update Dittocloud's built-in inventory catalog before enabling version 1",
			address,
			resource.Type,
		)
	}
	return expected, nil
}

func awsStateStringAttribute(attributes map[string]json.RawMessage, name string) string {
	raw := bytes.TrimSpace(attributes[name])
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return ""
	}
	var value string
	if json.Unmarshal(raw, &value) != nil {
		return ""
	}
	return value
}

func awsRawTerraformResourceAddress(resource rawTerraformResource, instance rawTerraformInstance) string {
	parts := make([]string, 0, 2)
	if resource.Module != "" {
		parts = append(parts, resource.Module)
	}
	address := resource.Type + "." + resource.Name
	if index := strings.TrimSpace(string(instance.IndexKey)); index != "" && index != "null" {
		address += "[" + index + "]"
	}
	parts = append(parts, address)
	return strings.Join(parts, ".")
}

func awsScopeTagInventoryAccountID(resources []awsScopeTagExpectedResource) (string, error) {
	accountIDs := map[string]bool{}
	for _, resource := range resources {
		if resource.ARN == "" {
			continue
		}
		parts := strings.SplitN(resource.ARN, ":", 6)
		if len(parts) != 6 || parts[0] != "arn" {
			return "", fmt.Errorf("scope-tag inventory resource %q has malformed ARN %q", resource.Address, resource.ARN)
		}
		if parts[4] == "" {
			continue
		}
		if !awsAccountIDPattern.MatchString(parts[4]) {
			return "", fmt.Errorf("scope-tag inventory resource %q has invalid AWS account ID in ARN %q", resource.Address, resource.ARN)
		}
		accountIDs[parts[4]] = true
	}
	if len(accountIDs) != 1 {
		ids := make([]string, 0, len(accountIDs))
		for id := range accountIDs {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		return "", fmt.Errorf("scope-tag inventory must identify exactly one AWS account; found [%s]", strings.Join(ids, ", "))
	}
	for id := range accountIDs {
		return id, nil
	}
	return "", fmt.Errorf("scope-tag inventory has no AWS account identity")
}
