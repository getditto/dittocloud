package bootstrap

import (
	"fmt"
	"sort"
	"strings"

	"github.com/spf13/pflag"
)

// Every subnet CIDR is derived from the tier netmasks, so applying a netmask a
// deployment was not built with replaces its subnets and takes the NAT gateways,
// nodes, and load balancers with them. A deployment created before the DMZ
// address layout used a /22 public and /18 private tier, and today's defaults
// are /24 and /23, so bumping the module version without pinning the old values
// plans exactly that. These preflights read what already exists and refuse the
// run before Terraform is invoked.

type awsSubnetTierChange struct {
	tier      string
	flagName  string
	fieldName string
	applied   int
	requested int
}

// validateAWSLegacySubnetRenumbering compares the netmasks this run would apply
// against the subnets already recorded in single-scope state.
func validateAWSLegacySubnetRenumbering(statePath string, flags *pflag.FlagSet) error {
	if allowed, err := flags.GetBool(awsAllowSubnetRenumberingFlag); err != nil {
		return fmt.Errorf("unable to get %s: %w", awsAllowSubnetRenumberingFlag, err)
	} else if allowed {
		return nil
	}
	createVPC, err := flags.GetBool("create-vpc")
	if err != nil {
		return fmt.Errorf("unable to get create-vpc: %w", err)
	}
	customerManagedVPC, err := flags.GetBool("customer-managed-vpc")
	if err != nil {
		return fmt.Errorf("unable to get customer-managed-vpc: %w", err)
	}
	if !createVPC || customerManagedVPC {
		return nil
	}

	state, present, err := loadRawTerraformState(statePath)
	if err != nil || !present {
		return err
	}
	applied := awsLegacyStateSubnetNetmasks(state)
	if len(applied) == 0 {
		return nil
	}

	requested := map[string]int{
		"public":  awsDefaultPublicSubnetNetmask,
		"private": awsDefaultPrivateSubnetNetmask,
	}
	flagNames := map[string]string{
		"public":  "aws-vpc-public-subnet-netmask",
		"private": "aws-vpc-private-subnet-netmask",
	}
	for tier, flagName := range flagNames {
		if !flags.Changed(flagName) {
			continue
		}
		value, err := flags.GetInt(flagName)
		if err != nil {
			return fmt.Errorf("unable to get %s: %w", flagName, err)
		}
		requested[tier] = value
	}

	changes := []awsSubnetTierChange{}
	for _, tier := range []string{"public", "private"} {
		appliedNetmask, exists := applied[tier]
		if !exists || appliedNetmask == requested[tier] {
			continue
		}
		changes = append(changes, awsSubnetTierChange{
			tier:      tier,
			flagName:  flagNames[tier],
			applied:   appliedNetmask,
			requested: requested[tier],
		})
	}
	if len(changes) == 0 {
		return nil
	}
	return fmt.Errorf(
		"this run would renumber the %s subnets of the VPC in Terraform state %q:\n%s\n\nRenumbering replaces those subnets and recreates the NAT gateways, nodes, and load balancers attached to them. Pin the sizing you already have:\n%s\n\nTo renumber deliberately, rerun with --%s. No Terraform operation was run",
		awsSubnetChangeTierList(changes),
		statePath,
		awsSubnetChangeSummary(changes),
		awsSubnetChangeFlagHint(changes),
		awsAllowSubnetRenumberingFlag,
	)
}

// validateAWSScopeSubnetRenumbering compares each managed VPC's requested sizing
// against the configuration snapshot last applied for that scope.
func validateAWSScopeSubnetRenumbering(statePath string, scopes AWSDeploymentScopes, allowed bool) error {
	if allowed {
		return nil
	}
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return err
	}
	if !registry.Present {
		return nil
	}

	scopeRefs := sortedAWSDeploymentScopeRefs(scopes)
	for _, scopeRef := range scopeRefs {
		scope := scopes[scopeRef]
		if scope.VPC.Mode != awsVPCModeDittocloud {
			continue
		}
		applied, exists := registry.Configurations[scopeRef]
		if !exists || applied.VPC.Mode != awsVPCModeDittocloud {
			continue
		}
		changes := []awsSubnetTierChange{}
		tiers := []struct {
			tier      string
			fieldName string
			applied   int
			requested int
		}{
			{tier: "public", fieldName: "publicSubnetNetmask", applied: applied.VPC.PublicSubnetNetmask, requested: scope.VPC.PublicSubnetNetmask},
			{tier: "private", fieldName: "privateSubnetNetmask", applied: applied.VPC.PrivateSubnetNetmask, requested: scope.VPC.PrivateSubnetNetmask},
		}
		for _, tier := range tiers {
			if tier.applied == 0 || tier.applied == tier.requested {
				continue
			}
			changes = append(changes, awsSubnetTierChange{
				tier:      tier.tier,
				fieldName: tier.fieldName,
				applied:   tier.applied,
				requested: tier.requested,
			})
		}
		if len(changes) == 0 {
			continue
		}
		return fmt.Errorf(
			"scope %q would renumber its %s subnets:\n%s\n\nRenumbering replaces those subnets and recreates the NAT gateways, nodes, and load balancers attached to them. Restore the sizing recorded in the applied configuration:\n%s\n\nTo renumber deliberately, rerun with --%s. No Terraform operation was run",
			scopeRef,
			awsSubnetChangeTierList(changes),
			awsSubnetChangeSummary(changes),
			awsSubnetChangeFieldHint(changes),
			awsAllowSubnetRenumberingFlag,
		)
	}
	return nil
}

// awsLegacyStateSubnetNetmasks reports the netmask each tier's subnets already
// use, keyed by tier, and reports nothing when the tiers disagree with
// themselves rather than guessing which subnet is authoritative.
func awsLegacyStateSubnetNetmasks(state rawTerraformState) map[string]int {
	collector := awsLegacyEvidenceCollector{}
	for _, resource := range state.Resources {
		if !isAWSLegacyManagedSubnetResource(resource) {
			continue
		}
		if err := collectAWSLegacyManagedSubnetEvidence(resource, collector); err != nil {
			return nil
		}
	}
	evidence, err := collector.resolve()
	if err != nil {
		return nil
	}
	netmasks := map[string]int{}
	for tier, field := range map[string]string{
		"public":  legacyScopeFieldVPCPublicSubnetNetmask,
		"private": legacyScopeFieldVPCPrivateSubnetNetmask,
	} {
		netmask, err := awsLegacyNetmaskEvidence(evidence, field)
		if err != nil || netmask == 0 {
			continue
		}
		netmasks[tier] = netmask
	}
	return netmasks
}

func awsSubnetChangeTierList(changes []awsSubnetTierChange) string {
	tiers := make([]string, 0, len(changes))
	for _, change := range changes {
		tiers = append(tiers, change.tier)
	}
	sort.Strings(tiers)
	return strings.Join(tiers, " and ")
}

func awsSubnetChangeSummary(changes []awsSubnetTierChange) string {
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, fmt.Sprintf("  %s subnets: /%d applied, /%d requested", change.tier, change.applied, change.requested))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func awsSubnetChangeFlagHint(changes []awsSubnetTierChange) string {
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, fmt.Sprintf("  --%s %d", change.flagName, change.applied))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}

func awsSubnetChangeFieldHint(changes []awsSubnetTierChange) string {
	lines := make([]string, 0, len(changes))
	for _, change := range changes {
		lines = append(lines, fmt.Sprintf("  vpc.%s: %d", change.fieldName, change.applied))
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n")
}
