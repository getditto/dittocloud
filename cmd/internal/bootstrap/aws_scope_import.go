package bootstrap

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"slices"
	"strings"
)

var (
	awsScopeReferenceInAddressPattern = regexp.MustCompile(`dsc-[0-7][0-9a-hjkmnp-tv-z]{25}`)
	awsResourceTypeInAddressPattern   = regexp.MustCompile(`(?:^|\.)(aws_[a-z0-9_]+|terraform_data)\.[A-Za-z0-9_]+(?:\[|\.|$)`)
	awsScopedIMDSImportAddressPattern = regexp.MustCompile(`^aws_ec2_instance_metadata_defaults\.scoped_imdsv2\["([^"]+)"\]$`)
	awsImportRegionSuffixPattern      = regexp.MustCompile(`@([a-z]{2}(?:-[a-z0-9]+)+-[0-9]+)$`)
)

type awsScopedImportConfiguration struct {
	Imports        []resourceImport
	Scopes         AWSDeploymentScopes
	EncodedScopes  string
	OriginalState  []byte
	TerraformState rawTerraformState
}

type awsScopedImportConfigurationContextKey struct{}

func prepareAWSScopedImportConfiguration(
	statePath string,
	scopes AWSDeploymentScopes,
	encodedScopes string,
	imports []resourceImport,
) (awsScopedImportConfiguration, error) {
	if len(imports) == 0 {
		return awsScopedImportConfiguration{}, fmt.Errorf("scope-mode import configuration requires at least one import")
	}
	if err := validateAWSScopedImportRegistry(statePath, scopes); err != nil {
		return awsScopedImportConfiguration{}, err
	}

	terraformState, exists, err := loadRawTerraformState(statePath)
	if err != nil {
		return awsScopedImportConfiguration{}, err
	}
	if !exists {
		return awsScopedImportConfiguration{}, fmt.Errorf("scope-mode imports require an existing registry-backed Terraform state at %q", statePath)
	}
	originalState, err := os.ReadFile(statePath)
	if err != nil {
		return awsScopedImportConfiguration{}, fmt.Errorf("unable to read Terraform state before scope-mode import: %w", err)
	}
	preparedImports, err := prepareAWSScopedResourceImports(scopes, imports)
	if err != nil {
		return awsScopedImportConfiguration{}, err
	}

	return awsScopedImportConfiguration{
		Imports:        preparedImports,
		Scopes:         scopes,
		EncodedScopes:  encodedScopes,
		OriginalState:  originalState,
		TerraformState: terraformState,
	}, nil
}

func validateAWSScopedImportRegistry(statePath string, scopes AWSDeploymentScopes) error {
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return err
	}
	if !registry.Present {
		return fmt.Errorf(
			"scope-mode imports require a seeded scope registry in Terraform state %q; run the registry migration before importing resources",
			statePath,
		)
	}
	if len(registry.Scopes) != len(scopes) {
		return fmt.Errorf(
			"scope-mode imports require the state registry to exactly match the scopes YAML; state has %d scope(s) and YAML has %d",
			len(registry.Scopes),
			len(scopes),
		)
	}
	for scopeRef, scope := range scopes {
		identity, exists := registry.Scopes[scopeRef]
		if !exists || identity.Default != scope.Default {
			return fmt.Errorf(
				"scope-mode imports require the state registry to exactly match the scopes YAML; scope %q is missing or has a different default identity",
				scopeRef,
			)
		}
	}
	return nil
}

func prepareAWSScopedResourceImports(scopes AWSDeploymentScopes, imports []resourceImport) ([]resourceImport, error) {
	prepared := slices.Clone(imports)
	for index := range prepared {
		region, regionQualified, err := resolveAWSScopedImportRegion(scopes, prepared[index].address)
		if err != nil {
			return nil, err
		}
		if !regionQualified {
			continue
		}
		prepared[index].id, err = qualifyAWSImportIDRegion(prepared[index].address, prepared[index].id, region)
		if err != nil {
			return nil, err
		}
	}
	return prepared, nil
}

