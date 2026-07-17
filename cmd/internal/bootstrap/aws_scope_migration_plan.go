package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"

	tfjson "github.com/hashicorp/terraform-json"
)

type awsInitialScopeMigrationPlanContextKey struct{}

type awsInitialScopeMigrationPlanConfiguration struct {
	ScopeRef                     string
	NATGatewayName               string
	Scope                        AWSDeploymentScope
	ConfigurationSnapshotPresent bool
}

func prepareAWSInitialScopeMigrationPlanConfiguration(
	statePath string,
	scopes AWSDeploymentScopes,
) (awsInitialScopeMigrationPlanConfiguration, bool, error) {
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return awsInitialScopeMigrationPlanConfiguration{}, false, err
	}
	if !registry.Present || len(registry.AppliedTagPolicyVersions) > 0 {
		return awsInitialScopeMigrationPlanConfiguration{}, false, nil
	}
	if len(registry.Scopes) != 1 {
		return awsInitialScopeMigrationPlanConfiguration{}, false, nil
	}
	if len(scopes) != 1 {
		return awsInitialScopeMigrationPlanConfiguration{}, false, fmt.Errorf(
			"complete the guarded initial AWS default-scope migration before adding another scope",
		)
	}
	scopeRef := registry.DefaultScopeRef
	scope, exists := scopes[scopeRef]
	if !exists || !scope.Default {
		return awsInitialScopeMigrationPlanConfiguration{}, false, fmt.Errorf(
			"initial AWS scope migration must retain registry-backed default scope %q",
			scopeRef,
		)
	}
	_, configurationSnapshotPresent := registry.Configurations[scopeRef]
	if configurationSnapshotPresent && !reflect.DeepEqual(registry.Configurations[scopeRef], scope) {
		return awsInitialScopeMigrationPlanConfiguration{}, false, fmt.Errorf(
			"initial AWS scope migration state contains a configuration snapshot that does not match reviewed default scope %q",
			scopeRef,
		)
	}
	return awsInitialScopeMigrationPlanConfiguration{
		ScopeRef:                     scopeRef,
		NATGatewayName:               scope.VPC.NATGatewayName,
		Scope:                        scope,
		ConfigurationSnapshotPresent: configurationSnapshotPresent,
	}, true, nil
}

func setAWSInitialScopeMigrationPlanConfiguration(
	ctx context.Context,
	configuration awsInitialScopeMigrationPlanConfiguration,
) context.Context {
	return context.WithValue(ctx, awsInitialScopeMigrationPlanContextKey{}, configuration)
}

func commandAWSInitialScopeMigrationPlanConfiguration(
	ctx context.Context,
) (awsInitialScopeMigrationPlanConfiguration, bool) {
	configuration, exists := ctx.Value(awsInitialScopeMigrationPlanContextKey{}).(awsInitialScopeMigrationPlanConfiguration)
	return configuration, exists
}

