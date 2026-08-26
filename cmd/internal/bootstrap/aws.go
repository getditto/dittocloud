package bootstrap

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/fatih/color"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// awsCmd handles aws specific variables and mutates the list of vars to be passed to terraform plan/apply
func awsCmd(vars *[]*tfexec.VarOption) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Bootstrap AWS",
		Long:  "Bootstrap AWS",
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			defer releaseCommandOperationLockOnError(cmd, &runErr)
			generated, err := maybeGenerateAWSLegacyScopesFile(cmd)
			if err != nil {
				return err
			}
			if generated {
				return nil
			}

			scopeMode, scopes, encodedScopes, allowedScopeRemovals, err := validateAWSScopesFlags(cmd.Flags())
			if err != nil {
				return err
			}
			if scopeMode {
				if err := validateAWSStateScopeLifecycle(
					commandCanonicalStatePath(cmd),
					scopes,
					allowedScopeRemovals,
				); err != nil {
					return err
				}
				allowRenumbering, err := cmd.Flags().GetBool(awsAllowSubnetRenumberingFlag)
				if err != nil {
					return fmt.Errorf("unable to get %s: %w", awsAllowSubnetRenumberingFlag, err)
				}
				if err := validateAWSScopeSubnetRenumbering(
					commandCanonicalStatePath(cmd),
					scopes,
					allowRenumbering,
				); err != nil {
					return err
				}
				profile, err := cmd.Flags().GetString("aws-profile")
				if err != nil {
					return fmt.Errorf("unable to get aws-profile: %w", err)
				}
				transitionConfiguration, authorizedPolicyRefs, err := prepareAWSScopeTagPolicyTransition(
					commandCanonicalStatePath(cmd),
					profile,
					scopes,
				)
				if err != nil {
					return err
				}
				legacyVersionZeroClusterPolicyRefs, err := detectAWSLegacyVersionZeroClusterPolicyRefs(
					commandCanonicalStatePath(cmd),
					scopes,
				)
				if err != nil {
					return err
				}
				if len(transitionConfiguration.Scopes) > 0 {
					if cmd.Flags().Changed("import-resource") {
						return fmt.Errorf("--import-resource cannot be combined with a scopeTagPolicyVersion 0 to 1 transition; complete imports at version 0 first")
					}
					cmd.SetContext(setAWSScopeTagPolicyTransitionConfiguration(cmd.Context(), transitionConfiguration))
				}
				if cmd.Flags().Changed("import-resource") {
					importValues, err := cmd.Flags().GetStringArray("import-resource")
					if err != nil {
						return fmt.Errorf("unable to get import-resource: %w", err)
					}
					imports, err := parseResourceImports(importValues)
					if err != nil {
						return err
					}
					configuration, err := prepareAWSScopedImportConfiguration(
						commandCanonicalStatePath(cmd),
						scopes,
						encodedScopes,
						imports,
					)
					if err != nil {
						return err
					}
					cmd.SetContext(setAWSScopedImportConfiguration(cmd.Context(), configuration))
				} else {
					initialMigrationConfiguration, initialMigration, err := prepareAWSInitialScopeMigrationPlanConfiguration(
						commandCanonicalStatePath(cmd),
						scopes,
					)
					if err != nil {
						return err
					}
					if initialMigration {
						cmd.SetContext(setAWSInitialScopeMigrationPlanConfiguration(cmd.Context(), initialMigrationConfiguration))
					}
				}
				scopeVars, err := awsScopeTerraformVariables(
					cmd.Flags(),
					encodedScopes,
					authorizedPolicyRefs,
					legacyVersionZeroClusterPolicyRefs,
				)
				if err != nil {
					return err
				}
				if err := writeAWSScopesSummary(cmd.OutOrStdout(), scopes); err != nil {
					return fmt.Errorf("unable to display AWS deployment scope summary: %w", err)
				}
				for _, value := range scopeVars {
					*vars = append(*vars, tfexec.Var(value))
				}
				return nil
			}
			if err := validateAWSLegacyModeState(commandCanonicalStatePath(cmd)); err != nil {
				return err
			}
			if err := validateAWSLegacySubnetRenumbering(commandCanonicalStatePath(cmd), cmd.Flags()); err != nil {
				return err
			}

			customerManagedVPC, err := cmd.Flags().GetBool("customer-managed-vpc")
			if err != nil {
				return fmt.Errorf("unable to get customer-managed-vpc: %w", err)
			}
			createVPC, err := cmd.Flags().GetBool("create-vpc")
			if err != nil {
				return fmt.Errorf("unable to get create-vpc: %w", err)
			}
			if customerManagedVPC {
				if cmd.Flags().Changed("create-vpc") && createVPC {
					return fmt.Errorf("--create-vpc=true cannot be used with --customer-managed-vpc")
				}
				vpcID, err := cmd.Flags().GetString("vpc-id")
				if err != nil {
					return fmt.Errorf("unable to get vpc-id: %w", err)
				}
				if strings.TrimSpace(vpcID) == "" {
					return fmt.Errorf("--vpc-id is required with --customer-managed-vpc so IAM permissions can be restricted to the existing VPC")
				}
			}
			if customerManagedVPC || !createVPC {
				for _, flagName := range awsManagedVPCFlags {
					if cmd.Flags().Changed(flagName) {
						return fmt.Errorf("--%s can only be used when --create-vpc=true", flagName)
					}
				}
			}
			if cmd.Flags().Changed("cluster-name") {
				stateFile := cmd.Flag("state").Value.String()
				if _, err := os.Stat(stateFile); os.IsNotExist(err) {
					return fmt.Errorf("--cluster-name requires an existing deployment; no state file found at %q\n\nRun bootstrap without --cluster-name first to create the initial deployment", stateFile)
				}
			}
			promptedAwsVars, err := promptAWSValues(cmd.Flags())
			if err != nil {
				return fmt.Errorf("unable to prompt for values: %w", err)
			}
			// Append the prompted AWS variables to the 'vars' slice that the parent command uses for terraform plan/apply
			*vars = append(*vars, promptedAwsVars...)
			return nil
		},
	}

	cmd.Flags().String("aws-profile", "", "AWS profile to use")
	cmd.Flags().String("aws-region", "us-east-1", "AWS region to use")
	cmd.Flags().String("aws-vpc-name", "ditto", "AWS VPC name to use")
	cmd.Flags().String("aws-vpc-cidr", "10.210.0.0/16", "Primary AWS VPC CIDR block, carrying load balancers, NAT gateways, and explicitly placed EC2 only")
	cmd.Flags().String(
		"aws-vpc-secondary-cidr",
		"",
		"Secondary VPC CIDR block carrying pod, node, and database capacity; a /16 inside 100.64.0.0/10, the same on every VPC because it is never routed outside its own",
	)
	cmd.Flags().Int(
		"aws-vpc-public-subnet-netmask",
		24,
		"Netmask for each per-AZ public subnet; pin this to the value an existing deployment already uses, because changing it renumbers live subnets",
	)
	cmd.Flags().Int(
		"aws-vpc-private-subnet-netmask",
		23,
		"Netmask for each per-AZ private subnet; pin this to the value an existing deployment already uses, because changing it renumbers live subnets",
	)
	cmd.Flags().StringArray(
		"aws-vpc-nat-eip-allocation-ids",
		[]string{},
		"Pre-allocated Elastic IP allocation IDs for the NAT gateways, one per availability zone in order (repeatable)",
	)
	cmd.Flags().Bool(
		awsAllowSubnetRenumberingFlag,
		false,
		"Authorize a subnet netmask change that replaces existing subnets, and the NAT gateways, nodes, and load balancers attached to them",
	)
	cmd.Flags().String(
		"karpenter-discovery-tag-value",
		"",
		"Value for the karpenter.sh/discovery tag on the node subnets; defaults to --cluster-name when set",
	)
	cmd.Flags().Bool("create-vpc", true, "Create the VPC with Dittocloud; set false to retain VPC lifecycle permissions for Cluster API to create it")
	cmd.Flags().Bool("enable-eks", false, "Provision EKS IAM permissions and enforce the EKS account-level IMDSv2 default")
	cmd.Flags().StringArray("controller-trusted-role-arns", []string{}, "AWS IAM role ARNs that can assume the CAPA controller role (can be specified multiple times)")
	cmd.Flags().StringArray("iam-trusted-role-arns", []string{}, "AWS IAM role ARNs that can assume the IAM trust editor role (can be specified multiple times)")
	cmd.Flags().Bool("customer-managed-vpc", false, "Set when the customer provides their own VPC; skips VPC creation and omits VPC lifecycle permissions from the CAPA controller role")
	cmd.Flags().String("vpc-id", "", "ID of an existing VPC; required with --customer-managed-vpc so CAPA EC2 operations can be restricted to it")
	cmd.Flags().String("cluster-name", "", "Tighten IAM conditions to a specific cluster name; requires an existing state file (re-runs only)")
	cmd.Flags().Bool("scopes", false, "Enable AWS multi-scope mode using a scopes YAML file")
	cmd.Flags().String("scopes-file", "", "Path to an AWS deployment scopes YAML file; requires --scopes")
	cmd.Flags().Bool("generate-scopes-file", false, "Generate a review-only default-scope YAML draft from legacy Terraform state; requires --scopes")
	cmd.Flags().StringArray(
		"allow-scope-removal",
		[]string{},
		"Authorize omission of one state-backed non-default scope reference (repeatable; requires --scopes)",
	)
	cmd.AddCommand(awsScopesCmd())

	return cmd
}