func resolveAWSScopedImportRegion(scopes AWSDeploymentScopes, address string) (string, bool, error) {
	if strings.HasPrefix(address, "data.") || strings.Contains(address, ".data.") {
		return "", false, fmt.Errorf("scope-mode import address %q targets a data source; only managed resources can be imported", address)
	}
	resourceType, err := awsResourceTypeFromAddress(address)
	if err != nil {
		return "", false, err
	}
	if resourceType == "terraform_data" {
		return "", false, fmt.Errorf("scope-mode import address %q targets providerless Terraform state; registry and validation sentinels cannot be imported", address)
	}

	configuredRefs := map[string]struct{}{}
	for _, scopeRef := range awsScopeReferenceInAddressPattern.FindAllString(address, -1) {
		if _, exists := scopes[scopeRef]; !exists {
			return "", false, fmt.Errorf("scope-mode import address %q references scope %q, which is not present in the scopes YAML", address, scopeRef)
		}
		configuredRefs[scopeRef] = struct{}{}
	}
	if len(configuredRefs) > 1 {
		return "", false, fmt.Errorf("scope-mode import address %q references more than one deployment scope", address)
	}

	region := ""
	if len(configuredRefs) == 1 {
		for scopeRef := range configuredRefs {
			scope := scopes[scopeRef]
			if scope.Default {
				return "", false, fmt.Errorf("default scope resource %q must use its legacy Terraform address without a scope reference", address)
			}
			region = scope.Region
		}
	} else if match := awsScopedIMDSImportAddressPattern.FindStringSubmatch(address); match != nil {
		region = match[1]
		if !awsRegionPattern.MatchString(region) || !awsScopesRequireRegionalIMDS(scopes, region) {
			return "", false, fmt.Errorf("scope-mode import address %q does not identify an IMDS Region required by a non-default EKS scope", address)
		}
	} else {
		if strings.Contains(address, "scoped_") || strings.Contains(address, "module.scoped_") {
			return "", false, fmt.Errorf("scope-mode import address %q does not contain its owning scope reference", address)
		}
		for _, scope := range scopes {
			if scope.Default {
				region = scope.Region
				break
			}
		}
	}
	if region == "" {
		return "", false, fmt.Errorf("scope-mode import address %q has no resolvable owning Region", address)
	}

	if strings.HasPrefix(resourceType, "aws_iam_") {
		return region, false, nil
	}
	if !strings.HasPrefix(resourceType, "aws_") {
		return "", false, fmt.Errorf("scope-mode import address %q uses unsupported resource type %q", address, resourceType)
	}
	return region, true, nil
}

func awsResourceTypeFromAddress(address string) (string, error) {
	matches := awsResourceTypeInAddressPattern.FindAllStringSubmatch(address, -1)
	if len(matches) == 0 {
		return "", fmt.Errorf("scope-mode import address %q does not contain a supported Terraform resource type", address)
	}
	return matches[len(matches)-1][1], nil
}

func awsScopesRequireRegionalIMDS(scopes AWSDeploymentScopes, region string) bool {
	defaultRegion := ""
	for _, scope := range scopes {
		if scope.Default {
			defaultRegion = scope.Region
			break
		}
	}
	if region == defaultRegion {
		return false
	}
	for _, scope := range scopes {
		if scope.ClusterType == awsClusterTypeEKS && scope.Region == region {
			return true
		}
	}
	return false
}

func qualifyAWSImportIDRegion(address, id, region string) (string, error) {
	if match := awsImportRegionSuffixPattern.FindStringSubmatch(id); match != nil {
		if match[1] != region {
			return "", fmt.Errorf(
				"scope-mode import address %q belongs to Region %q but import ID %q is qualified for Region %q",
				address,
				region,
				id,
				match[1],
			)
		}
		return id, nil
	}
	return id + "@" + region, nil
}

func setAWSScopedImportConfiguration(cmdContext context.Context, configuration awsScopedImportConfiguration) context.Context {
	return context.WithValue(cmdContext, awsScopedImportConfigurationContextKey{}, configuration)
}

func commandAWSScopedImportConfiguration(cmdContext context.Context) (awsScopedImportConfiguration, bool) {
	configuration, ok := cmdContext.Value(awsScopedImportConfigurationContextKey{}).(awsScopedImportConfiguration)
	return configuration, ok
}