func validateAWSInitialScopeMigrationPlan(
	plan *tfjson.Plan,
	configuration awsInitialScopeMigrationPlanConfiguration,
) error {
	if plan == nil {
		return fmt.Errorf("initial AWS scope migration plan is unavailable")
	}
	if plan.Complete != nil && !*plan.Complete {
		return fmt.Errorf("initial AWS scope migration plan is incomplete")
	}
	if len(plan.DeferredChanges) > 0 {
		return fmt.Errorf("initial AWS scope migration plan contains deferred resource changes")
	}
	plannedChangesByAddress := make(map[string]*tfjson.ResourceChange, len(plan.ResourceChanges))
	for _, change := range plan.ResourceChanges {
		if change != nil {
			plannedChangesByAddress[change.Address] = change
		}
	}
	for _, drift := range plan.ResourceDrift {
		if drift == nil || drift.Change == nil || drift.Change.Actions.NoOp() {
			continue
		}
		if err := validateAWSInitialScopeMigrationDrift(
			drift,
			plannedChangesByAddress[drift.Address],
			configuration,
		); err != nil {
			return err
		}
	}
	if err := validateAWSNATGatewayNamePlanEvidence(plan.ResourceChanges, configuration); err != nil {
		return err
	}

	tagPolicyMarkerAddress := fmt.Sprintf("terraform_data.scope_tag_policy[%q]", configuration.ScopeRef)
	configurationMarkerAddress := fmt.Sprintf("terraform_data.scope_configuration[%q]", configuration.ScopeRef)
	tagPolicyMarkerCreates := 0
	configurationMarkerCreates := 0
	for _, change := range plan.ResourceChanges {
		if change == nil || change.Change == nil {
			return fmt.Errorf("initial AWS scope migration plan contains a malformed resource change")
		}
		if change.Change.Actions.NoOp() || (change.Mode == tfjson.DataResourceMode && change.Change.Actions.Read()) {
			continue
		}
		if change.Address == tagPolicyMarkerAddress {
			if err := validateAWSInitialScopeMigrationMarker(change, configuration); err != nil {
				return err
			}
			tagPolicyMarkerCreates++
			continue
		}
		if change.Address == configurationMarkerAddress {
			if err := validateAWSInitialScopeMigrationConfigurationMarker(change, configuration); err != nil {
				return err
			}
			configurationMarkerCreates++
			continue
		}
		if change.Mode != tfjson.ManagedResourceMode || !change.Change.Actions.Update() {
			return fmt.Errorf(
				"initial AWS scope migration plan contains unsupported action at %q: %s",
				change.Address,
				strings.Join(terraformChangeActions(change.Change.Actions), ","),
			)
		}
		if err := validateAWSInitialScopeMigrationTagUpdate(change, configuration.ScopeRef); err != nil {
			return err
		}
	}
	if tagPolicyMarkerCreates != 1 {
		return fmt.Errorf("initial AWS scope migration plan must create exactly one applied tag-policy marker; found %d", tagPolicyMarkerCreates)
	}
	expectedConfigurationMarkerCreates := 1
	if configuration.ConfigurationSnapshotPresent {
		expectedConfigurationMarkerCreates = 0
	}
	if configurationMarkerCreates != expectedConfigurationMarkerCreates {
		if expectedConfigurationMarkerCreates == 1 {
			return fmt.Errorf("initial AWS scope migration plan must create exactly one applied configuration snapshot; found %d", configurationMarkerCreates)
		}
		return fmt.Errorf("initial AWS scope migration plan must not recreate the existing applied configuration snapshot; found %d creates", configurationMarkerCreates)
	}
	return validateAWSInitialScopeMigrationOutputs(plan.OutputChanges, configuration.ScopeRef)
}

func validateAWSInitialScopeMigrationMarker(
	change *tfjson.ResourceChange,
	configuration awsInitialScopeMigrationPlanConfiguration,
) error {
	index, indexIsString := change.Index.(string)
	if change.Mode != tfjson.ManagedResourceMode ||
		change.Type != "terraform_data" ||
		change.Name != "scope_tag_policy" ||
		change.ProviderName != terraformBuiltinProviderName ||
		!change.Change.Actions.Create() ||
		!indexIsString || index != configuration.ScopeRef ||
		change.PreviousAddress != "" || change.DeposedKey != "" || change.Change.Importing != nil {
		return fmt.Errorf("initial AWS scope migration marker at %q is not the exact built-in create", change.Address)
	}
	after, ok := terraformObject(change.Change.After)
	if !ok {
		return fmt.Errorf("initial AWS scope migration marker at %q has malformed planned values", change.Address)
	}
	input, ok := terraformObject(after["input"])
	if !ok ||
		terraformNumber(input["schema_version"]) != 1 ||
		input["scope_ref"] != configuration.ScopeRef ||
		terraformNumber(input["policy_version"]) != 0 {
		return fmt.Errorf("initial AWS scope migration marker at %q must record schema 1, scope %q, and policy version 0", change.Address, configuration.ScopeRef)
	}
	return nil
}

func validateAWSInitialScopeMigrationConfigurationMarker(
	change *tfjson.ResourceChange,
	configuration awsInitialScopeMigrationPlanConfiguration,
) error {
	index, indexIsString := change.Index.(string)
	if change.Mode != tfjson.ManagedResourceMode ||
		change.Type != "terraform_data" ||
		change.Name != "scope_configuration" ||
		change.ProviderName != terraformBuiltinProviderName ||
		!change.Change.Actions.Create() ||
		!indexIsString || index != configuration.ScopeRef ||
		change.PreviousAddress != "" || change.DeposedKey != "" || change.Change.Importing != nil {
		return fmt.Errorf("initial AWS scope migration configuration snapshot at %q is not the exact built-in create", change.Address)
	}
	after, ok := terraformObject(change.Change.After)
	if !ok {
		return fmt.Errorf("initial AWS scope migration configuration snapshot at %q has malformed planned values", change.Address)
	}
	input, ok := terraformObject(after["input"])
	if !ok || len(input) != 3 ||
		terraformNumber(input["schema_version"]) != awsScopeConfigurationSchemaVersion ||
		input["scope_ref"] != configuration.ScopeRef {
		return fmt.Errorf("initial AWS scope migration configuration snapshot at %q must record schema %d and scope %q", change.Address, awsScopeConfigurationSchemaVersion, configuration.ScopeRef)
	}
	plannedScope, ok := terraformPlannedAWSDeploymentScope(input["configuration"])
	if !ok || !reflect.DeepEqual(plannedScope, configuration.Scope) {
		return fmt.Errorf("initial AWS scope migration configuration snapshot at %q does not exactly match the reviewed scope configuration", change.Address)
	}
	return nil
}