func validateAWSScopesFlags(flags *pflag.FlagSet) (bool, AWSDeploymentScopes, string, []string, error) {
	scopeMode, err := flags.GetBool("scopes")
	if err != nil {
		return false, nil, "", nil, fmt.Errorf("unable to get scopes: %w", err)
	}
	scopesFile, err := flags.GetString("scopes-file")
	if err != nil {
		return false, nil, "", nil, fmt.Errorf("unable to get scopes-file: %w", err)
	}
	allowedScopeRemovals, err := flags.GetStringArray("allow-scope-removal")
	if err != nil {
		return false, nil, "", nil, fmt.Errorf("unable to get allow-scope-removal: %w", err)
	}

	if !scopeMode {
		if strings.TrimSpace(scopesFile) != "" {
			return false, nil, "", nil, fmt.Errorf("--scopes-file requires --scopes=true")
		}
		if flags.Changed("allow-scope-removal") {
			return false, nil, "", nil, fmt.Errorf("--allow-scope-removal requires --scopes=true")
		}
		return false, nil, "", nil, nil
	}
	if strings.TrimSpace(scopesFile) == "" {
		return false, nil, "", nil, fmt.Errorf("--scopes-file is required with --scopes=true")
	}
	if flags.Changed("tf-var") {
		return false, nil, "", nil, fmt.Errorf("--tf-var cannot be used with --scopes=true; scope mode accepts only validated scope YAML and explicit account-level flags")
	}

	legacyScopeFlags := []string{
		"aws-region",
		"aws-vpc-name",
		"aws-vpc-cidr",
		"aws-vpc-secondary-cidr",
		"aws-vpc-public-subnet-netmask",
		"aws-vpc-private-subnet-netmask",
		"aws-vpc-nat-eip-allocation-ids",
		"karpenter-discovery-tag-value",
		"create-vpc",
		"enable-eks",
		"customer-managed-vpc",
		"vpc-id",
		"cluster-name",
	}
	for _, flagName := range legacyScopeFlags {
		if flags.Changed(flagName) {
			return false, nil, "", nil, fmt.Errorf("--%s cannot be used with --scopes=true; configure it in the scopes YAML", flagName)
		}
	}

	scopes, err := loadAWSDeploymentScopes(scopesFile)
	if err != nil {
		return false, nil, "", nil, err
	}
	encodedScopes, err := marshalAWSDeploymentScopes(scopes)
	if err != nil {
		return false, nil, "", nil, err
	}
	return true, scopes, encodedScopes, allowedScopeRemovals, nil
}

