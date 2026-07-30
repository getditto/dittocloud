package bootstrap

import (
	"context"
	"fmt"
	"reflect"
	"sort"
)

type awsScopeTagPolicyTransitionContextKey struct{}

type awsScopeTagPolicyTransitionConfiguration struct {
	StatePath  string
	AWSProfile string
	Scopes     AWSDeploymentScopes
}

func prepareAWSScopeTagPolicyTransition(
	statePath string,
	awsProfile string,
	desiredScopes AWSDeploymentScopes,
) (awsScopeTagPolicyTransitionConfiguration, []string, error) {
	configuration := awsScopeTagPolicyTransitionConfiguration{
		StatePath:  statePath,
		AWSProfile: awsProfile,
		Scopes:     AWSDeploymentScopes{},
	}
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return configuration, nil, err
	}

	authorizedRefs := make([]string, 0)
	for _, scopeRef := range sortedAWSDeploymentScopeRefs(desiredScopes) {
		desired := desiredScopes[scopeRef]
		appliedVersion, markerExists := registry.AppliedTagPolicyVersions[scopeRef]

		if desired.ScopeTagPolicyVersion == 0 {
			if markerExists && appliedVersion == 1 {
				return configuration, nil, fmt.Errorf(
					"scope %q cannot downgrade scopeTagPolicyVersion from applied version 1 to 0",
					scopeRef,
				)
			}
			continue
		}

		authorizedRefs = append(authorizedRefs, scopeRef)
		if !registry.Present {
			return configuration, nil, fmt.Errorf(
				"scope %q cannot start at scopeTagPolicyVersion 1; apply it at version 0 before verified enablement",
				scopeRef,
			)
		}
		if _, exists := registry.Scopes[scopeRef]; !exists {
			return configuration, nil, fmt.Errorf(
				"new scope %q cannot start at scopeTagPolicyVersion 1; apply it at version 0 before verified enablement",
				scopeRef,
			)
		}
		if !markerExists {
			return configuration, nil, fmt.Errorf(
				"scope %q has no applied tag-policy marker; apply it at version 0 before verified enablement",
				scopeRef,
			)
		}
		switch appliedVersion {
		case 0:
			if _, exists := registry.Configurations[scopeRef]; !exists {
				return configuration, nil, fmt.Errorf(
					"scope %q has no applied configuration snapshot; apply version 0 before verified enablement",
					scopeRef,
				)
			}
			configuration.Scopes[scopeRef] = desired
		case 1:
			appliedConfiguration, exists := registry.Configurations[scopeRef]
			if !exists {
				return configuration, nil, fmt.Errorf(
					"scope %q has applied tag-policy version 1 but no applied configuration snapshot; manual recovery is required",
					scopeRef,
				)
			}
			if desired.ClusterName != appliedConfiguration.ClusterName {
				return configuration, nil, fmt.Errorf(
					"scope %q clusterName is immutable after scopeTagPolicyVersion 1 is applied; state records %q but YAML requests %q",
					scopeRef,
					appliedConfiguration.ClusterName,
					desired.ClusterName,
				)
			}
		default:
			return configuration, nil, fmt.Errorf(
				"scope %q has unsupported applied tag-policy version %d",
				scopeRef,
				appliedVersion,
			)
		}
	}

	sort.Strings(authorizedRefs)
	return configuration, authorizedRefs, nil
}

func detectAWSLegacyVersionZeroClusterPolicyRefs(
	statePath string,
	desiredScopes AWSDeploymentScopes,
) ([]string, error) {
	var defaultScopeRef string
	var defaultScope AWSDeploymentScope
	for _, scopeRef := range sortedAWSDeploymentScopeRefs(desiredScopes) {
		scope := desiredScopes[scopeRef]
		if scope.Default {
			defaultScopeRef = scopeRef
			defaultScope = scope
			break
		}
	}
	if defaultScopeRef == "" || defaultScope.ScopeTagPolicyVersion != 0 || defaultScope.ClusterName == "" {
		return nil, nil
	}

	state, exists, err := loadRawTerraformState(statePath)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, nil
	}
	collector := awsLegacyEvidenceCollector{}
	for _, resource := range state.Resources {
		if !isAWSLegacyPhaseTwoIAMPolicy(resource) {
			continue
		}
		if err := collectAWSLegacyClusterNameEvidence(resource, collector); err != nil {
			return nil, err
		}
	}
	evidence, err := collector.resolve()
	if err != nil {
		return nil, err
	}
	appliedClusterName := evidence[legacyScopeFieldClusterName].Value
	if appliedClusterName == "" {
		return nil, nil
	}
	if appliedClusterName != defaultScope.ClusterName {
		return nil, fmt.Errorf(
			"default scope %q clusterName %q conflicts with the existing legacy phase-two IAM cluster %q",
			defaultScopeRef,
			defaultScope.ClusterName,
			appliedClusterName,
		)
	}
	return []string{defaultScopeRef}, nil
}

func setAWSScopeTagPolicyTransitionConfiguration(
	ctx context.Context,
	configuration awsScopeTagPolicyTransitionConfiguration,
) context.Context {
	return context.WithValue(ctx, awsScopeTagPolicyTransitionContextKey{}, configuration)
}

