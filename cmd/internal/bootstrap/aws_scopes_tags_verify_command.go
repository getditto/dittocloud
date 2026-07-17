package bootstrap

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const awsScopeIdentityTagKey = "ditto.live/scope-ref"

type awsScopeTagVerificationRequest struct {
	ScopeRef   string
	Scope      AWSDeploymentScope
	AccountID  string
	StatePath  string
	AWSProfile string
	State      []awsScopeTagExpectedResource
	Exclusions []string
}

type awsScopeTagVerificationReport struct {
	ScopeRef             string
	AccountID            string
	ClusterName          string
	ClusterType          string
	Region               string
	StateResources       []awsScopeTagVerifiedResource
	ClusterResources     []awsScopeTagVerifiedResource
	ExplicitExclusions   []string
	NativeDiscoveryKeys  []string
	AppliedPolicyVersion int
}

type awsScopeTagExpectedResource struct {
	Address    string
	Type       string
	Identifier string
	ARN        string
	Region     string
	QueueURL   string
}

type awsScopeTagVerifiedResource struct {
	Identity string
	Type     string
	Tags     map[string]string
}

type awsScopeTagVerifier interface {
	Verify(context.Context, awsScopeTagVerificationRequest) (awsScopeTagVerificationReport, error)
}

var newAWSScopeTagVerifier = func(ctx context.Context, profile, region string) (awsScopeTagVerifier, error) {
	return newAWSSDKScopeTagVerifier(ctx, profile, region)
}

func awsScopesTagsVerifyCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "verify",
		Short: "Verify live AWS tags before single-cluster scope lockdown",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			enable, err := cmd.Flags().GetBool("enable")
			if err != nil {
				return fmt.Errorf("unable to get enable: %w", err)
			}
			operationName := "read-only scopes tags verify"
			if enable {
				operationName = "configuration-only scopes tags verify --enable"
			}
			if err := rejectAWSScopesConfigurationOnlyTerraformFlags(cmd.Flags(), operationName); err != nil {
				return err
			}
			scopesFilePath, err := requiredAWSScopesTagsVerifyFlag(cmd, "scopes-file")
			if err != nil {
				return err
			}
			scopeRef, err := requiredAWSScopesTagsVerifyFlag(cmd, "scope-ref")
			if err != nil {
				return err
			}
			if !awsScopeReferencePattern.MatchString(scopeRef) {
				return fmt.Errorf("--scope-ref must be an exact generated Dittocloud scope reference")
			}

			statePath := cmd.Flag("state").Value.String()
			operationLock, err := acquireStateOperationLock(statePath, "bootstrap aws scopes tags verify")
			if err != nil {
				return err
			}
			defer func() { runErr = errors.Join(runErr, operationLock.Release()) }()

			fileLock, err := acquireScopesFileLock(scopesFilePath, "bootstrap aws scopes tags verify")
			if err != nil {
				return err
			}
			defer func() { runErr = errors.Join(runErr, fileLock.Release()) }()

			document, err := loadAWSDeploymentScopesDocument(fileLock.canonicalPath)
			if err != nil {
				return err
			}
			scopes := document.scopes
			if err := validateAWSStateScopeLifecycle(operationLock.canonicalStatePath, scopes, nil); err != nil {
				return err
			}
			scope, exists := scopes[scopeRef]
			if !exists {
				return fmt.Errorf("AWS scopes file %q does not contain scope %q", fileLock.canonicalPath, scopeRef)
			}
			requestedClusterName, err := cmd.Flags().GetString("cluster-name")
			if err != nil {
				return fmt.Errorf("unable to get cluster-name: %w", err)
			}
			requestedClusterName = strings.TrimSpace(requestedClusterName)
			clusterName := scope.ClusterName
			if clusterName == "" {
				clusterName = requestedClusterName
			} else if requestedClusterName != "" && requestedClusterName != clusterName {
				return fmt.Errorf(
					"--cluster-name %q does not match scope %q clusterName %q",
					requestedClusterName,
					scopeRef,
					scope.ClusterName,
				)
			}
			if clusterName == "" {
				return fmt.Errorf(
					"scope %q has no clusterName; version 0 supports shared multi-cluster operation, but tag verification for version 1 requires --cluster-name with one exact cluster name",
					scopeRef,
				)
			}
			verificationScope := scope
			verificationScope.ClusterName = clusterName
			if enable && scope.ScopeTagPolicyVersion != 0 {
				return fmt.Errorf("scope %q already uses scopeTagPolicyVersion %d; --enable requires version 0", scopeRef, scope.ScopeTagPolicyVersion)
			}
			profile, err := cmd.Flags().GetString("aws-profile")
			if err != nil {
				return fmt.Errorf("unable to get aws-profile: %w", err)
			}
			report, err := verifyAWSScopeTagPolicyReadiness(
				cmd.Context(),
				operationLock.canonicalStatePath,
				profile,
				scopeRef,
				verificationScope,
				scope.ScopeTagPolicyVersion,
				false,
			)
			if err != nil {
				return err
			}
			if enable {
				updatedScope := scope
				updatedScope.ClusterName = clusterName
				updatedScope.ScopeTagPolicyVersion = 1
				encoded, err := document.enableScopeTagPolicyVersionOne(scopeRef, updatedScope)
				if err != nil {
					return err
				}
				if err := persistAWSDeploymentScopesFile(fileLock.canonicalPath, encoded, document.permissions); err != nil {
					return err
				}
			}
			return writeAWSScopesTagsVerificationReport(cmd.OutOrStdout(), report, enable, fileLock.canonicalPath)
		},
	}
	cmd.Flags().String("scopes-file", "", "Path to the AWS deployment scopes YAML file")
	cmd.Flags().String("scope-ref", "", "Exact AWS deployment scope reference to verify")
	cmd.Flags().String("cluster-name", "", "Exact single cluster name to assess when the version-0 scope does not persist one")
	cmd.Flags().String("aws-profile", "", "AWS profile to use for read-only tag queries")
	cmd.Flags().Bool("enable", false, "After successful verification, atomically set this scope to single-cluster policy version 1")
	return cmd
}