func writeAWSScopesSummary(writer io.Writer, scopes AWSDeploymentScopes) error {
	scopeRefs := sortedAWSDeploymentScopeRefs(scopes)

	if _, err := fmt.Fprintf(writer, "AWS deployment scopes (%d):\n", len(scopeRefs)); err != nil {
		return err
	}
	for _, scopeRef := range scopeRefs {
		scope := scopes[scopeRef]
		defaultMarker := ""
		if scope.Default {
			defaultMarker = " [default]"
		}
		natGatewayName := ""
		if scope.VPC.NATGatewayName != "" {
			natGatewayName = " natGatewayName=" + scope.VPC.NATGatewayName
		}
		secondaryCIDR := ""
		if scope.VPC.SecondaryCIDR != "" {
			secondaryCIDR = " secondaryCidr=" + scope.VPC.SecondaryCIDR
		}
		clusterName := ""
		if scope.ClusterName != "" {
			clusterName = " clusterName=" + scope.ClusterName
		}
		if _, err := fmt.Fprintf(
			writer,
			"  - %s%s: region=%s clusterType=%s vpcMode=%s%s scopeTagPolicyVersion=%d%s%s\n",
			scopeRef,
			defaultMarker,
			scope.Region,
			scope.ClusterType,
			scope.VPC.Mode,
			clusterName,
			scope.ScopeTagPolicyVersion,
			natGatewayName,
			secondaryCIDR,
		); err != nil {
			return err
		}
	}
	return nil
}

