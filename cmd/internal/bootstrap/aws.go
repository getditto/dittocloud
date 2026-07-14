package bootstrap

import (
	"encoding/json"
	"fmt"
	"os"
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
		RunE: func(cmd *cobra.Command, args []string) error {
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

	return cmd
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
