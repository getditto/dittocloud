package bootstrap

import (
	"context"
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
	Profile   string
	Region    string
	VPCName   string
	VPCCidr   string
	AccountID string // Will be populated dynamically
}

func (a *AWSConfig) BuildTFVars() []*tfexec.VarOption {
	var vars []*tfexec.VarOption
	return append(vars,
		tfexec.Var("profile="+a.Profile),
		tfexec.Var("region="+a.Region),
		tfexec.Var("vpc_name="+a.VPCName),
		tfexec.Var("vpc_cidr="+a.VPCCidr),
	)
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
			err := promptAWSValues(cmd.Flag, config)
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

func promptAWSValues(flag func(name string) (flag *pflag.Flag), awsConfig *AWSConfig) error {
	optional := color.New(color.FgYellow)

	// Ask for the profile
	awsConfig.Profile = FlagOrPrompt(flag("aws-profile"), "Enter the AWS profile", "")

	// Ask for optional
	optional.Println("confirm parameters")

	awsConfig.Region = StringPrompt(
		"Enter the AWS region",
		flag("aws-region").Value.String(),
	)

	awsConfig.VPCName = StringPrompt(
		"Enter the VPC name",
		flag("aws-vpc-name").Value.String(),
	)

	awsConfig.VPCCidr = StringPrompt(
		"Enter the CIDR block",
		flag("aws-vpc-cidr").Value.String(),
	)
	//Set Account ID
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