func terraformPlannedAWSDeploymentScope(value any) (AWSDeploymentScope, bool) {
	configuration, ok := terraformObject(value)
	if !ok || len(configuration) != 6 {
		return AWSDeploymentScope{}, false
	}
	defaultScope, ok := configuration["default"].(bool)
	if !ok {
		return AWSDeploymentScope{}, false
	}
	clusterName, ok := terraformOptionalString(configuration["cluster_name"])
	if !ok {
		return AWSDeploymentScope{}, false
	}
	clusterType, ok := configuration["cluster_type"].(string)
	if !ok {
		return AWSDeploymentScope{}, false
	}
	region, ok := configuration["region"].(string)
	if !ok {
		return AWSDeploymentScope{}, false
	}
	policyVersion := terraformNumber(configuration["scope_tag_policy_version"])
	if policyVersion < 0 {
		return AWSDeploymentScope{}, false
	}
	vpcValues, ok := terraformObject(configuration["vpc"])
	if !ok || len(vpcValues) != 5 {
		return AWSDeploymentScope{}, false
	}
	vpcMode, ok := vpcValues["mode"].(string)
	if !ok {
		return AWSDeploymentScope{}, false
	}
	vpcName, nameOK := terraformOptionalString(vpcValues["name"])
	vpcCIDR, cidrOK := terraformOptionalString(vpcValues["cidr"])
	vpcID, idOK := terraformOptionalString(vpcValues["id"])
	natGatewayName, natOK := terraformOptionalString(vpcValues["nat_gateway_name"])
	if !nameOK || !cidrOK || !idOK || !natOK {
		return AWSDeploymentScope{}, false
	}
	return AWSDeploymentScope{
		Default:               defaultScope,
		ClusterName:           clusterName,
		ClusterType:           clusterType,
		Region:                region,
		ScopeTagPolicyVersion: policyVersion,
		VPC: AWSScopeVPC{
			Mode:           vpcMode,
			Name:           vpcName,
			CIDR:           vpcCIDR,
			ID:             vpcID,
			NATGatewayName: natGatewayName,
		},
	}, true
}

func terraformOptionalString(value any) (string, bool) {
	if value == nil {
		return "", true
	}
	decoded, ok := value.(string)
	return decoded, ok
}

func validateAWSInitialScopeMigrationTagUpdate(change *tfjson.ResourceChange, scopeRef string) error {
	before, beforeOK := terraformObject(change.Change.Before)
	after, afterOK := terraformObject(change.Change.After)
	if !beforeOK || !afterOK {
		return fmt.Errorf("initial AWS scope migration update at %q has malformed values", change.Address)
	}
	beforeWithoutTags := cloneTerraformObjectWithout(before, "tags", "tags_all")
	afterWithoutTags := cloneTerraformObjectWithout(after, "tags", "tags_all")
	if !reflect.DeepEqual(beforeWithoutTags, afterWithoutTags) {
		return fmt.Errorf("initial AWS scope migration update at %q changes an attribute other than tags", change.Address)
	}
	for _, field := range []string{"tags", "tags_all"} {
		beforeTags, beforePresent := terraformStringMap(before[field])
		afterTags, afterPresent := terraformStringMap(after[field])
		if !beforePresent && !afterPresent {
			continue
		}
		if !afterPresent {
			return fmt.Errorf("initial AWS scope migration update at %q removes %s", change.Address, field)
		}
		for key, value := range beforeTags {
			if afterTags[key] != value {
				return fmt.Errorf("initial AWS scope migration update at %q removes or changes tag %q", change.Address, key)
			}
		}
		for key, value := range afterTags {
			if _, existed := beforeTags[key]; existed {
				continue
			}
			switch key {
			case "ditto.live/scope-ref":
				if value != scopeRef {
					return fmt.Errorf("initial AWS scope migration update at %q assigns the wrong scope identity", change.Address)
				}
			case "ditto.live/managed_by":
				if value != "dittocloud" {
					return fmt.Errorf("initial AWS scope migration update at %q assigns an invalid managed-by tag", change.Address)
				}
			default:
				return fmt.Errorf("initial AWS scope migration update at %q adds unexpected tag %q", change.Address, key)
			}
		}
		if afterTags["ditto.live/scope-ref"] != scopeRef {
			return fmt.Errorf("initial AWS scope migration update at %q does not establish the exact scope identity tag", change.Address)
		}
	}
	return nil
}

