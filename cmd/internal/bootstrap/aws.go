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
				scopeVars, err := awsScopeTerraformVariables(cmd.Flags(), encodedScopes)
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
				if cmd.Flags().Changed("aws-vpc-name") {
					return fmt.Errorf("--aws-vpc-name can only be used when --create-vpc=true")
				}
				if cmd.Flags().Changed("aws-vpc-cidr") {
					return fmt.Errorf("--aws-vpc-cidr can only be used when --create-vpc=true")
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
	cmd.Flags().String("aws-vpc-cidr", "10.210.0.0/16", "AWS VPC CIDR block to use")
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
	for _, scopeRef := range sortedAWSDeploymentScopeRefs(scopes) {
		scope := scopes[scopeRef]
		if scope.ScopeTagPolicyVersion == 1 {
			return false, nil, "", nil, fmt.Errorf(
				"scope %q cannot use scopeTagPolicyVersion: 1 until the verified tag-policy transition workflow is implemented; keep it at 0",
				scopeRef,
			)
		}
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
		if _, err := fmt.Fprintf(
			writer,
			"  - %s%s: region=%s clusterType=%s vpcMode=%s%s\n",
			scopeRef,
			defaultMarker,
			scope.Region,
			scope.ClusterType,
			scope.VPC.Mode,
			natGatewayName,
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
func awsScopeTerraformVariables(flags *pflag.FlagSet, encodedScopes string) ([]string, error) {
	values := []string{"deployment_scopes=" + encodedScopes}

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