func sortedAWSDeploymentScopeRefs(scopes AWSDeploymentScopes) []string {
	scopeRefs := make([]string, 0, len(scopes))
	for scopeRef := range scopes {
		scopeRefs = append(scopeRefs, scopeRef)
	}
	sort.Strings(scopeRefs)
	return scopeRefs
}

// awsScopeTerraformVariables returns the complete account-level input shared by
// every deployment scope. Scope-owned values are carried only in the validated
// deployment_scopes object.
func awsScopeTerraformVariables(
	flags *pflag.FlagSet,
	encodedScopes string,
	authorizedPolicyRefs []string,
	legacyVersionZeroClusterPolicyRefs []string,
) ([]string, error) {
	values := []string{"deployment_scopes=" + encodedScopes}
	if len(authorizedPolicyRefs) > 0 {
		encodedRefs, err := json.Marshal(authorizedPolicyRefs)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal verified scope tag-policy references: %w", err)
		}
		values = append(values, "scope_tag_policy_cli_authorized_refs="+string(encodedRefs))
	}
	if len(legacyVersionZeroClusterPolicyRefs) > 0 {
		encodedRefs, err := json.Marshal(legacyVersionZeroClusterPolicyRefs)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal preserved legacy version-0 cluster-policy references: %w", err)
		}
		values = append(values, "scope_tag_policy_v0_legacy_cluster_refs="+string(encodedRefs))
	}

	if flags.Changed("aws-profile") {
		profile, err := flags.GetString("aws-profile")
		if err != nil {
			return nil, fmt.Errorf("unable to get aws-profile: %w", err)
		}
		values = append(values, "profile="+profile)
	}

	sharedARNFlags := []struct {
		flagName     string
		variableName string
	}{
		{flagName: "controller-trusted-role-arns", variableName: "controller_trusted_role_arns"},
		{flagName: "iam-trusted-role-arns", variableName: "iam_trusted_role_arns"},
	}
	for _, sharedFlag := range sharedARNFlags {
		if !flags.Changed(sharedFlag.flagName) {
			continue
		}
		arns, err := flags.GetStringArray(sharedFlag.flagName)
		if err != nil {
			return nil, fmt.Errorf("unable to get %s: %w", sharedFlag.flagName, err)
		}
		encodedARNs, err := json.Marshal(arns)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal %s: %w", sharedFlag.flagName, err)
		}
		values = append(values, sharedFlag.variableName+"="+string(encodedARNs))
	}

	return values, nil
}

