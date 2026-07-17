package bootstrap

import (
	"errors"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"

	"github.com/spf13/cobra"
)

const awsScopeConfigurationSchemaVersion = 1

func awsScopesRecoverCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "recover",
		Short: "Recover the last applied AWS deployment scopes file from Terraform state",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) (runErr error) {
			if err := rejectAWSScopesConfigurationOnlyTerraformFlags(cmd.Flags(), "review-only scopes recover"); err != nil {
				return err
			}
			scopesFilePath, err := cmd.Flags().GetString("scopes-file")
			if err != nil {
				return fmt.Errorf("unable to get scopes-file: %w", err)
			}
			if strings.TrimSpace(scopesFilePath) == "" {
				return fmt.Errorf("--scopes-file is required")
			}

			statePath := cmd.Flag("state").Value.String()
			operationLock, err := acquireStateOperationLock(statePath, "bootstrap aws scopes recover")
			if err != nil {
				return err
			}
			defer func() {
				runErr = errors.Join(runErr, operationLock.Release())
			}()

			fileLock, err := acquireScopesFileLock(scopesFilePath, "bootstrap aws scopes recover")
			if err != nil {
				return err
			}
			defer func() {
				runErr = errors.Join(runErr, fileLock.Release())
			}()

			if err := requireEmptyAWSScopesRecoveryDestination(fileLock.canonicalPath); err != nil {
				return err
			}
			scopes, err := recoverAWSDeploymentScopesFromState(operationLock.canonicalStatePath)
			if err != nil {
				return err
			}
			encoded, err := encodeAWSDeploymentScopesDocument(fileLock.canonicalPath, scopes)
			if err != nil {
				return err
			}
			if err := persistAWSDeploymentScopesFile(fileLock.canonicalPath, encoded, 0600); err != nil {
				return err
			}
			return writeAWSScopesRecoverySummary(
				cmd.OutOrStdout(),
				operationLock.canonicalStatePath,
				fileLock.canonicalPath,
				scopes,
			)
		},
	}
	cmd.Flags().String("scopes-file", "", "Path for the recovered AWS deployment scopes YAML file")
	return cmd
}

func requireEmptyAWSScopesRecoveryDestination(path string) error {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("unable to inspect AWS scopes recovery destination %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return fmt.Errorf("AWS scopes recovery destination %q must be a regular file", path)
	}
	if info.Size() != 0 {
		return fmt.Errorf("refusing to overwrite non-empty AWS scopes recovery destination %q", path)
	}
	return nil
}

func recoverAWSDeploymentScopesFromState(statePath string) (AWSDeploymentScopes, error) {
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return nil, err
	}
	if registry.StateEmpty {
		return nil, fmt.Errorf("Terraform state %q is empty; exact AWS scopes recovery requires a registry-backed applied state", statePath)
	}
	if !registry.Present {
		return nil, fmt.Errorf("Terraform state %q has no scope registry; exact AWS scopes recovery is unavailable", statePath)
	}
	if !registry.ConfigurationPresent {
		return nil, fmt.Errorf(
			"Terraform state %q has no scope configuration snapshots; exact AWS scopes recovery is unavailable and manual recovery is required",
			statePath,
		)
	}

	missingScopeRefs := make([]string, 0)
	for scopeRef := range registry.Scopes {
		if _, exists := registry.Configurations[scopeRef]; !exists {
			missingScopeRefs = append(missingScopeRefs, scopeRef)
		}
	}
	sort.Strings(missingScopeRefs)
	if len(missingScopeRefs) > 0 || len(registry.Configurations) != len(registry.Scopes) {
		return nil, fmt.Errorf(
			"Terraform state %q does not contain exactly one configuration snapshot for every registry-backed scope; missing snapshots: [%s]",
			statePath,
			strings.Join(missingScopeRefs, ", "),
		)
	}

	recovered := make(AWSDeploymentScopes, len(registry.Scopes))
	for _, scopeRef := range sortedAWSStateScopeRefs(registry.Scopes) {
		identity := registry.Scopes[scopeRef]
		configuration := registry.Configurations[scopeRef]
		if configuration.Default != identity.Default {
			return nil, fmt.Errorf(
				"Terraform state %q configuration snapshot for scope %q has default=%t but the identity registry has default=%t",
				statePath,
				scopeRef,
				configuration.Default,
				identity.Default,
			)
		}
		appliedPolicyVersion := registry.AppliedTagPolicyVersions[scopeRef]
		if configuration.ScopeTagPolicyVersion != appliedPolicyVersion {
			return nil, fmt.Errorf(
				"Terraform state %q configuration snapshot for scope %q has tag policy version %d but the applied marker has version %d",
				statePath,
				scopeRef,
				configuration.ScopeTagPolicyVersion,
				appliedPolicyVersion,
			)
		}
		recovered[scopeRef] = configuration
	}
	if err := recovered.Validate(); err != nil {
		return nil, fmt.Errorf("Terraform state %q contains an invalid recovered AWS scopes configuration: %w", statePath, err)
	}
	return recovered, nil
}

func sortedAWSStateScopeRefs(scopes map[string]awsStateScopeIdentity) []string {
	refs := make([]string, 0, len(scopes))
	for scopeRef := range scopes {
		refs = append(refs, scopeRef)
	}
	sort.Strings(refs)
	return refs
}

func writeAWSScopesRecoverySummary(writer io.Writer, statePath, scopesPath string, scopes AWSDeploymentScopes) error {
	if _, err := fmt.Fprintf(writer, "Recovered the last applied AWS deployment scopes (%d) from %q:\n", len(scopes), statePath); err != nil {
		return err
	}
	for _, scopeRef := range sortedAWSDeploymentScopeRefs(scopes) {
		scope := scopes[scopeRef]
		defaultMarker := ""
		if scope.Default {
			defaultMarker = " [default]"
		}
		if _, err := fmt.Fprintf(
			writer,
			"  - %s%s: region=%s clusterType=%s vpcMode=%s scopeTagPolicyVersion=%d\n",
			scopeRef,
			defaultMarker,
			scope.Region,
			scope.ClusterType,
			scope.VPC.Mode,
			scope.ScopeTagPolicyVersion,
		); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintf(
		writer,
		"Wrote %q with mode 0600. Review it before a separate bootstrap run; Terraform state was not changed.\n",
		scopesPath,
	)
	return err
}