func requiredAWSScopesTagsVerifyFlag(cmd *cobra.Command, name string) (string, error) {
	value, err := cmd.Flags().GetString(name)
	if err != nil {
		return "", fmt.Errorf("unable to get %s: %w", name, err)
	}
	value = strings.TrimSpace(value)
	if value == "" {
		return "", fmt.Errorf("--%s is required", name)
	}
	return value, nil
}

func writeAWSScopesTagsVerificationReport(writer io.Writer, report awsScopeTagVerificationReport, enabled bool, scopesFilePath string) error {
	if _, err := fmt.Fprintf(
		writer,
		"AWS scope tag verification passed for %q: account=%s region=%s cluster=%s clusterType=%s appliedPolicyVersion=%d\n",
		report.ScopeRef,
		report.AccountID,
		report.Region,
		report.ClusterName,
		report.ClusterType,
		report.AppliedPolicyVersion,
	); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "  Dittocloud-managed resources: %d\n", len(report.StateResources)); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(writer, "  Cluster-native resources: %d\n", len(report.ClusterResources)); err != nil {
		return err
	}
	if len(report.NativeDiscoveryKeys) > 0 {
		keys := append([]string(nil), report.NativeDiscoveryKeys...)
		sort.Strings(keys)
		if _, err := fmt.Fprintf(writer, "  Verified native ownership keys: %s\n", strings.Join(keys, ", ")); err != nil {
			return err
		}
	}
	if len(report.ExplicitExclusions) > 0 {
		exclusions := append([]string(nil), report.ExplicitExclusions...)
		sort.Strings(exclusions)
		if _, err := fmt.Fprintln(writer, "  Explicit exclusions:"); err != nil {
			return err
		}
		for _, exclusion := range exclusions {
			if _, err := fmt.Fprintf(writer, "    - %s\n", exclusion); err != nil {
				return err
			}
		}
	}
	if !enabled {
		_, err := fmt.Fprintln(writer, "No Terraform lifecycle or AWS mutation was run. This report does not enable version 1.")
		return err
	}
	_, err := fmt.Fprintf(
		writer,
		"Enabled configuration for version 1 in %q: scope=%s clusterName=%s scopeTagPolicyVersion=1\nNo Terraform lifecycle or AWS mutation was run. Run normal scope mode to repeat verification and apply the IAM transition.\n",
		scopesFilePath,
		report.ScopeRef,
		report.ClusterName,
	)
	return err
}