const awsAllowSubnetRenumberingFlag = "allow-subnet-renumbering"

// awsManagedVPCFlags configure a VPC that Dittocloud creates, so none of them
// mean anything without --create-vpc.
var awsManagedVPCFlags = []string{
	"aws-vpc-name",
	"aws-vpc-cidr",
	"aws-vpc-secondary-cidr",
	"aws-vpc-public-subnet-netmask",
	"aws-vpc-private-subnet-netmask",
	"aws-vpc-nat-eip-allocation-ids",
	"karpenter-discovery-tag-value",
}

// awsManagedVPCTerraformVariables forwards only the subnet and NAT settings an
// operator actually set. Anything left alone keeps the module default, which
// matters most for the subnet netmasks: sending a value an existing deployment
// was not built with renumbers its subnets.
func awsManagedVPCTerraformVariables(flags *pflag.FlagSet) ([]*tfexec.VarOption, error) {
	vars := []*tfexec.VarOption{}
	stringFlags := []struct {
		flagName     string
		variableName string
	}{
		{flagName: "aws-vpc-secondary-cidr", variableName: "vpc_secondary_cidr"},
		{flagName: "karpenter-discovery-tag-value", variableName: "karpenter_discovery_tag_value"},
	}
	for _, stringFlag := range stringFlags {
		if !flags.Changed(stringFlag.flagName) {
			continue
		}
		value, err := flags.GetString(stringFlag.flagName)
		if err != nil {
			return nil, fmt.Errorf("unable to get %s: %w", stringFlag.flagName, err)
		}
		vars = append(vars, tfexec.Var(stringFlag.variableName+"="+value))
	}

	netmaskFlags := []struct {
		flagName     string
		variableName string
	}{
		{flagName: "aws-vpc-public-subnet-netmask", variableName: "public_subnet_netmask"},
		{flagName: "aws-vpc-private-subnet-netmask", variableName: "private_subnet_netmask"},
	}
	for _, netmaskFlag := range netmaskFlags {
		if !flags.Changed(netmaskFlag.flagName) {
			continue
		}
		value, err := flags.GetInt(netmaskFlag.flagName)
		if err != nil {
			return nil, fmt.Errorf("unable to get %s: %w", netmaskFlag.flagName, err)
		}
		vars = append(vars, tfexec.Var(fmt.Sprintf("%s=%d", netmaskFlag.variableName, value)))
	}

	if flags.Changed("aws-vpc-nat-eip-allocation-ids") {
		allocationIDs, err := flags.GetStringArray("aws-vpc-nat-eip-allocation-ids")
		if err != nil {
			return nil, fmt.Errorf("unable to get aws-vpc-nat-eip-allocation-ids: %w", err)
		}
		if err := validateAWSScopeNATEIPAllocationIDs("legacy deployment", allocationIDs); err != nil {
			return nil, err
		}
		encoded, err := json.Marshal(allocationIDs)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal aws-vpc-nat-eip-allocation-ids: %w", err)
		}
		vars = append(vars, tfexec.Var("nat_gateway_eip_allocation_ids="+string(encoded)))
	}
	return vars, nil
}

