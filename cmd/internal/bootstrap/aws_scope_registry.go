package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"sort"
	"strings"
)

const supportedTerraformStateVersion = 4

type awsStateScopeIdentity struct {
	ScopeRef string
	Default  bool
}

type awsStateScopeRegistry struct {
	Present                  bool
	StateEmpty               bool
	ApparentScopeData        bool
	Scopes                   map[string]awsStateScopeIdentity
	DefaultScopeRef          string
	AppliedTagPolicyVersions map[string]int
	ConfigurationPresent     bool
	Configurations           AWSDeploymentScopes
}

type rawTerraformState struct {
	Version          int                        `json:"version"`
	TerraformVersion string                     `json:"terraform_version"`
	Serial           int64                      `json:"serial"`
	Lineage          string                     `json:"lineage"`
	Outputs          map[string]json.RawMessage `json:"outputs"`
	Resources        []rawTerraformResource     `json:"resources"`
}

type rawTerraformResource struct {
	Module    string                 `json:"module"`
	Mode      string                 `json:"mode"`
	Type      string                 `json:"type"`
	Name      string                 `json:"name"`
	Instances []rawTerraformInstance `json:"instances"`
}

type rawTerraformInstance struct {
	IndexKey   json.RawMessage `json:"index_key"`
	Attributes json.RawMessage `json:"attributes"`
	Deposed    string          `json:"deposed"`
	Status     string          `json:"status"`
}

func loadAWSStateScopeRegistry(statePath string) (awsStateScopeRegistry, error) {
	registry := awsStateScopeRegistry{
		StateEmpty:               true,
		Scopes:                   map[string]awsStateScopeIdentity{},
		AppliedTagPolicyVersions: map[string]int{},
		Configurations:           AWSDeploymentScopes{},
	}

	state, exists, err := loadRawTerraformState(statePath)
	if err != nil {
		return registry, err
	}
	if !exists {
		return registry, nil
	}

	registry.StateEmpty = len(state.Resources) == 0 && len(state.Outputs) == 0
	registryResourceCount := 0
	tagPolicyResourceCount := 0
	configurationResourceCount := 0
	for _, resource := range state.Resources {
		if resource.Type == "terraform_data" && resource.Name == "scope_registry" {
			if !isAWSStateScopeRegistryResource(resource) {
				return registry, fmt.Errorf(
					"terraform state %q contains terraform_data.scope_registry outside its required root managed address",
					statePath,
				)
			}
			registryResourceCount++
			if registryResourceCount > 1 {
				return registry, fmt.Errorf("terraform state %q contains duplicate root terraform_data.scope_registry resources", statePath)
			}
			if err := decodeAWSStateScopeRegistryResource(statePath, resource, &registry); err != nil {
				return registry, err
			}
			continue
		}
		if resource.Type == "terraform_data" && resource.Name == "scope_tag_policy" {
			registry.ApparentScopeData = true
			if !isAWSStateScopeTagPolicyResource(resource) {
				return registry, fmt.Errorf(
					"terraform state %q contains terraform_data.scope_tag_policy outside its required root managed address",
					statePath,
				)
			}
			tagPolicyResourceCount++
			if tagPolicyResourceCount > 1 {
				return registry, fmt.Errorf("terraform state %q contains duplicate root terraform_data.scope_tag_policy resources", statePath)
			}
			if err := decodeAWSStateScopeTagPolicyResource(statePath, resource, &registry); err != nil {
				return registry, err
			}
			continue
		}
		if resource.Type == "terraform_data" && resource.Name == "scope_configuration" {
			registry.ApparentScopeData = true
			if !isAWSStateScopeConfigurationResource(resource) {
				return registry, fmt.Errorf(
					"terraform state %q contains terraform_data.scope_configuration outside its required root managed address",
					statePath,
				)
			}
			configurationResourceCount++
			if configurationResourceCount > 1 {
				return registry, fmt.Errorf("terraform state %q contains duplicate root terraform_data.scope_configuration resources", statePath)
			}
			if err := decodeAWSStateScopeConfigurationResource(statePath, resource, &registry); err != nil {
				return registry, err
			}
			continue
		}
		if isApparentAWSScopedResource(resource) {
			registry.ApparentScopeData = true
		}
	}

	if registry.Present {
		if len(registry.Scopes) == 0 {
			return registry, fmt.Errorf("terraform state %q contains an empty scope registry", statePath)
		}
		defaultScopeRefs := make([]string, 0, 1)
		for scopeRef, identity := range registry.Scopes {
			if identity.Default {
				defaultScopeRefs = append(defaultScopeRefs, scopeRef)
			}
		}
		sort.Strings(defaultScopeRefs)
		if len(defaultScopeRefs) != 1 {
			return registry, fmt.Errorf(
				"terraform state %q scope registry must contain exactly one default scope; found %d",
				statePath,
				len(defaultScopeRefs),
			)
		}
		registry.DefaultScopeRef = defaultScopeRefs[0]
		for scopeRef := range registry.AppliedTagPolicyVersions {
			if _, exists := registry.Scopes[scopeRef]; !exists {
				return registry, fmt.Errorf(
					"terraform state %q contains an applied tag-policy marker for unknown scope %q",
					statePath,
					scopeRef,
				)
			}
		}
		for scopeRef := range registry.Configurations {
			if _, exists := registry.Scopes[scopeRef]; !exists {
				return registry, fmt.Errorf(
					"terraform state %q contains a configuration snapshot for unknown scope %q",
					statePath,
					scopeRef,
				)
			}
		}
	}

	if !registry.Present && registry.ApparentScopeData {
		return registry, fmt.Errorf(
			"terraform state %q contains apparent scope-mode resources but no valid root terraform_data.scope_registry; manual recovery is required",
			statePath,
		)
	}
	return registry, nil
}

