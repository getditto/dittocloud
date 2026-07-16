package bootstrap

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

const (
	legacyScopeFieldRegion      = "region"
	legacyScopeFieldVPCMode     = "vpc.mode"
	legacyScopeFieldVPCID       = "vpc.id"
	legacyScopeFieldVPCName     = "vpc.name"
	legacyScopeFieldVPCCIDR     = "vpc.cidr"
	legacyScopeFieldClusterType = "clusterType"
	legacyScopeFieldClusterName = "clusterName"
)

type awsLegacyScopeFieldEvidence struct {
	Value   string
	Sources []string
}

type awsLegacyScopeDiscovery struct {
	Scope    AWSDeploymentScope
	Evidence map[string]awsLegacyScopeFieldEvidence
	Missing  []string
}

type awsLegacyEvidenceCollector map[string]map[string]map[string]struct{}

type awsLegacyVPCValidationEvidence struct {
	CustomerManaged bool
	VPCID           string
	Source          string
}

var legacyPhaseTwoIAMPolicyNames = map[string]struct{}{
	"capa_controller_base":    {},
	"capa_controller_network": {},
	"capa_controller_elb":     {},
	"capa_control_plane":      {},
	"capa_control_plane_tags": {},
}

func discoverAWSLegacyScope(state rawTerraformState) (awsLegacyScopeDiscovery, error) {
	collector := awsLegacyEvidenceCollector{}
	if err := collectAWSLegacyOutputEvidence(state.Outputs["aws"], collector); err != nil {
		return awsLegacyScopeDiscovery{}, err
	}

	managedVPCPresent := false
	var validationEvidence *awsLegacyVPCValidationEvidence
	validationResourceCount := 0

	for _, resource := range state.Resources {
		if isAWSLegacyManagedVPCResource(resource) {
			present, err := collectAWSLegacyManagedVPCEvidence(resource, collector)
			if err != nil {
				return awsLegacyScopeDiscovery{}, err
			}
			managedVPCPresent = managedVPCPresent || present
		}

		if isAWSLegacyVPCValidationResource(resource) {
			validationResourceCount++
			if validationResourceCount > 1 {
				return awsLegacyScopeDiscovery{}, fmt.Errorf("legacy state contains duplicate root terraform_data.validate_vpc_mode resources")
			}
			decoded, err := decodeAWSLegacyVPCValidationEvidence(resource)
			if err != nil {
				return awsLegacyScopeDiscovery{}, err
			}
			validationEvidence = &decoded
		}

		if isAWSLegacyEKSMarkerResource(resource) {
			present, err := awsLegacyResourceHasReadyInstance(resource)
			if err != nil {
				return awsLegacyScopeDiscovery{}, err
			}
			if present {
				collector.add(legacyScopeFieldClusterType, awsClusterTypeEKS, awsLegacyResourceAddress(resource))
			}
		}

		if isAWSLegacyPhaseTwoIAMPolicy(resource) {
			if err := collectAWSLegacyClusterNameEvidence(resource, collector); err != nil {
				return awsLegacyScopeDiscovery{}, err
			}
		}
	}

	if validationEvidence != nil {
		if validationEvidence.VPCID != "" {
			collector.add(legacyScopeFieldVPCID, validationEvidence.VPCID, validationEvidence.Source+" vpc_id")
		}
		switch {
		case validationEvidence.CustomerManaged:
			collector.add(legacyScopeFieldVPCMode, awsVPCModeExisting, validationEvidence.Source+" customer_managed_vpc")
		case !managedVPCPresent:
			collector.add(legacyScopeFieldVPCMode, awsVPCModeCAPI, validationEvidence.Source+" customer_managed_vpc")
		}
	}

	evidence, err := collector.resolve()
	if err != nil {
		return awsLegacyScopeDiscovery{}, err
	}
	if vpcID := evidence[legacyScopeFieldVPCID].Value; vpcID != "" && !awsVPCIDPattern.MatchString(vpcID) {
		return awsLegacyScopeDiscovery{}, fmt.Errorf("legacy state vpc.id evidence %q is not a valid VPC ID", vpcID)
	}
	discovery := awsLegacyScopeDiscovery{
		Scope: AWSDeploymentScope{
			Default:               true,
			ScopeTagPolicyVersion: 0,
		},
		Evidence: evidence,
	}
	discovery.Scope.Region = evidence[legacyScopeFieldRegion].Value
	discovery.Scope.ClusterType = evidence[legacyScopeFieldClusterType].Value
	discovery.Scope.ClusterName = evidence[legacyScopeFieldClusterName].Value
	discovery.Scope.VPC = AWSScopeVPC{
		Mode: evidence[legacyScopeFieldVPCMode].Value,
		Name: evidence[legacyScopeFieldVPCName].Value,
		CIDR: evidence[legacyScopeFieldVPCCIDR].Value,
	}
	if discovery.Scope.VPC.Mode != awsVPCModeDittocloud {
		discovery.Scope.VPC.ID = evidence[legacyScopeFieldVPCID].Value
	}
	discovery.Missing = missingAWSLegacyScopeFields(discovery.Scope)
	return discovery, nil
}

