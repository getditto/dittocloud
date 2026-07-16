package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
)

const supportedTerraformStateVersion = 4

type awsStateScopeIdentity struct {
	ScopeRef string
	Default  bool
}

type awsStateScopeRegistry struct {
	Present           bool
	StateEmpty        bool
	ApparentScopeData bool
	Scopes            map[string]awsStateScopeIdentity
	DefaultScopeRef   string
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
		StateEmpty: true,
		Scopes:     map[string]awsStateScopeIdentity{},
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
	for _, resource := range state.Resources {
		if resource.Type == "terraform_data" && resource.Name == "scope_registry" {
			if !isAWSStateScopeRegistryResource(resource) {
				return registry, fmt.Errorf(
					"Terraform state %q contains terraform_data.scope_registry outside its required root managed address",
					statePath,
				)
			}
			registryResourceCount++
			if registryResourceCount > 1 {
				return registry, fmt.Errorf("Terraform state %q contains duplicate root terraform_data.scope_registry resources", statePath)
			}
			if err := decodeAWSStateScopeRegistryResource(statePath, resource, &registry); err != nil {
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
			return registry, fmt.Errorf("Terraform state %q contains an empty scope registry", statePath)
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
				"Terraform state %q scope registry must contain exactly one default scope; found %d",
				statePath,
				len(defaultScopeRefs),
			)
		}
		registry.DefaultScopeRef = defaultScopeRefs[0]
	}

	if !registry.Present && registry.ApparentScopeData {
		return registry, fmt.Errorf(
			"Terraform state %q contains apparent scope-mode resources but no valid root terraform_data.scope_registry; manual recovery is required",
			statePath,
		)
	}
	return registry, nil
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
		return state, false, fmt.Errorf("Terraform state %q is empty and cannot be decoded", statePath)
	}
	var topLevel map[string]json.RawMessage
	if err := json.Unmarshal(content, &topLevel); err != nil {
		return state, false, fmt.Errorf("unable to decode Terraform state %q: %w", statePath, err)
	}
	for _, requiredField := range []string{"version", "terraform_version", "serial", "lineage", "outputs", "resources"} {
		if topLevel[requiredField] == nil {
			return state, false, fmt.Errorf("Terraform state %q is missing required field %q", statePath, requiredField)
		}
	}

	decoder := json.NewDecoder(bytes.NewReader(content))
	if err := decoder.Decode(&state); err != nil {
		return state, false, fmt.Errorf("unable to decode Terraform state %q: %w", statePath, err)
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		if err == nil {
			return state, false, fmt.Errorf("Terraform state %q contains multiple JSON values", statePath)
		}
		return state, false, fmt.Errorf("Terraform state %q contains trailing malformed JSON: %w", statePath, err)
	}
	if state.Version != supportedTerraformStateVersion {
		return state, false, fmt.Errorf(
			"Terraform state %q uses unsupported format version %d; Dittocloud supports version %d",
			statePath,
			state.Version,
			supportedTerraformStateVersion,
		)
	}
	if strings.TrimSpace(state.TerraformVersion) == "" || state.Serial < 0 || strings.TrimSpace(state.Lineage) == "" {
		return state, false, fmt.Errorf("Terraform state %q has invalid version, serial, or lineage metadata", statePath)
	}
	if !jsonObject(topLevel["outputs"]) || !jsonArray(topLevel["resources"]) {
		return state, false, fmt.Errorf("Terraform state %q outputs or resources have an invalid shape", statePath)
	}
	return state, true, nil
}

func isAWSStateScopeRegistryResource(resource rawTerraformResource) bool {
	return resource.Module == "" && resource.Mode == "managed" && resource.Type == "terraform_data" && resource.Name == "scope_registry"
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
	return strings.HasPrefix(resource.Name, "scoped_") || resource.Name == "scope_tag_policy"
}

func decodeAWSStateScopeRegistryResource(statePath string, resource rawTerraformResource, registry *awsStateScopeRegistry) error {
	registry.Present = true
	if len(resource.Instances) == 0 {
		return fmt.Errorf("Terraform state %q contains a scope registry with no instances", statePath)
	}

	for _, instance := range resource.Instances {
		if instance.Deposed != "" || instance.Status != "" {
			return fmt.Errorf("Terraform state %q contains a deposed or non-ready scope registry instance", statePath)
		}

		var scopeRef string
		if len(instance.IndexKey) == 0 || json.Unmarshal(instance.IndexKey, &scopeRef) != nil || !awsScopeReferencePattern.MatchString(scopeRef) {
			return fmt.Errorf("Terraform state %q contains an invalid scope registry address key", statePath)
		}
		if _, exists := registry.Scopes[scopeRef]; exists {
			return fmt.Errorf("Terraform state %q contains duplicate scope registry key %q", statePath, scopeRef)
		}

		identity, err := decodeAWSStateScopeIdentity(instance.Attributes)
		if err != nil {
			return fmt.Errorf("Terraform state %q scope registry key %q is invalid: %w", statePath, scopeRef, err)
		}
		if identity.ScopeRef != scopeRef {
			return fmt.Errorf(
				"Terraform state %q scope registry address key %q does not match stored scope_ref %q",
				statePath,
				scopeRef,
				identity.ScopeRef,
			)
		}
		registry.Scopes[scopeRef] = identity
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
		return nil
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