func validateAWSLegacyModeState(statePath string) error {
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return err
	}
	if registry.Present {
		return fmt.Errorf(
			"terraform state %q is managed in AWS scope mode; rerun with --scopes=true and the matching --scopes-file; no Terraform operation was run",
			statePath,
		)
	}
	return nil
}

func loadRawTerraformState(statePath string) (rawTerraformState, bool, error) {
	var state rawTerraformState
	content, err := os.ReadFile(statePath)
	if errors.Is(err, os.ErrNotExist) {
		return state, false, nil
	}
	if err != nil {
		return state, false, fmt.Errorf("unable to read Terraform state %q: %w", statePath, err)
	}
	if len(bytes.TrimSpace(content)) == 0 {
		return state, false, fmt.Errorf("terraform state %q is empty and cannot be decoded", statePath)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(content, &topLevel); err != nil {
		return state, false, fmt.Errorf("unable to decode Terraform state %q: %w", statePath, err)
	}
	for _, requiredField := range []string{"version", "terraform_version", "serial", "lineage", "outputs", "resources"} {
		if topLevel[requiredField] == nil {
			return state, false, fmt.Errorf("terraform state %q is missing required field %q", statePath, requiredField)
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&state); err != nil {
		return state, false, fmt.Errorf("unable to decode Terraform state %q: %w", statePath, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return state, false, fmt.Errorf("terraform state %q contains multiple JSON values", statePath)
		}
		return state, false, fmt.Errorf("terraform state %q contains trailing malformed JSON: %w", statePath, err)
	}
	if state.Version != supportedTerraformStateVersion {
		return state, false, fmt.Errorf(
			"terraform state %q uses unsupported format version %d; Dittocloud supports version %d",
			statePath,
			state.Version,
			supportedTerraformStateVersion,
		)
	}
	if strings.TrimSpace(state.TerraformVersion) == "" || state.Serial < 0 || strings.TrimSpace(state.Lineage) == "" {
		return state, false, fmt.Errorf("terraform state %q has invalid version, serial, or lineage metadata", statePath)
	}
	if !jsonObject(topLevel["outputs"]) || !jsonArray(topLevel["resources"]) {
		return state, false, fmt.Errorf("terraform state %q outputs or resources have an invalid shape", statePath)
	}
	return state, true, nil
}

func isAWSStateScopeRegistryResource(resource rawTerraformResource) bool {
	return resource.Module == "" && resource.Mode == "managed" && resource.Type == "terraform_data" && resource.Name == "scope_registry"
}

func isAWSStateScopeTagPolicyResource(resource rawTerraformResource) bool {
	return resource.Module == "" && resource.Mode == "managed" && resource.Type == "terraform_data" && resource.Name == "scope_tag_policy"
}

func isAWSStateScopeConfigurationResource(resource rawTerraformResource) bool {
	return resource.Module == "" && resource.Mode == "managed" && resource.Type == "terraform_data" && resource.Name == "scope_configuration"
}

func jsonObject(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 1 && trimmed[0] == '{' && trimmed[len(trimmed)-1] == '}'
}

func jsonArray(value json.RawMessage) bool {
	trimmed := bytes.TrimSpace(value)
	return len(trimmed) > 1 && trimmed[0] == '[' && trimmed[len(trimmed)-1] == ']'
}

func isApparentAWSScopedResource(resource rawTerraformResource) bool {
	if strings.Contains(resource.Module, "module.scoped_") {
		return true
	}
	return strings.HasPrefix(resource.Name, "scoped_") ||
		resource.Name == "scope_tag_policy" ||
		resource.Name == "scope_configuration"
}

func decodeAWSStateScopeRegistryResource(statePath string, resource rawTerraformResource, registry *awsStateScopeRegistry) error {
	registry.Present = true
	if len(resource.Instances) == 0 {
		return fmt.Errorf("terraform state %q contains a scope registry with no instances", statePath)
	}

	for _, instance := range resource.Instances {
		if instance.Deposed != "" || instance.Status != "" {
			return fmt.Errorf("terraform state %q contains a deposed or non-ready scope registry instance", statePath)
		}

		var scopeRef string
		if len(instance.IndexKey) == 0 || json.Unmarshal(instance.IndexKey, &scopeRef) != nil || !awsScopeReferencePattern.MatchString(scopeRef) {
			return fmt.Errorf("terraform state %q contains an invalid scope registry address key", statePath)
		}
		if _, exists := registry.Scopes[scopeRef]; exists {
			return fmt.Errorf("terraform state %q contains duplicate scope registry key %q", statePath, scopeRef)
		}

		identity, err := decodeAWSStateScopeIdentity(instance.Attributes)
		if err != nil {
			return fmt.Errorf("terraform state %q scope registry key %q is invalid: %w", statePath, scopeRef, err)
		}
		if identity.ScopeRef != scopeRef {
			return fmt.Errorf(
				"terraform state %q scope registry address key %q does not match stored scope_ref %q",
				statePath,
				scopeRef,
				identity.ScopeRef,
			)
		}
		registry.Scopes[scopeRef] = identity
	}
	return nil
}

func decodeAWSStateScopeTagPolicyResource(statePath string, resource rawTerraformResource, registry *awsStateScopeRegistry) error {
	if len(resource.Instances) == 0 {
		return fmt.Errorf("terraform state %q contains an applied tag-policy resource with no instances", statePath)
	}

	for _, instance := range resource.Instances {
		if instance.Deposed != "" || instance.Status != "" {
			return fmt.Errorf("terraform state %q contains a deposed or non-ready applied tag-policy instance", statePath)
		}

		var scopeRef string
		if len(instance.IndexKey) == 0 || json.Unmarshal(instance.IndexKey, &scopeRef) != nil || !awsScopeReferencePattern.MatchString(scopeRef) {
			return fmt.Errorf("terraform state %q contains an invalid applied tag-policy address key", statePath)
		}
		if _, exists := registry.AppliedTagPolicyVersions[scopeRef]; exists {
			return fmt.Errorf("terraform state %q contains duplicate applied tag-policy key %q", statePath, scopeRef)
		}

		storedScopeRef, policyVersion, err := decodeAWSStateScopeTagPolicy(instance.Attributes)
		if err != nil {
			return fmt.Errorf("terraform state %q applied tag-policy key %q is invalid: %w", statePath, scopeRef, err)
		}
		if storedScopeRef != scopeRef {
			return fmt.Errorf(
				"terraform state %q applied tag-policy address key %q does not match stored scope_ref %q",
				statePath,
				scopeRef,
				storedScopeRef,
			)
		}
		registry.AppliedTagPolicyVersions[scopeRef] = policyVersion
	}
	return nil
}

func decodeAWSStateScopeConfigurationResource(statePath string, resource rawTerraformResource, registry *awsStateScopeRegistry) error {
	registry.ConfigurationPresent = true
	if len(resource.Instances) == 0 {
		return fmt.Errorf("terraform state %q contains a scope configuration resource with no instances", statePath)
	}

	for _, instance := range resource.Instances {
		if instance.Deposed != "" || instance.Status != "" {
			return fmt.Errorf("terraform state %q contains a deposed or non-ready scope configuration instance", statePath)
		}

		var scopeRef string
		if len(instance.IndexKey) == 0 || json.Unmarshal(instance.IndexKey, &scopeRef) != nil || !awsScopeReferencePattern.MatchString(scopeRef) {
			return fmt.Errorf("terraform state %q contains an invalid scope configuration address key", statePath)
		}
		if _, exists := registry.Configurations[scopeRef]; exists {
			return fmt.Errorf("terraform state %q contains duplicate scope configuration key %q", statePath, scopeRef)
		}

		storedScopeRef, configuration, err := decodeAWSStateScopeConfiguration(instance.Attributes)
		if err != nil {
			return fmt.Errorf("terraform state %q scope configuration key %q is invalid: %w", statePath, scopeRef, err)
		}
		if storedScopeRef != scopeRef {
			return fmt.Errorf(
				"terraform state %q scope configuration address key %q does not match stored scope_ref %q",
				statePath,
				scopeRef,
				storedScopeRef,
			)
		}
		registry.Configurations[scopeRef] = configuration
	}
	return nil
}

func decodeAWSStateScopeConfiguration(attributesJSON json.RawMessage) (string, AWSDeploymentScope, error) {
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
		return "", AWSDeploymentScope{}, fmt.Errorf("attributes are malformed: %w", err)
	}
	inputJSON, exists := attributes["input"]
	if !exists {
		return "", AWSDeploymentScope{}, fmt.Errorf("input is missing")
	}

	var dynamicValue map[string]json.RawMessage
	if err := json.Unmarshal(inputJSON, &dynamicValue); err != nil {
		return "", AWSDeploymentScope{}, fmt.Errorf("input is malformed: %w", err)
	}
	if len(dynamicValue) != 2 || dynamicValue["value"] == nil || dynamicValue["type"] == nil {
		return "", AWSDeploymentScope{}, fmt.Errorf("input must use Terraform's exact dynamic value and type encoding")
	}
	var stored map[string]json.RawMessage
	if err := json.Unmarshal(dynamicValue["value"], &stored); err != nil {
		return "", AWSDeploymentScope{}, fmt.Errorf("input value is malformed: %w", err)
	}
	if len(stored) != 3 || stored["schema_version"] == nil || stored["scope_ref"] == nil || stored["configuration"] == nil {
		return "", AWSDeploymentScope{}, fmt.Errorf("input value must contain exactly schema_version, scope_ref, and configuration")
	}
	version, err := decodeAWSStateExactNumber(stored["schema_version"])
	if err != nil || version < awsMinimumScopeConfigurationSchemaVersion || version > awsScopeConfigurationSchemaVersion {
		return "", AWSDeploymentScope{}, fmt.Errorf(
			"unsupported schema_version; expected %d through %d",
			awsMinimumScopeConfigurationSchemaVersion,
			awsScopeConfigurationSchemaVersion,
		)
	}
	if err := validateAWSStateScopeConfigurationType(dynamicValue["type"], version); err != nil {
		return "", AWSDeploymentScope{}, err
	}

	var scopeRef string
	if err := json.Unmarshal(stored["scope_ref"], &scopeRef); err != nil || !awsScopeReferencePattern.MatchString(scopeRef) {
		return "", AWSDeploymentScope{}, fmt.Errorf("scope_ref is not a valid generated Dittocloud scope reference")
	}
	configuration, err := decodeAWSStateScopeConfigurationValue(scopeRef, stored["configuration"], version)
	if err != nil {
		return "", AWSDeploymentScope{}, err
	}
	return scopeRef, configuration, nil
}

func validateAWSStateScopeConfigurationType(typeJSON json.RawMessage, version int) error {
	var actual any
	if err := json.Unmarshal(typeJSON, &actual); err != nil {
		return fmt.Errorf("input type descriptor is malformed")
	}
	expected := []any{
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
						awsStateScopeVPCTypeAttributes(version),
					},
				},
			},
		},
	}
	if !reflect.DeepEqual(actual, expected) {
		return fmt.Errorf("input type descriptor does not match scope configuration schema %d", version)
	}
	return nil
}