func validateAWSInitialScopeMigrationDrift(
	change *tfjson.ResourceChange,
	plannedChange *tfjson.ResourceChange,
	configuration awsInitialScopeMigrationPlanConfiguration,
) error {
	if !change.Change.Actions.Update() {
		return fmt.Errorf("initial AWS scope migration detected unsupported live drift at %q", change.Address)
	}
	if validateAWSInitialScopeMigrationRefreshOnlyDrift(change, plannedChange, configuration.ScopeRef) {
		return nil
	}
	isNAT := strings.HasPrefix(change.Address, "module.vpc[0].module.vpc.aws_nat_gateway.this[")
	isVPCResource := isNAT ||
		strings.HasPrefix(change.Address, "module.vpc[0].module.vpc.aws_subnet.private[") ||
		strings.HasPrefix(change.Address, "module.vpc[0].module.vpc.aws_subnet.public[")
	before, beforeOK := terraformObject(change.Change.Before)
	after, afterOK := terraformObject(change.Change.After)
	if !beforeOK || !afterOK {
		return fmt.Errorf("initial AWS scope migration detected non-tag live drift at %q", change.Address)
	}
	beforeWithoutTags := cloneTerraformObjectWithout(before, "tags", "tags_all")
	afterWithoutTags := cloneTerraformObjectWithout(after, "tags", "tags_all")
	if !reflect.DeepEqual(beforeWithoutTags, afterWithoutTags) {
		return fmt.Errorf("initial AWS scope migration detected non-tag live drift at %q", change.Address)
	}
	if !isVPCResource {
		for _, field := range []string{"tags", "tags_all"} {
			beforeTags, beforeOK := terraformOptionalStringMap(before[field])
			afterTags, afterOK := terraformOptionalStringMap(after[field])
			if !beforeOK || !afterOK || !reflect.DeepEqual(beforeTags, afterTags) {
				return fmt.Errorf("initial AWS scope migration detected unreviewed live drift at %q", change.Address)
			}
		}
		return nil
	}
	for _, field := range []string{"tags", "tags_all"} {
		beforeTags, _ := terraformStringMap(before[field])
		afterTags, ok := terraformStringMap(after[field])
		if !ok {
			return fmt.Errorf("initial AWS scope migration detected malformed live %s at %q", field, change.Address)
		}
		for key, value := range beforeTags {
			if awsExternallyManagedCAPITag(key) {
				continue
			}
			if key == "Name" && isNAT && configuration.NATGatewayName != "" && afterTags[key] == configuration.NATGatewayName {
				continue
			}
			if afterTags[key] != value {
				return fmt.Errorf("initial AWS scope migration live drift at %q removes or changes tag %q", change.Address, key)
			}
		}
		for key := range afterTags {
			if _, existed := beforeTags[key]; existed || awsExternallyManagedCAPITag(key) {
				continue
			}
			return fmt.Errorf("initial AWS scope migration detected unexpected live tag %q at %q", key, change.Address)
		}
	}
	return nil
}

func validateAWSInitialScopeMigrationRefreshOnlyDrift(
	drift *tfjson.ResourceChange,
	plannedChange *tfjson.ResourceChange,
	scopeRef string,
) bool {
	if drift.Mode != tfjson.ManagedResourceMode ||
		plannedChange == nil || plannedChange.Change == nil ||
		plannedChange.Mode != drift.Mode || plannedChange.Type != drift.Type ||
		plannedChange.ProviderName != drift.ProviderName || plannedChange.Address != drift.Address ||
		plannedChange.PreviousAddress != "" || plannedChange.DeposedKey != "" ||
		plannedChange.Change.Importing != nil ||
		!reflect.DeepEqual(drift.Change.After, plannedChange.Change.Before) {
		return false
	}
	if plannedChange.Change.Actions.NoOp() {
		return reflect.DeepEqual(plannedChange.Change.Before, plannedChange.Change.After)
	}
	return plannedChange.Change.Actions.Update() &&
		validateAWSInitialScopeMigrationTagUpdate(plannedChange, scopeRef) == nil
}

