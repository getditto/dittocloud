package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const maxScopeReferenceGenerationAttempts = 1024

var awsScopeAddInputIsInteractive = func() bool {
	return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
}

func awsScopesCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scopes",
		Short: "Manage AWS deployment scope configuration",
		// Cobra executes only the closest persistent hooks. These deliberate
		// no-ops prevent configuration-only scope commands from inheriting the
		// bootstrap Terraform lifecycle.
		PersistentPreRunE:  func(cmd *cobra.Command, args []string) error { return nil },
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error { return nil },
	}
	cmd.AddCommand(awsScopesAddCmd())
	cmd.AddCommand(awsScopesRecoverCmd())
	cmd.AddCommand(awsScopesMigrateCmd())
	return cmd
}

func awsScopesAddCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "add",
		Short: "Generate and append an AWS deployment scope",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			if err := rejectAWSScopesAddTerraformFlags(cmd.Flags()); err != nil {
				return err
			}
			scopesFilePath, err := cmd.Flags().GetString("scopes-file")
			if err != nil {
				return fmt.Errorf("unable to get scopes-file: %w", err)
			}
			if strings.TrimSpace(scopesFilePath) == "" {
				return fmt.Errorf("--scopes-file is required")
			}
			scope, err := collectAWSScopesAddInput(cmd.Flags())
			if err != nil {
				return err
			}

			statePath := cmd.Flag("state").Value.String()
			operationLock, err := acquireStateOperationLock(statePath, "bootstrap aws scopes add")
			if err != nil {
				return err
			}
			defer func() {
				if operationLock != nil {
					runErr = errors.Join(runErr, operationLock.Release())
				}
			}()

			fileLock, err := acquireScopesFileLock(scopesFilePath, "bootstrap aws scopes add")
			if err != nil {
				return err
			}
			defer func() {
				if fileLock != nil {
					runErr = errors.Join(runErr, fileLock.Release())
				}
			}()

			document, err := loadAWSDeploymentScopesDocument(fileLock.canonicalPath)
			if err != nil {
				return err
			}
			if err := validateAWSScopesAddStateBoundary(
				operationLock.canonicalStatePath,
				document,
				cmd.Flags().Changed("default"),
				scope.Default,
			); err != nil {
				return err
			}
			if err := operationLock.Release(); err != nil {
				return fmt.Errorf("unable to release Dittocloud operation lock after scope-add state discovery: %w", err)
			}
			operationLock = nil

			scopeRef, err := generateUnusedAWSDeploymentScopeReference(document.scopes)
			if err != nil {
				return err
			}
			encodedDocument, err := document.appendScope(scopeRef, scope)
			if err != nil {
				return err
			}
			if err := persistAWSDeploymentScopesFile(
				fileLock.canonicalPath,
				encodedDocument,
				document.permissions,
			); err != nil {
				return err
			}
			if err := fileLock.Release(); err != nil {
				return fmt.Errorf("unable to release Dittocloud scopes-file lock: %w", err)
			}
			fileLock = nil

			_, err = fmt.Fprintln(cmd.OutOrStdout(), scopeRef)
			return err
		},
	}

	cmd.Flags().String("scopes-file", "", "Path to the AWS deployment scopes YAML file")
	cmd.Flags().Bool("default", false, "Create the first default scope in a greenfield configuration")
	cmd.Flags().String("region", "", "AWS Region owned by the scope")
	cmd.Flags().String("cluster-type", awsClusterTypeKubeadm, "Cluster type: kubeadm or eks")
	cmd.Flags().String("cluster-name", "", "Optional Kubernetes cluster name")
	cmd.Flags().String("vpc-mode", "", "VPC ownership mode: dittocloud, existing, or capi")
	cmd.Flags().String("vpc-name", "", "VPC name; required for dittocloud mode")
	cmd.Flags().String("vpc-cidr", "", "VPC CIDR; required for dittocloud mode")
	cmd.Flags().String("vpc-id", "", "VPC ID; required for existing mode and optional for capi mode")
	return cmd
}

func rejectAWSScopesAddTerraformFlags(flags *pflag.FlagSet) error {
	return rejectAWSScopesConfigurationOnlyTerraformFlags(flags, "configuration-only scopes add")
}

func rejectAWSScopesConfigurationOnlyTerraformFlags(flags *pflag.FlagSet, operation string) error {
	for _, flagName := range []string{
		"dry-run",
		"force-terraform-download",
		"import-resource",
		"log-level",
		"no-color",
		"remove-tmpdir",
		"tf-var",
	} {
		if flags.Changed(flagName) {
			return fmt.Errorf("--%s cannot be used with %s", flagName, operation)
		}
	}
	return nil
}