func awsStateScopeVPCTypeAttributes(version int) map[string]any {
	attributes := map[string]any{
		"mode":             "string",
		"name":             "string",
		"cidr":             "string",
		"id":               "string",
		"nat_gateway_name": "string",
	}
	if version >= 2 {
		attributes["secondary_cidr"] = "string"
		attributes["public_subnet_netmask"] = "number"
		attributes["private_subnet_netmask"] = "number"
		attributes["nat_gateway_eip_allocation_ids"] = []any{"list", "string"}
	}
	return attributes
}

func decodeAWSStateScopeConfigurationValue(scopeRef string, valueJSON json.RawMessage, version int) (AWSDeploymentScope, error) {
	var value map[string]json.RawMessage
	if err := json.Unmarshal(valueJSON, &value); err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("configuration is malformed: %w", err)
	}
	expectedFields := []string{"default", "cluster_name", "cluster_type", "region", "scope_tag_policy_version", "vpc"}
	if !hasExactJSONFields(value, expectedFields...) {
		return AWSDeploymentScope{}, fmt.Errorf("configuration must contain exactly %s", strings.Join(expectedFields, ", "))
	}

	var configuration AWSDeploymentScope
	if err := json.Unmarshal(value["default"], &configuration.Default); err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("configuration.default must be a boolean")
	}
	clusterName, err := decodeAWSStateOptionalString(value["cluster_name"])
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("configuration.cluster_name must be a string or null")
	}
	configuration.ClusterName = clusterName
	if err := json.Unmarshal(value["cluster_type"], &configuration.ClusterType); err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("configuration.cluster_type must be a string")
	}
	if err := json.Unmarshal(value["region"], &configuration.Region); err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("configuration.region must be a string")
	}
	policyVersion, err := decodeAWSStateExactNumber(value["scope_tag_policy_version"])
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("configuration.scope_tag_policy_version must be a number")
	}
	configuration.ScopeTagPolicyVersion = policyVersion

	var vpc map[string]json.RawMessage
	if err := json.Unmarshal(value["vpc"], &vpc); err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("configuration.vpc is malformed: %w", err)
	}
	vpcFields := []string{"mode", "name", "cidr", "id", "nat_gateway_name"}
	if version >= 2 {
		vpcFields = append(
			vpcFields,
			"secondary_cidr",
			"public_subnet_netmask",
			"private_subnet_netmask",
			"nat_gateway_eip_allocation_ids",
		)
	}
	if !hasExactJSONFields(vpc, vpcFields...) {
		return AWSDeploymentScope{}, fmt.Errorf("configuration.vpc must contain exactly %s", strings.Join(vpcFields, ", "))
	}
	if err := json.Unmarshal(vpc["mode"], &configuration.VPC.Mode); err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("configuration.vpc.mode must be a string")
	}
	optionalVPCFields := []struct {
		name        string
		destination *string
	}{
		{name: "name", destination: &configuration.VPC.Name},
		{name: "cidr", destination: &configuration.VPC.CIDR},
		{name: "id", destination: &configuration.VPC.ID},
		{name: "nat_gateway_name", destination: &configuration.VPC.NATGatewayName},
	}
	if version >= 2 {
		optionalVPCFields = append(
			optionalVPCFields,
			struct {
				name        string
				destination *string
			}{name: "secondary_cidr", destination: &configuration.VPC.SecondaryCIDR},
		)
	}
	for _, field := range optionalVPCFields {
		decoded, err := decodeAWSStateOptionalString(vpc[field.name])
		if err != nil {
			return AWSDeploymentScope{}, fmt.Errorf("configuration.vpc.%s must be a string or null", field.name)
		}
		*field.destination = decoded
	}
	if version >= 2 {
		optionalNetmasks := []struct {
			name        string
			destination *int
		}{
			{name: "public_subnet_netmask", destination: &configuration.VPC.PublicSubnetNetmask},
			{name: "private_subnet_netmask", destination: &configuration.VPC.PrivateSubnetNetmask},
		}
		for _, field := range optionalNetmasks {
			decoded, err := decodeAWSStateOptionalNumber(vpc[field.name])
			if err != nil {
				return AWSDeploymentScope{}, fmt.Errorf("configuration.vpc.%s must be a number or null", field.name)
			}
			*field.destination = decoded
		}
		allocationIDs, err := decodeAWSStateStringList(vpc["nat_gateway_eip_allocation_ids"])
		if err != nil {
			return AWSDeploymentScope{}, fmt.Errorf("configuration.vpc.nat_gateway_eip_allocation_ids must be a list of strings or null")
		}
		configuration.VPC.NATGatewayEIPAllocationIDs = allocationIDs
	}
	// A schema 1 snapshot predates the DMZ split, so it carries the sizing that
	// module was pinned to rather than today's defaults.
	if version < 2 && configuration.VPC.Mode == awsVPCModeDittocloud {
		configuration.VPC.PublicSubnetNetmask = awsLegacyPublicSubnetNetmask
		configuration.VPC.PrivateSubnetNetmask = awsLegacyPrivateSubnetNetmask
	}
	if err := validateAWSDeploymentScopeFields(scopeRef, configuration); err != nil {
		return AWSDeploymentScope{}, err
	}
	return configuration, nil
}