func validateAWSNATGatewayNamePlanEvidence(
	changes []*tfjson.ResourceChange,
	configuration awsInitialScopeMigrationPlanConfiguration,
) error {
	if configuration.NATGatewayName == "" {
		return nil
	}
	verified := 0
	for _, change := range changes {
		if change == nil || change.Change == nil {
			continue
		}
		isNAT := strings.HasPrefix(change.Address, "module.vpc[0].module.vpc.aws_nat_gateway.this[")
		if !isNAT {
			continue
		}
		before, ok := terraformObject(change.Change.Before)
		if !ok {
			continue
		}
		tags, ok := terraformStringMap(before["tags"])
		if !ok {
			continue
		}
		if tags["Name"] != configuration.NATGatewayName {
			return fmt.Errorf(
				"initial AWS scope migration expected NAT gateway Name %q but refreshed %q has %q",
				configuration.NATGatewayName,
				change.Address,
				tags["Name"],
			)
		}
		verified++
	}
	if verified == 0 {
		return fmt.Errorf("initial AWS scope migration could not verify NAT gateway Name %q", configuration.NATGatewayName)
	}
	return nil
}

func awsExternallyManagedCAPITag(key string) bool {
	return strings.HasPrefix(key, "kubernetes.io/cluster/") ||
		strings.HasPrefix(key, "sigs.k8s.io/cluster-api-provider-aws/cluster/") ||
		key == "sigs.k8s.io/cluster-api-provider-aws/role"
}

func validateAWSInitialScopeMigrationOutputs(changes map[string]*tfjson.Change, scopeRef string) error {
	awsOutputUpdates := 0
	for name, change := range changes {
		if change == nil || change.Actions.NoOp() {
			continue
		}
		if name != "aws" || !change.Actions.Update() {
			return fmt.Errorf("initial AWS scope migration contains unsupported output change %q", name)
		}
		before, beforeOK := terraformObject(change.Before)
		after, afterOK := terraformObject(change.After)
		if !beforeOK || !afterOK {
			return fmt.Errorf("initial AWS scope migration aws output change is malformed")
		}
		awsOutputUpdates++
		for key, value := range before {
			if !reflect.DeepEqual(after[key], value) {
				return fmt.Errorf("initial AWS scope migration changes legacy aws output field %q", key)
			}
		}
		for key := range after {
			if _, existed := before[key]; existed || key == "scopes" || key == "regionalResources" {
				continue
			}
			return fmt.Errorf("initial AWS scope migration adds unexpected aws output field %q", key)
		}
		scopes, ok := terraformObject(after["scopes"])
		if !ok || len(scopes) != 1 {
			return fmt.Errorf("initial AWS scope migration aws output does not add the scope map")
		}
		if _, ok := scopes[scopeRef]; !ok {
			return fmt.Errorf("initial AWS scope migration aws output omits default scope %q", scopeRef)
		}
		if _, ok := after["regionalResources"]; !ok {
			return fmt.Errorf("initial AWS scope migration aws output does not add regionalResources")
		}
	}
	if awsOutputUpdates != 1 {
		return fmt.Errorf("initial AWS scope migration must update exactly one aws output; found %d", awsOutputUpdates)
	}
	return nil
}

func terraformObject(value any) (map[string]any, bool) {
	object, ok := value.(map[string]any)
	return object, ok
}

func terraformStringMap(value any) (map[string]string, bool) {
	object, ok := terraformObject(value)
	if !ok {
		return nil, false
	}
	result := make(map[string]string, len(object))
	for key, raw := range object {
		value, ok := raw.(string)
		if !ok {
			return nil, false
		}
		result[key] = value
	}
	return result, true
}

func terraformOptionalStringMap(value any) (map[string]string, bool) {
	if value == nil {
		return map[string]string{}, true
	}
	return terraformStringMap(value)
}

func cloneTerraformObjectWithout(value map[string]any, keys ...string) map[string]any {
	result := make(map[string]any, len(value))
	for key, item := range value {
		result[key] = item
	}
	for _, key := range keys {
		delete(result, key)
	}
	return result
}

func terraformNumber(value any) int {
	switch value := value.(type) {
	case float64:
		return int(value)
	case int:
		return value
	case int64:
		return int(value)
	case json.Number:
		parsed, err := value.Int64()
		if err == nil {
			return int(parsed)
		}
	default:
	}
	return -1
}

func terraformChangeActions(actions tfjson.Actions) []string {
	result := make([]string, 0, len(actions))
	for _, action := range actions {
		result = append(result, string(action))
	}
	return result
}