func (collector awsLegacyEvidenceCollector) add(field, value, source string) {
	value = strings.TrimSpace(value)
	if value == "" {
		return
	}
	if collector[field] == nil {
		collector[field] = map[string]map[string]struct{}{}
	}
	if collector[field][value] == nil {
		collector[field][value] = map[string]struct{}{}
	}
	collector[field][value][source] = struct{}{}
}

func (collector awsLegacyEvidenceCollector) resolve() (map[string]awsLegacyScopeFieldEvidence, error) {
	resolved := make(map[string]awsLegacyScopeFieldEvidence, len(collector))
	fields := make([]string, 0, len(collector))
	for field := range collector {
		fields = append(fields, field)
	}
	sort.Strings(fields)
	for _, field := range fields {
		values := make([]string, 0, len(collector[field]))
		for value := range collector[field] {
			values = append(values, value)
		}
		sort.Strings(values)
		if len(values) > 1 {
			details := make([]string, 0, len(values))
			for _, value := range values {
				sources := sortedAWSLegacyEvidenceSources(collector[field][value])
				details = append(details, fmt.Sprintf("%q from %s", value, strings.Join(sources, ", ")))
			}
			return nil, fmt.Errorf("conflicting legacy state evidence for %s: %s", field, strings.Join(details, "; "))
		}
		resolved[field] = awsLegacyScopeFieldEvidence{
			Value:   values[0],
			Sources: sortedAWSLegacyEvidenceSources(collector[field][values[0]]),
		}
	}
	return resolved, nil
}

func sortedAWSLegacyEvidenceSources(sources map[string]struct{}) []string {
	result := make([]string, 0, len(sources))
	for source := range sources {
		result = append(result, source)
	}
	sort.Strings(result)
	return result
}