func hasExactJSONFields(value map[string]json.RawMessage, fields ...string) bool {
	if len(value) != len(fields) {
		return false
	}
	for _, field := range fields {
		if value[field] == nil {
			return false
		}
	}
	return true
}

func decodeAWSStateOptionalNumber(value json.RawMessage) (int, error) {
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return 0, nil
	}
	return decodeAWSStateExactNumber(value)
}

func decodeAWSStateStringList(value json.RawMessage) ([]string, error) {
	if value == nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return nil, nil
	}
	var decoded []string
	if err := json.Unmarshal(value, &decoded); err != nil {
		return nil, err
	}
	if len(decoded) == 0 {
		return nil, nil
	}
	return decoded, nil
}

func decodeAWSStateOptionalString(value json.RawMessage) (string, error) {
	if bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
		return "", nil
	}
	var decoded string
	if err := json.Unmarshal(value, &decoded); err != nil {
		return "", err
	}
	return decoded, nil
}

func decodeAWSStateScopeTagPolicy(attributesJSON json.RawMessage) (string, int, error) {
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
		return "", 0, fmt.Errorf("attributes are malformed: %w", err)
	}
	inputJSON, exists := attributes["input"]
	if !exists {
		return "", 0, fmt.Errorf("input is missing")
	}

	var dynamicValue map[string]json.RawMessage
	if err := json.Unmarshal(inputJSON, &dynamicValue); err != nil {
		return "", 0, fmt.Errorf("input is malformed: %w", err)
	}
	if len(dynamicValue) != 2 || dynamicValue["value"] == nil || dynamicValue["type"] == nil {
		return "", 0, fmt.Errorf("input must use Terraform's exact dynamic value and type encoding")
	}

	expectedFields := map[string]string{
		"schema_version": "number",
		"scope_ref":      "string",
		"policy_version": "number",
	}
	if err := validateAWSStateDynamicObjectType(dynamicValue["type"], expectedFields); err != nil {
		return "", 0, err
	}

	var storedPolicy map[string]json.RawMessage
	if err := json.Unmarshal(dynamicValue["value"], &storedPolicy); err != nil {
		return "", 0, fmt.Errorf("input value is malformed: %w", err)
	}
	if len(storedPolicy) != 3 || storedPolicy["schema_version"] == nil || storedPolicy["scope_ref"] == nil || storedPolicy["policy_version"] == nil {
		return "", 0, fmt.Errorf("input value must contain exactly schema_version, scope_ref, and policy_version")
	}
	if version, err := decodeAWSStateExactNumber(storedPolicy["schema_version"]); err != nil || version != 1 {
		return "", 0, fmt.Errorf("unsupported schema_version; expected 1")
	}

	var scopeRef string
	if err := json.Unmarshal(storedPolicy["scope_ref"], &scopeRef); err != nil || !awsScopeReferencePattern.MatchString(scopeRef) {
		return "", 0, fmt.Errorf("scope_ref is not a valid generated Dittocloud scope reference")
	}
	policyVersion, err := decodeAWSStateExactNumber(storedPolicy["policy_version"])
	if err != nil || (policyVersion != 0 && policyVersion != 1) {
		return "", 0, fmt.Errorf("policy_version must be 0 or 1")
	}
	return scopeRef, policyVersion, nil
}

