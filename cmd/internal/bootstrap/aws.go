package bootstrap

import (
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
			promptedAwsVars, err := promptAWSValues(cmd.Flag)
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

	return cmd
}
func promptAWSValues(flag func(name string) (flag *pflag.Flag)) ([]*tfexec.VarOption, error) {
	vars := []*tfexec.VarOption{}

	optional := color.New(color.FgYellow)
	// Ask for the profile
	vars = append(vars,
		// tfexec.Var("profile="+StringPrompt("Enter the AWS profile", flag("aws-profile").Value.String())),
		tfexec.Var("profile="+FlagOrPrompt(flag("aws-profile"), "Enter the AWS profile", "")),
	)

	// Ask for optional
	optional.Println("confirm parameters")

	if region := StringPrompt(
		"Enter the AWS region",
		flag("aws-region").Value.String(),
	); region != "" {
		vars = append(vars,
			tfexec.Var("region="+region),
		)
	}
	if vpcName := StringPrompt(
		"Enter the VPC name",
		flag("aws-vpc-name").Value.String(),
	); vpcName != "" {
		vars = append(vars,
			tfexec.Var("vpc_name="+vpcName),
		)
	}
	if cidr := StringPrompt(
		"Enter the CIDR block",
		flag("aws-vpc-cidr").Value.String(),
	); cidr != "" {
		vars = append(vars,
			tfexec.Var("vpc_cidr="+cidr),
		)
	}

	return vars, nil
}
