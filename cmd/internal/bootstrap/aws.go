package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/config"
	"github.com/aws/aws-sdk-go-v2/service/sts"
	"github.com/fatih/color"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// AWSConfig holds AWS-specific configuration
type AWSConfig struct {
	Profile                   string
	Region                    string
	VPCName                   string
	VPCCidr                   string
	AccountID                 string // Will be populated dynamically
	ControllerTrustedRoleArns string // JSON-encoded list of ARNs
	IAMTrustedRoleArns        string // JSON-encoded list of ARNs
}

func (a *AWSConfig) BuildTFVars() []*tfexec.VarOption {
	var vars []*tfexec.VarOption
	vars = append(vars,
		tfexec.Var("profile="+a.Profile),
		tfexec.Var("region="+a.Region),
		tfexec.Var("vpc_name="+a.VPCName),
		tfexec.Var("vpc_cidr="+a.VPCCidr),
	)
	if a.ControllerTrustedRoleArns != "" {
		vars = append(vars, tfexec.Var("controller_trusted_role_arns="+a.ControllerTrustedRoleArns))
	}
	if a.IAMTrustedRoleArns != "" {
		vars = append(vars, tfexec.Var("iam_trusted_role_arns="+a.IAMTrustedRoleArns))
	}
	return vars
}

func (a *AWSConfig) BucketURL() (string, error) {
	if a.AccountID == "" {
		return "", fmt.Errorf("account ID is required for AWS state management")
	}
	return fmt.Sprintf("s3://ditto-terraform-state-%s?region=%s", a.AccountID, a.Region), nil
}

func (a *AWSConfig) GetBackendConfig() (TerraformBackendConfig, error) {
	if a.AccountID == "" {
		return nil, fmt.Errorf("account ID is required for AWS state management")
	}

	bucketName := fmt.Sprintf("ditto-terraform-state-%s", a.AccountID)
	return &AWSBackendConfig{
		BucketName: bucketName,
		Region:     a.Region,
		KeyPrefix:  "terraform.tfstate",
	}, nil
}

// awsCmd handles aws specific variables and populates the config
func awsCmd(config *AWSConfig) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "aws",
		Short: "Bootstrap AWS",
		Long:  "Bootstrap AWS",
		RunE: func(cmd *cobra.Command, args []string) error {
			err := promptAWSValues(cmd.Flags(), config)
			if err != nil {
				return fmt.Errorf("unable to prompt for values: %w", err)
			}

			// Set the AWS configuration
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

func getAccountID(awsConfig *AWSConfig) {
	if os.Getenv("AWS_PROFILE") == "" {
		os.Setenv("AWS_PROFILE", awsConfig.Profile)
	}
	cfg, err := config.LoadDefaultConfig(
		context.Background(),
	)
	if err != nil {
		return
	}

	stsClient := sts.NewFromConfig(cfg)
	result, err := stsClient.GetCallerIdentity(context.Background(), &sts.GetCallerIdentityInput{})
	if err != nil {
		return
	}
	awsConfig.AccountID = aws.ToString(result.Account)
}

func promptAWSValues(flags *pflag.FlagSet, awsConfig *AWSConfig) error {
	optional := color.New(color.FgYellow)

	// Ask for the profile
	awsConfig.Profile = FlagOrPrompt(flags.Lookup("aws-profile"), "Enter the AWS profile", "")

	// Ask for optional
	optional.Println("confirm parameters")

	awsConfig.Region = StringPrompt(
		"Enter the AWS region",
		flags.Lookup("aws-region").Value.String(),
	)

	awsConfig.VPCName = StringPrompt(
		"Enter the VPC name",
		flags.Lookup("aws-vpc-name").Value.String(),
	)

	awsConfig.VPCCidr = StringPrompt(
		"Enter the CIDR block",
		flags.Lookup("aws-vpc-cidr").Value.String(),
	)

	// Handle controller-trusted-role-arns flag from CLI
	if flags.Changed("controller-trusted-role-arns") {
		arns, err := flags.GetStringArray("controller-trusted-role-arns")
		if err != nil {
			return fmt.Errorf("unable to get controller-trusted-role-arns: %w", err)
		}
		jsonStr, err := json.Marshal(arns)
		if err != nil {
			return fmt.Errorf("unable to marshal controller-trusted-role-arns: %w", err)
		}
		awsConfig.ControllerTrustedRoleArns = string(jsonStr)
	}

	// Handle iam-trusted-role-arns flag from CLI
	if flags.Changed("iam-trusted-role-arns") {
		arns, err := flags.GetStringArray("iam-trusted-role-arns")
		if err != nil {
			return fmt.Errorf("unable to get iam-trusted-role-arns: %w", err)
		}
		jsonStr, err := json.Marshal(arns)
		if err != nil {
			return fmt.Errorf("unable to marshal iam-trusted-role-arns: %w", err)
		}
		awsConfig.IAMTrustedRoleArns = string(jsonStr)
	}

	// Set Account ID
	getAccountID(awsConfig)
	return nil
}

// AWSBackendConfig implements TerraformBackendConfig for AWS
type AWSBackendConfig struct {
	BucketName string
	Region     string
	KeyPrefix  string
}

func (c *AWSBackendConfig) BackendConfigFile() (string, error) {
	return `terraform {
  backend "s3" {}
}
`, nil
}

func (c *AWSBackendConfig) GetBackendConfig() ([]tfexec.InitOption, error) {
	return []tfexec.InitOption{
		tfexec.BackendConfig(fmt.Sprintf("bucket=%s", c.BucketName)),
		tfexec.BackendConfig(fmt.Sprintf("region=%s", c.Region)),
		tfexec.BackendConfig(fmt.Sprintf("key=%s", c.KeyPrefix)),
	}, nil
}

func (c *AWSBackendConfig) GetBackendType() string {
	return "s3"
}