func decodeAWSStateExactNumber(value json.RawMessage) (int, error) {
	decoder := json.NewDecoder(bytes.NewReader(value))
	decoder.UseNumber()
	var number json.Number
	if err := decoder.Decode(&number); err != nil {
		return 0, err
	}
	parsed, err := number.Int64()
	if err != nil {
		return 0, err
	}
	return int(parsed), nil
}

func validateAWSStateDynamicObjectType(typeJSON json.RawMessage, expectedFields map[string]string) error {
	var descriptor []json.RawMessage
	if err := json.Unmarshal(typeJSON, &descriptor); err != nil || len(descriptor) != 2 {
		return fmt.Errorf("input type descriptor is malformed")
	}
	var kind string
	if err := json.Unmarshal(descriptor[0], &kind); err != nil || kind != "object" {
		return fmt.Errorf("input type descriptor must describe an object")
	}
	var fields map[string]string
	if err := json.Unmarshal(descriptor[1], &fields); err != nil {
		return fmt.Errorf("input object type descriptor is malformed")
	}
	if len(fields) != len(expectedFields) {
		return fmt.Errorf("input type descriptor contains an unexpected field set")
	}
	for field, expectedType := range expectedFields {
		if fields[field] != expectedType {
			return fmt.Errorf("input type descriptor field %s must be %s", field, expectedType)
		}
	}
	return nil
}