func collectAWSLegacyOutputEvidence(rawOutput json.RawMessage, collector awsLegacyEvidenceCollector) error {
	if len(bytes.TrimSpace(rawOutput)) == 0 {
		return nil
	}
	var output map[string]json.RawMessage
	if err := json.Unmarshal(rawOutput, &output); err != nil {
		return fmt.Errorf("legacy state output aws is malformed: %w", err)
	}
	valueJSON, exists := output["value"]
	if !exists || !jsonObject(valueJSON) {
		return fmt.Errorf("legacy state output aws.value must be an object")
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(valueJSON, &value); err != nil {
		return fmt.Errorf("legacy state output aws.value is malformed: %w", err)
	}
	region, present, err := decodeAWSLegacyOptionalString(value["region"], "output aws.region")
	if err != nil {
		return err
	}
	if present {
		collector.add(legacyScopeFieldRegion, region, "output aws.region")
	}

	vpcJSON, exists := value["vpc"]
	if !exists || bytes.Equal(bytes.TrimSpace(vpcJSON), []byte("null")) {
		return nil
	}
	var vpcEntries []json.RawMessage
	if err := json.Unmarshal(vpcJSON, &vpcEntries); err != nil {
		return fmt.Errorf("legacy state output aws.vpc must be a list: %w", err)
	}
	for index, entryJSON := range vpcEntries {
		var entry map[string]json.RawMessage
		if err := json.Unmarshal(entryJSON, &entry); err != nil {
			return fmt.Errorf("legacy state output aws.vpc[%d] is malformed: %w", index, err)
		}
		directID, directPresent, err := decodeAWSLegacyOptionalString(entry["vpc_id"], fmt.Sprintf("output aws.vpc[%d].vpc_id", index))
		if err != nil {
			return err
		}
		if directPresent {
			collector.add(legacyScopeFieldVPCID, directID, fmt.Sprintf("output aws.vpc[%d].vpc_id", index))
		}

		nestedJSON, exists := entry["vpc"]
		if !exists || bytes.Equal(bytes.TrimSpace(nestedJSON), []byte("null")) {
			continue
		}
		var nested map[string]json.RawMessage
		if err := json.Unmarshal(nestedJSON, &nested); err != nil {
			return fmt.Errorf("legacy state output aws.vpc[%d].vpc must be an object: %w", index, err)
		}
		nestedID, nestedPresent, err := decodeAWSLegacyOptionalString(nested["vpc_id"], fmt.Sprintf("output aws.vpc[%d].vpc.vpc_id", index))
		if err != nil {
			return err
		}
		if nestedPresent {
			collector.add(legacyScopeFieldVPCID, nestedID, fmt.Sprintf("output aws.vpc[%d].vpc.vpc_id", index))
		}
	}
	return nil
}

func decodeAWSLegacyOptionalString(rawValue json.RawMessage, source string) (string, bool, error) {
	trimmed := bytes.TrimSpace(rawValue)
	if len(trimmed) == 0 || bytes.Equal(trimmed, []byte("null")) {
		return "", false, nil
	}
	var value string
	if err := json.Unmarshal(trimmed, &value); err != nil {
		return "", false, fmt.Errorf("legacy state %s must be a string", source)
	}
	value = strings.TrimSpace(value)
	return value, value != "", nil
}

func isAWSLegacyManagedVPCResource(resource rawTerraformResource) bool {
	if resource.Mode != "managed" || resource.Type != "aws_vpc" || resource.Name != "this" {
		return false
	}
	return resource.Module == "module.vpc[0].module.vpc" || resource.Module == "module.vpc.module.vpc"
}

func collectAWSLegacyManagedVPCEvidence(resource rawTerraformResource, collector awsLegacyEvidenceCollector) (bool, error) {
	readyInstances, err := awsLegacyReadyInstances(resource)
	if err != nil {
		return false, err
	}
	for _, instance := range readyInstances {
		var attributes map[string]json.RawMessage
		if err := json.Unmarshal(instance.Attributes, &attributes); err != nil {
			return false, fmt.Errorf("legacy state %s attributes are malformed: %w", awsLegacyResourceAddress(resource), err)
		}
		cidr, present, err := decodeAWSLegacyOptionalString(attributes["cidr_block"], awsLegacyResourceAddress(resource)+" cidr_block")
		if err != nil {
			return false, err
		}
		if present {
			collector.add(legacyScopeFieldVPCCIDR, cidr, awsLegacyResourceAddress(resource)+" cidr_block")
		}

		tagsJSON, exists := attributes["tags"]
		if !exists || bytes.Equal(bytes.TrimSpace(tagsJSON), []byte("null")) {
			continue
		}
		var tags map[string]json.RawMessage
		if err := json.Unmarshal(tagsJSON, &tags); err != nil {
			return false, fmt.Errorf("legacy state %s tags are malformed: %w", awsLegacyResourceAddress(resource), err)
		}
		name, present, err := decodeAWSLegacyOptionalString(tags["Name"], awsLegacyResourceAddress(resource)+" tags.Name")
		if err != nil {
			return false, err
		}
		if present {
			collector.add(legacyScopeFieldVPCName, name, awsLegacyResourceAddress(resource)+" tags.Name")
		}
	}
	if len(readyInstances) > 0 {
		collector.add(legacyScopeFieldVPCMode, awsVPCModeDittocloud, awsLegacyResourceAddress(resource))
	}
	return len(readyInstances) > 0, nil
}

func isAWSLegacyVPCValidationResource(resource rawTerraformResource) bool {
	return resource.Module == "" && resource.Mode == "managed" && resource.Type == "terraform_data" && resource.Name == "validate_vpc_mode"
}

func decodeAWSLegacyVPCValidationEvidence(resource rawTerraformResource) (awsLegacyVPCValidationEvidence, error) {
	readyInstances, err := awsLegacyReadyInstances(resource)
	if err != nil {
		return awsLegacyVPCValidationEvidence{}, err
	}
	if len(readyInstances) != 1 {
		return awsLegacyVPCValidationEvidence{}, fmt.Errorf("legacy state %s must contain exactly one ready instance", awsLegacyResourceAddress(resource))
	}
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal(readyInstances[0].Attributes, &attributes); err != nil {
		return awsLegacyVPCValidationEvidence{}, fmt.Errorf("legacy state %s attributes are malformed: %w", awsLegacyResourceAddress(resource), err)
	}
	var dynamicValue map[string]json.RawMessage
	if err := json.Unmarshal(attributes["input"], &dynamicValue); err != nil {
		return awsLegacyVPCValidationEvidence{}, fmt.Errorf("legacy state %s input is malformed", awsLegacyResourceAddress(resource))
	}
	if len(dynamicValue) != 2 || dynamicValue["value"] == nil || dynamicValue["type"] == nil {
		return awsLegacyVPCValidationEvidence{}, fmt.Errorf("legacy state %s input must use Terraform's exact dynamic value and type encoding", awsLegacyResourceAddress(resource))
	}
	if err := validateAWSLegacyVPCValidationType(dynamicValue["type"]); err != nil {
		return awsLegacyVPCValidationEvidence{}, fmt.Errorf("legacy state %s input type is invalid: %w", awsLegacyResourceAddress(resource), err)
	}
	var value map[string]json.RawMessage
	if err := json.Unmarshal(dynamicValue["value"], &value); err != nil {
		return awsLegacyVPCValidationEvidence{}, fmt.Errorf("legacy state %s input value is malformed", awsLegacyResourceAddress(resource))
	}
	if len(value) != 2 || value["customer_managed_vpc"] == nil || value["vpc_id"] == nil {
		return awsLegacyVPCValidationEvidence{}, fmt.Errorf("legacy state %s input value must contain exactly customer_managed_vpc and vpc_id", awsLegacyResourceAddress(resource))
	}
	var customerManaged bool
	if err := json.Unmarshal(value["customer_managed_vpc"], &customerManaged); err != nil {
		return awsLegacyVPCValidationEvidence{}, fmt.Errorf("legacy state %s customer_managed_vpc must be a boolean", awsLegacyResourceAddress(resource))
	}
	vpcID, _, err := decodeAWSLegacyOptionalString(value["vpc_id"], awsLegacyResourceAddress(resource)+" input vpc_id")
	if err != nil {
		return awsLegacyVPCValidationEvidence{}, err
	}
	return awsLegacyVPCValidationEvidence{
		CustomerManaged: customerManaged,
		VPCID:           vpcID,
		Source:          awsLegacyResourceAddress(resource) + " input",
	}, nil
}

func validateAWSLegacyVPCValidationType(rawType json.RawMessage) error {
	var descriptor []json.RawMessage
	if err := json.Unmarshal(rawType, &descriptor); err != nil || len(descriptor) != 2 {
		return fmt.Errorf("type descriptor is malformed")
	}
	var kind string
	if err := json.Unmarshal(descriptor[0], &kind); err != nil || kind != "object" {
		return fmt.Errorf("type descriptor must describe an object")
	}
	var fields map[string]string
	if err := json.Unmarshal(descriptor[1], &fields); err != nil {
		return fmt.Errorf("object type descriptor is malformed")
	}
	if len(fields) != 2 || fields["customer_managed_vpc"] != "bool" || fields["vpc_id"] != "string" {
		return fmt.Errorf("object type descriptor must contain exactly customer_managed_vpc bool and vpc_id string")
	}
	return nil
}

func isAWSLegacyEKSMarkerResource(resource rawTerraformResource) bool {
	if resource.Mode != "managed" {
		return false
	}
	if resource.Module == "" && resource.Name == "karpenter_interruption" {
		switch resource.Type {
		case "aws_sqs_queue", "aws_sqs_queue_policy", "aws_cloudwatch_event_rule", "aws_cloudwatch_event_target":
			return true
		}
	}
	if resource.Module != "module.cross_account_iam[0]" {
		return false
	}
	return resource.Name == "capa_eks_control_plane" || resource.Name == "capa_controller_eks_policy"
}

func isAWSLegacyPhaseTwoIAMPolicy(resource rawTerraformResource) bool {
	if resource.Mode != "managed" || resource.Type != "aws_iam_policy" || resource.Module != "module.cross_account_iam[0]" {
		return false
	}
	_, accepted := legacyPhaseTwoIAMPolicyNames[resource.Name]
	return accepted
}

func collectAWSLegacyClusterNameEvidence(resource rawTerraformResource, collector awsLegacyEvidenceCollector) error {
	readyInstances, err := awsLegacyReadyInstances(resource)
	if err != nil {
		return err
	}
	for _, instance := range readyInstances {
		var attributes map[string]json.RawMessage
		if err := json.Unmarshal(instance.Attributes, &attributes); err != nil {
			return fmt.Errorf("legacy state %s attributes are malformed: %w", awsLegacyResourceAddress(resource), err)
		}
		policyJSON, present, err := decodeAWSLegacyOptionalString(attributes["policy"], awsLegacyResourceAddress(resource)+" policy")
		if err != nil {
			return err
		}
		if !present {
			return fmt.Errorf("legacy state %s policy is missing", awsLegacyResourceAddress(resource))
		}
		if err := collectAWSLegacyClusterNamesFromPolicy(policyJSON, awsLegacyResourceAddress(resource), collector); err != nil {
			return err
		}
	}
	return nil
}

func collectAWSLegacyClusterNamesFromPolicy(policyJSON, source string, collector awsLegacyEvidenceCollector) error {
	var document map[string]json.RawMessage
	if err := json.Unmarshal([]byte(policyJSON), &document); err != nil {
		return fmt.Errorf("legacy state %s policy document is malformed", source)
	}
	statementJSON, exists := document["Statement"]
	if !exists {
		return fmt.Errorf("legacy state %s policy document has no Statement", source)
	}
	statements, err := decodeAWSLegacyPolicyStatements(statementJSON)
	if err != nil {
		return fmt.Errorf("legacy state %s policy Statement is malformed", source)
	}
	for _, statementJSON := range statements {
		var statement map[string]json.RawMessage
		if err := json.Unmarshal(statementJSON, &statement); err != nil {
			return fmt.Errorf("legacy state %s policy Statement is malformed", source)
		}
		var condition map[string]json.RawMessage
		if err := json.Unmarshal(statement["Condition"], &condition); err != nil {
			continue
		}
		var stringEquals map[string]json.RawMessage
		if err := json.Unmarshal(condition["StringEquals"], &stringEquals); err != nil {
			continue
		}
		for key, valueJSON := range stringEquals {
			clusterName, accepted, err := decodeAWSLegacyClusterCondition(key, valueJSON)
			if err != nil {
				return fmt.Errorf("legacy state %s contains malformed phase-two IAM condition %q", source, key)
			}
			if accepted {
				collector.add(legacyScopeFieldClusterName, clusterName, source+" StringEquals "+key)
			}
		}
	}
	return nil
}

func decodeAWSLegacyPolicyStatements(rawStatements json.RawMessage) ([]json.RawMessage, error) {
	trimmed := bytes.TrimSpace(rawStatements)
	if len(trimmed) == 0 {
		return nil, fmt.Errorf("empty Statement")
	}
	if trimmed[0] == '{' {
		return []json.RawMessage{trimmed}, nil
	}
	var statements []json.RawMessage
	if err := json.Unmarshal(trimmed, &statements); err != nil {
		return nil, err
	}
	return statements, nil
}

func decodeAWSLegacyClusterCondition(key string, rawValue json.RawMessage) (string, bool, error) {
	prefixes := []string{
		"aws:RequestTag/kubernetes.io/cluster/",
		"ec2:ResourceTag/kubernetes.io/cluster/",
		"ec2:ResourceTag/sigs.k8s.io/cluster-api-provider-aws/cluster/",
	}
	for _, prefix := range prefixes {
		if !strings.HasPrefix(key, prefix) {
			continue
		}
		clusterName := strings.TrimPrefix(key, prefix)
		var value string
		if json.Unmarshal(rawValue, &value) != nil || value != "owned" || !awsClusterNamePattern.MatchString(clusterName) || len(clusterName) > 63 {
			return "", false, fmt.Errorf("invalid cluster ownership condition")
		}
		return clusterName, true, nil
	}

	if key != "aws:RequestTag/elbv2.k8s.aws/cluster" && key != "aws:ResourceTag/elbv2.k8s.aws/cluster" {
		return "", false, nil
	}
	var clusterName string
	if json.Unmarshal(rawValue, &clusterName) != nil || !awsClusterNamePattern.MatchString(clusterName) || len(clusterName) > 63 {
		return "", false, fmt.Errorf("invalid ELB cluster ownership condition")
	}
	return clusterName, true, nil
}

func awsLegacyReadyInstances(resource rawTerraformResource) ([]rawTerraformInstance, error) {
	ready := make([]rawTerraformInstance, 0, len(resource.Instances))
	for _, instance := range resource.Instances {
		if instance.Deposed != "" {
			continue
		}
		if instance.Status != "" {
			return nil, fmt.Errorf("legacy state %s contains a non-ready evidence instance", awsLegacyResourceAddress(resource))
		}
		ready = append(ready, instance)
	}
	return ready, nil
}

func awsLegacyResourceHasReadyInstance(resource rawTerraformResource) (bool, error) {
	instances, err := awsLegacyReadyInstances(resource)
	return len(instances) > 0, err
}

func awsLegacyResourceAddress(resource rawTerraformResource) string {
	address := resource.Type + "." + resource.Name
	if resource.Module != "" {
		return resource.Module + "." + address
	}
	return address
}

func missingAWSLegacyScopeFields(scope AWSDeploymentScope) []string {
	missing := make([]string, 0, 5)
	if strings.TrimSpace(scope.Region) == "" {
		missing = append(missing, legacyScopeFieldRegion)
	}
	if strings.TrimSpace(scope.VPC.Mode) == "" {
		missing = append(missing, legacyScopeFieldVPCMode)
	} else {
		switch scope.VPC.Mode {
		case awsVPCModeDittocloud:
			if strings.TrimSpace(scope.VPC.Name) == "" {
				missing = append(missing, legacyScopeFieldVPCName)
			}
			if strings.TrimSpace(scope.VPC.CIDR) == "" {
				missing = append(missing, legacyScopeFieldVPCCIDR)
			}
		case awsVPCModeExisting:
			if strings.TrimSpace(scope.VPC.ID) == "" {
				missing = append(missing, legacyScopeFieldVPCID)
			}
		}
	}
	if strings.TrimSpace(scope.ClusterType) == "" {
		missing = append(missing, legacyScopeFieldClusterType)
	}
	return missing
}