func collectAWSScopesAddInput(flags *pflag.FlagSet) (AWSDeploymentScope, error) {
	defaultScope, err := flags.GetBool("default")
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("unable to get default: %w", err)
	}
	clusterType, err := flags.GetString("cluster-type")
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("unable to get cluster-type: %w", err)
	}
	clusterName, err := flags.GetString("cluster-name")
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("unable to get cluster-name: %w", err)
	}
	region, err := requiredAWSScopesAddString(flags, "region", "Enter the AWS Region")
	if err != nil {
		return AWSDeploymentScope{}, err
	}
	vpcMode, err := flags.GetString("vpc-mode")
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("unable to get vpc-mode: %w", err)
	}
	if strings.TrimSpace(vpcMode) == "" && awsScopeAddInputIsInteractive() {
		vpcMode = OptionsPrompt("Select the VPC mode", []string{awsVPCModeDittocloud, awsVPCModeExisting, awsVPCModeCAPI})
	}
	if strings.TrimSpace(vpcMode) == "" {
		return AWSDeploymentScope{}, fmt.Errorf("--vpc-mode is required")
	}

	vpcName, err := flags.GetString("vpc-name")
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("unable to get vpc-name: %w", err)
	}
	vpcCIDR, err := flags.GetString("vpc-cidr")
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("unable to get vpc-cidr: %w", err)
	}
	vpcID, err := flags.GetString("vpc-id")
	if err != nil {
		return AWSDeploymentScope{}, fmt.Errorf("unable to get vpc-id: %w", err)
	}
	vpc := AWSScopeVPC{Mode: vpcMode, Name: vpcName, CIDR: vpcCIDR, ID: vpcID}
	switch vpcMode {
	case awsVPCModeDittocloud:
		vpc.Name, err = requiredAWSScopesAddString(flags, "vpc-name", "Enter the VPC name")
		if err == nil {
			vpc.CIDR, err = requiredAWSScopesAddString(flags, "vpc-cidr", "Enter the VPC CIDR")
		}
	case awsVPCModeExisting:
		vpc.ID, err = requiredAWSScopesAddString(flags, "vpc-id", "Enter the existing VPC ID")
	case awsVPCModeCAPI:
		if strings.TrimSpace(vpc.ID) == "" && awsScopeAddInputIsInteractive() {
			vpc.ID = StringPrompt("Enter the optional discovered VPC ID", "")
		}
	default:
		return AWSDeploymentScope{}, fmt.Errorf("--vpc-mode must be one of %q, %q, or %q", awsVPCModeDittocloud, awsVPCModeExisting, awsVPCModeCAPI)
	}
	if err != nil {
		return AWSDeploymentScope{}, err
	}
	if strings.TrimSpace(clusterName) == "" && awsScopeAddInputIsInteractive() {
		clusterName = StringPrompt("Enter the optional Kubernetes cluster name", "")
	}

	scope := AWSDeploymentScope{
		Default:               defaultScope,
		ClusterName:           clusterName,
		ClusterType:           clusterType,
		Region:                region,
		ScopeTagPolicyVersion: 0,
		VPC:                   vpc,
	}
	if err := validateAWSDeploymentScopeFields("new scope", scope); err != nil {
		return AWSDeploymentScope{}, err
	}
	return scope, nil
}

func requiredAWSScopesAddString(flags *pflag.FlagSet, flagName, prompt string) (string, error) {
	value, err := flags.GetString(flagName)
	if err != nil {
		return "", fmt.Errorf("unable to get %s: %w", flagName, err)
	}
	if strings.TrimSpace(value) == "" && awsScopeAddInputIsInteractive() {
		value = StringPrompt(prompt, "")
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("--%s is required", flagName)
	}
	return value, nil
}

func validateAWSScopesAddStateBoundary(
	statePath string,
	document *awsDeploymentScopesDocument,
	defaultFlagChanged bool,
	defaultRequested bool,
) error {
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return err
	}
	if document.empty {
		if !defaultRequested {
			return fmt.Errorf("--default is required to create the first scope in a greenfield configuration")
		}
		if registry.Present {
			return fmt.Errorf("Terraform state %q already contains a scope registry but the scopes file is empty; run bootstrap aws scopes recover", statePath)
		}
		if !registry.StateEmpty {
			return fmt.Errorf("Terraform state %q contains a legacy deployment; use the guarded legacy scopes-file generation workflow instead of scopes add --default", statePath)
		}
		return nil
	}

	if defaultFlagChanged {
		return fmt.Errorf("--default cannot be used when adding to an existing scopes file")
	}
	if !registry.StateEmpty && !registry.Present {
		return fmt.Errorf("legacy Terraform state %q must seed its default scope registry before additional scopes are added", statePath)
	}
	if registry.Present {
		return validateAWSStateScopeLifecycle(statePath, document.scopes, nil)
	}
	return nil
}

func generateUnusedAWSDeploymentScopeReference(existingScopes AWSDeploymentScopes) (string, error) {
	for attempt := 0; attempt < maxScopeReferenceGenerationAttempts; attempt++ {
		scopeRef, err := nextAWSDeploymentScopeReference()
		if err != nil {
			return "", err
		}
		if !awsScopeReferencePattern.MatchString(scopeRef) {
			return "", fmt.Errorf("scope reference generator produced invalid value %q", scopeRef)
		}
		if _, exists := existingScopes[scopeRef]; !exists {
			return scopeRef, nil
		}
	}
	return "", fmt.Errorf("unable to generate an unused scope reference after %d attempts", maxScopeReferenceGenerationAttempts)
}
