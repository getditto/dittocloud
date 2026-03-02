package bootstrap

import (
	"encoding/json"
	"fmt"

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
	cmd.Flags().StringArray("controller-trusted-role-arns", []string{}, "AWS IAM role ARNs that can assume the CAPA controller role (can be specified multiple times)")
	cmd.Flags().StringArray("iam-trusted-role-arns", []string{}, "AWS IAM role ARNs that can assume the IAM trust editor role (can be specified multiple times)")

	return cmd
}

func promptAWSValues(flags *pflag.FlagSet) ([]*tfexec.VarOption, error) {
	vars := []*tfexec.VarOption{}

	optional := color.New(color.FgYellow)
	// Ask for the profile
	vars = append(vars,
		tfexec.Var("profile="+FlagOrPrompt(flags.Lookup("aws-profile"), "Enter the AWS profile", "")),
	)

	// Ask for optional
	optional.Println("confirm parameters")

	if region := StringPrompt(
		"Enter the AWS region",
		flags.Lookup("aws-region").Value.String(),
	); region != "" {
		vars = append(vars,
			tfexec.Var("region="+region),
		)
	}
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

	return vars, nil
}