func decodeAWSStateScopeIdentity(attributesJSON json.RawMessage) (awsStateScopeIdentity, error) {
	var attributes map[string]json.RawMessage
	if err := json.Unmarshal(attributesJSON, &attributes); err != nil {
		return awsStateScopeIdentity{}, fmt.Errorf("attributes are malformed: %w", err)
	}
	inputJSON, exists := attributes["input"]
	if !exists {
		return awsStateScopeIdentity{}, fmt.Errorf("input is missing")
	}

	var dynamicValue map[string]json.RawMessage
	if err := json.Unmarshal(inputJSON, &dynamicValue); err != nil {
		return awsStateScopeIdentity{}, fmt.Errorf("input is malformed: %w", err)
	}
	if len(dynamicValue) != 2 || dynamicValue["value"] == nil || dynamicValue["type"] == nil {
		return awsStateScopeIdentity{}, fmt.Errorf("input must use Terraform's exact dynamic value and type encoding")
	}
	if err := validateAWSStateScopeIdentityType(dynamicValue["type"]); err != nil {
		return awsStateScopeIdentity{}, err
	}

	var storedIdentity map[string]json.RawMessage
	if err := json.Unmarshal(dynamicValue["value"], &storedIdentity); err != nil {
		return awsStateScopeIdentity{}, fmt.Errorf("input value is malformed: %w", err)
	}
	if len(storedIdentity) != 3 || storedIdentity["schema_version"] == nil || storedIdentity["scope_ref"] == nil || storedIdentity["default"] == nil {
		return awsStateScopeIdentity{}, fmt.Errorf("input value must contain exactly schema_version, scope_ref, and default")
	}

	decoder := json.NewDecoder(bytes.NewReader(storedIdentity["schema_version"]))
	decoder.UseNumber()
	var schemaVersion json.Number
	if err := decoder.Decode(&schemaVersion); err != nil || schemaVersion.String() != "1" {
		return awsStateScopeIdentity{}, fmt.Errorf("unsupported schema_version %q; expected 1", schemaVersion.String())
	}

	var identity awsStateScopeIdentity
	if err := json.Unmarshal(storedIdentity["scope_ref"], &identity.ScopeRef); err != nil || !awsScopeReferencePattern.MatchString(identity.ScopeRef) {
		return awsStateScopeIdentity{}, fmt.Errorf("scope_ref is not a valid generated Dittocloud scope reference")
	}
	if err := json.Unmarshal(storedIdentity["default"], &identity.Default); err != nil {
		return awsStateScopeIdentity{}, fmt.Errorf("default must be a boolean")
	}
	return identity, nil
}