func commandAWSScopeTagPolicyTransitionConfiguration(
	ctx context.Context,
) (awsScopeTagPolicyTransitionConfiguration, bool) {
	configuration, ok := ctx.Value(awsScopeTagPolicyTransitionContextKey{}).(awsScopeTagPolicyTransitionConfiguration)
	return configuration, ok && len(configuration.Scopes) > 0
}

func verifyAWSScopeTagPolicyTransitions(
	ctx context.Context,
	configuration awsScopeTagPolicyTransitionConfiguration,
) ([]awsScopeTagVerificationReport, error) {
	reports := make([]awsScopeTagVerificationReport, 0, len(configuration.Scopes))
	for _, scopeRef := range sortedAWSDeploymentScopeRefs(configuration.Scopes) {
		report, err := verifyAWSScopeTagPolicyReadiness(
			ctx,
			configuration.StatePath,
			configuration.AWSProfile,
			scopeRef,
			configuration.Scopes[scopeRef],
			0,
			true,
		)
		if err != nil {
			return nil, fmt.Errorf("scope %q is not ready for tag-policy version 1: %w", scopeRef, err)
		}
		reports = append(reports, report)
	}
	return reports, nil
}

func verifyAWSScopeTagPolicyReadiness(
	ctx context.Context,
	statePath string,
	awsProfile string,
	scopeRef string,
	verificationScope AWSDeploymentScope,
	expectedAppliedPolicyVersion int,
	allowPolicyTransition bool,
) (awsScopeTagVerificationReport, error) {
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return awsScopeTagVerificationReport{}, err
	}
	if !registry.Present {
		return awsScopeTagVerificationReport{}, fmt.Errorf("terraform state %q has no AWS scope registry", statePath)
	}
	identity, exists := registry.Scopes[scopeRef]
	if !exists {
		return awsScopeTagVerificationReport{}, fmt.Errorf("terraform state %q does not contain scope %q", statePath, scopeRef)
	}
	if identity.Default != verificationScope.Default {
		return awsScopeTagVerificationReport{}, fmt.Errorf("scope %q default identity does not match Terraform state", scopeRef)
	}
	appliedPolicyVersion, exists := registry.AppliedTagPolicyVersions[scopeRef]
	if !exists {
		return awsScopeTagVerificationReport{}, fmt.Errorf("terraform state %q has no applied tag-policy marker for scope %q", statePath, scopeRef)
	}
	if appliedPolicyVersion != expectedAppliedPolicyVersion {
		return awsScopeTagVerificationReport{}, fmt.Errorf(
			"scope %q expected applied tag-policy version %d but Terraform state records version %d",
			scopeRef,
			expectedAppliedPolicyVersion,
			appliedPolicyVersion,
		)
	}
	if !allowPolicyTransition && verificationScope.ScopeTagPolicyVersion != appliedPolicyVersion {
		return awsScopeTagVerificationReport{}, fmt.Errorf(
			"scope %q requests tag-policy version %d but Terraform state records applied version %d; verification cannot authorize an unapplied policy transition",
			scopeRef,
			verificationScope.ScopeTagPolicyVersion,
			appliedPolicyVersion,
		)
	}

	appliedConfiguration, exists := registry.Configurations[scopeRef]
	if !exists {
		return awsScopeTagVerificationReport{}, fmt.Errorf("terraform state %q has no applied configuration snapshot for scope %q", statePath, scopeRef)
	}
	reviewedConfiguration := verificationScope
	reviewedConfiguration.ClusterName = ""
	appliedConfiguration.ClusterName = ""
	if allowPolicyTransition {
		reviewedConfiguration.ScopeTagPolicyVersion = expectedAppliedPolicyVersion
		appliedConfiguration.ScopeTagPolicyVersion = expectedAppliedPolicyVersion
	}
	if !reflect.DeepEqual(reviewedConfiguration, appliedConfiguration) {
		ignoredFields := "clusterName"
		if allowPolicyTransition {
			ignoredFields += " and scopeTagPolicyVersion"
		}
		return awsScopeTagVerificationReport{}, fmt.Errorf(
			"scope %q differs from its applied configuration in fields other than %s; apply or revert those changes before live tag verification",
			scopeRef,
			ignoredFields,
		)
	}

	stateResources, exclusions, err := loadAWSStateScopeTagInventory(statePath, scopeRef, verificationScope)
	if err != nil {
		return awsScopeTagVerificationReport{}, err
	}
	accountID, err := awsScopeTagInventoryAccountID(stateResources)
	if err != nil {
		return awsScopeTagVerificationReport{}, err
	}
	verifier, err := newAWSScopeTagVerifier(ctx, awsProfile, verificationScope.Region)
	if err != nil {
		return awsScopeTagVerificationReport{}, fmt.Errorf("unable to initialize read-only AWS tag verification: %w", err)
	}
	report, err := verifier.Verify(ctx, awsScopeTagVerificationRequest{
		ScopeRef:   scopeRef,
		Scope:      verificationScope,
		AccountID:  accountID,
		StatePath:  statePath,
		AWSProfile: awsProfile,
		State:      stateResources,
		Exclusions: exclusions,
	})
	if err != nil {
		return awsScopeTagVerificationReport{}, err
	}
	report.ScopeRef = scopeRef
	report.AccountID = accountID
	report.ClusterName = verificationScope.ClusterName
	report.ClusterType = verificationScope.ClusterType
	report.Region = verificationScope.Region
	report.AppliedPolicyVersion = appliedPolicyVersion
	return report, nil
}