func promptAWSValues(flags *pflag.FlagSet) ([]*tfexec.VarOption, error) {
	vars := []*tfexec.VarOption{}
	customerManagedVPC, err := flags.GetBool("customer-managed-vpc")
	if err != nil {
		return nil, fmt.Errorf("unable to get customer-managed-vpc: %w", err)
	}
	createVPC, err := flags.GetBool("create-vpc")
	if err != nil {
		return nil, fmt.Errorf("unable to get create-vpc: %w", err)
	}

	optional := color.New(color.FgYellow)
	// Ask for the profile
	vars = append(vars,
		tfexec.Var("profile="+FlagOrPrompt(flags.Lookup("aws-profile"), "Enter the AWS profile", "")),
	)

	// Ask for optional
	_, _ = optional.Println("confirm parameters")

	if region := StringPrompt(
		"Enter the AWS region",
		flags.Lookup("aws-region").Value.String(),
	); region != "" {
		vars = append(vars,
			tfexec.Var("region="+region),
		)
	}
	if createVPC && !customerManagedVPC {
		if vpcName := StringPrompt(
			"Enter the VPC name",
			flags.Lookup("aws-vpc-name").Value.String(),
		); vpcName != "" {
			vars = append(vars,
				tfexec.Var("vpc_name="+vpcName),
			)
		}
		if cidr := StringPrompt(
			"Enter the CIDR block",
			flags.Lookup("aws-vpc-cidr").Value.String(),
		); cidr != "" {
			vars = append(vars,
				tfexec.Var("vpc_cidr="+cidr),
			)
		}
		managedVPCVars, err := awsManagedVPCTerraformVariables(flags)
		if err != nil {
			return nil, err
		}
		vars = append(vars, managedVPCVars...)
	}

	if flags.Changed("controller-trusted-role-arns") {
		arns, err := flags.GetStringArray("controller-trusted-role-arns")
		if err != nil {
			return nil, fmt.Errorf("unable to get controller-trusted-role-arns: %w", err)
		}
		jsonStr, err := json.Marshal(arns)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal controller-trusted-role-arns: %w", err)
		}
		vars = append(vars, tfexec.Var("controller_trusted_role_arns="+string(jsonStr)))
	}

	if flags.Changed("iam-trusted-role-arns") {
		arns, err := flags.GetStringArray("iam-trusted-role-arns")
		if err != nil {
			return nil, fmt.Errorf("unable to get iam-trusted-role-arns: %w", err)
		}
		jsonStr, err := json.Marshal(arns)
		if err != nil {
			return nil, fmt.Errorf("unable to marshal iam-trusted-role-arns: %w", err)
		}
		vars = append(vars, tfexec.Var("iam_trusted_role_arns="+string(jsonStr)))
	}

	if flags.Changed("customer-managed-vpc") {
		vars = append(vars, tfexec.Var(fmt.Sprintf("customer_managed_vpc=%t", customerManagedVPC)))
	}
	if customerManagedVPC {
		vars = append(vars, tfexec.Var("create_vpc=false"))
	} else if flags.Changed("create-vpc") {
		vars = append(vars, tfexec.Var(fmt.Sprintf("create_vpc=%t", createVPC)))
	}

	if flags.Changed("enable-eks") {
		enableEKS, err := flags.GetBool("enable-eks")
		if err != nil {
			return nil, fmt.Errorf("unable to get enable-eks: %w", err)
		}
		vars = append(vars, tfexec.Var(fmt.Sprintf("enable_eks=%t", enableEKS)))
	}

	if flags.Changed("vpc-id") {
		vpcID, err := flags.GetString("vpc-id")
		if err != nil {
			return nil, fmt.Errorf("unable to get vpc-id: %w", err)
		}
		vars = append(vars, tfexec.Var("vpc_id="+vpcID))
	}

	if flags.Changed("cluster-name") {
		clusterName, err := flags.GetString("cluster-name")
		if err != nil {
			return nil, fmt.Errorf("unable to get cluster-name: %w", err)
		}
		vars = append(vars, tfexec.Var("cluster_name="+clusterName))
	}

	return vars, nil
}