func validateAWSStateScopeIdentityType(typeJSON json.RawMessage) error {
	var descriptor []json.RawMessage
	if err := json.Unmarshal(typeJSON, &descriptor); err != nil || len(descriptor) != 2 {
		return fmt.Errorf("input type descriptor is malformed")
	}
	var kind string
	if err := json.Unmarshal(descriptor[0], &kind); err != nil || kind != "object" {
		return fmt.Errorf("input type descriptor must describe an object")
	}
	var fields map[string]string
	if err := json.Unmarshal(descriptor[1], &fields); err != nil {
		return fmt.Errorf("input object type descriptor is malformed")
	}
	expectedFields := map[string]string{
		"schema_version": "number",
		"scope_ref":      "string",
		"default":        "bool",
	}
	if len(fields) != len(expectedFields) {
		return fmt.Errorf("input type descriptor must contain exactly schema_version, scope_ref, and default")
	}
	for field, expectedType := range expectedFields {
		if fields[field] != expectedType {
			return fmt.Errorf("input type descriptor field %s must be %s", field, expectedType)
		}
	}
	return nil
}

func validateAWSStateScopeLifecycle(
	statePath string,
	desiredScopes AWSDeploymentScopes,
	allowedRemovals []string,
) error {
	allowedRemovalSet := make(map[string]struct{}, len(allowedRemovals))
	for _, scopeRef := range allowedRemovals {
		if !awsScopeReferencePattern.MatchString(scopeRef) {
			return fmt.Errorf("--allow-scope-removal value %q is not a valid generated Dittocloud scope reference", scopeRef)
		}
		if _, duplicate := allowedRemovalSet[scopeRef]; duplicate {
			return fmt.Errorf("duplicate --allow-scope-removal value %q", scopeRef)
		}
		allowedRemovalSet[scopeRef] = struct{}{}
	}

	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return err
	}
	if !registry.Present {
		if len(allowedRemovalSet) > 0 {
			return fmt.Errorf("--allow-scope-removal cannot be used because Terraform state %q has no scope registry", statePath)
		}
		if registry.StateEmpty {
			return nil
		}
		if len(desiredScopes) != 1 {
			return fmt.Errorf(
				"legacy Terraform state %q must first migrate to exactly one generated default scope before additional scopes are added",
				statePath,
			)
		}
		return fmt.Errorf(
			"legacy Terraform state %q has no scope registry; run bootstrap aws scopes migrate seed-registry before a normal scope-mode plan or apply",
			statePath,
		)
	}

	desiredDefault, exists := desiredScopes[registry.DefaultScopeRef]
	if !exists || !desiredDefault.Default {
		return fmt.Errorf(
			"default scope %q is immutable and cannot be removed or reassigned; manual recovery is required",
			registry.DefaultScopeRef,
		)
	}

	missingScopeRefs := make([]string, 0)
	addedScopeRefs := make([]string, 0)
	for scopeRef := range registry.Scopes {
		if _, exists := desiredScopes[scopeRef]; !exists {
			missingScopeRefs = append(missingScopeRefs, scopeRef)
		}
	}
	for scopeRef := range desiredScopes {
		if _, exists := registry.Scopes[scopeRef]; !exists {
			addedScopeRefs = append(addedScopeRefs, scopeRef)
		}
	}
	sort.Strings(missingScopeRefs)
	sort.Strings(addedScopeRefs)
	if len(missingScopeRefs) > 0 && len(addedScopeRefs) > 0 {
		return fmt.Errorf(
			"scope removal and addition cannot occur together because that is indistinguishable from an implicit scope reference replacement; remove %s and add %s in separate operations",
			strings.Join(missingScopeRefs, ", "),
			strings.Join(addedScopeRefs, ", "),
		)
	}

	missingAuthorization := make([]string, 0)
	for _, scopeRef := range missingScopeRefs {
		if _, allowed := allowedRemovalSet[scopeRef]; !allowed {
			missingAuthorization = append(missingAuthorization, scopeRef)
		}
	}
	unusedAuthorization := make([]string, 0)
	for scopeRef := range allowedRemovalSet {
		if _, missing := registry.Scopes[scopeRef]; !missing {
			unusedAuthorization = append(unusedAuthorization, scopeRef)
			continue
		}
		if _, stillDesired := desiredScopes[scopeRef]; stillDesired {
			unusedAuthorization = append(unusedAuthorization, scopeRef)
		}
	}
	sort.Strings(unusedAuthorization)
	if len(missingAuthorization) > 0 || len(unusedAuthorization) > 0 {
		return fmt.Errorf(
			"scope removal authorization must exactly match state-backed scopes omitted from YAML; missing authorization: [%s]; unused authorization: [%s]",
			strings.Join(missingAuthorization, ", "),
			strings.Join(unusedAuthorization, ", "),
		)
	}
	return nil
}
