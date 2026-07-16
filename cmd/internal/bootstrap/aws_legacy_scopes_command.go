package bootstrap

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

var (
	awsLegacyScopeInputIsInteractive = func() bool {
		return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	}
	awsLegacyScopeConfirmGeneration = func() bool {
		for {
			switch strings.ToLower(StringPrompt("Generate a draft scopes YAML from the legacy Terraform state? (y/n)", "")) {
			case "y", "yes":
				return true
			case "n", "no":
				return false
			}
		}
	}
	awsLegacyScopePromptString = StringPrompt
	awsLegacyScopePromptOption = OptionsPrompt
)

type awsLegacyScopeSummaryLine struct {
	field   string
	value   string
	sources []string
}

func maybeGenerateAWSLegacyScopesFile(cmd *cobra.Command) (handled bool, runErr error) {
	generateRequested, err := cmd.Flags().GetBool("generate-scopes-file")
	if err != nil {
		return true, fmt.Errorf("unable to get generate-scopes-file: %w", err)
	}
	scopeMode, err := cmd.Flags().GetBool("scopes")
	if err != nil {
		return true, fmt.Errorf("unable to get scopes: %w", err)
	}
	if generateRequested && !scopeMode {
		return true, fmt.Errorf("--generate-scopes-file requires --scopes=true")
	}
	if !scopeMode {
		return false, nil
	}

	scopesFilePath, err := cmd.Flags().GetString("scopes-file")
	if err != nil {
		return true, fmt.Errorf("unable to get scopes-file: %w", err)
	}
	if strings.TrimSpace(scopesFilePath) == "" {
		if generateRequested {
			return true, fmt.Errorf("--scopes-file is required with --generate-scopes-file")
		}
		return false, nil
	}

	destinationEmpty, err := awsLegacyScopesDestinationIsEmpty(scopesFilePath)
	if err != nil {
		return true, err
	}
	if !destinationEmpty {
		if generateRequested {
			return true, fmt.Errorf("--generate-scopes-file refuses to overwrite non-empty scopes file %q", scopesFilePath)
		}
		return false, nil
	}

	registry, err := loadAWSStateScopeRegistry(commandCanonicalStatePath(cmd))
	if err != nil {
		return true, err
	}
	if registry.Present {
		return true, fmt.Errorf(
			"Terraform state %q already contains a scope registry but scopes file %q is missing or empty; manual recovery is required",
			commandCanonicalStatePath(cmd),
			scopesFilePath,
		)
	}
	if registry.StateEmpty {
		if generateRequested {
			return true, fmt.Errorf("--generate-scopes-file requires an existing legacy Terraform state")
		}
		return false, nil
	}

	if err := validateAWSLegacyScopesGenerationFlags(cmd.Flags()); err != nil {
		return true, err
	}
	if !generateRequested {
		if !awsLegacyScopeInputIsInteractive() {
			return true, fmt.Errorf(
				"legacy Terraform state %q detected with no usable scopes file; rerun with --generate-scopes-file to create a review-only draft",
				commandCanonicalStatePath(cmd),
			)
		}
		if !awsLegacyScopeConfirmGeneration() {
			skipCommandTerraformLifecycle(cmd)
			_, err := fmt.Fprintln(cmd.OutOrStdout(), "Legacy scopes YAML generation declined; no file was written and Terraform was not run.")
			return true, err
		}
	}

	fileLock, err := acquireScopesFileLock(scopesFilePath, "bootstrap aws generate legacy scopes file")
	if err != nil {
		return true, err
	}
	defer func() {
		if fileLock != nil {
			runErr = errors.Join(runErr, fileLock.Release())
		}
	}()
	canonicalScopesFilePath := fileLock.canonicalPath

	destinationEmpty, err = awsLegacyScopesDestinationIsEmpty(fileLock.canonicalPath)
	if err != nil {
		return true, err
	}
	if !destinationEmpty {
		return true, fmt.Errorf("legacy scopes-file generation refuses to overwrite non-empty scopes file %q", fileLock.canonicalPath)
	}

	state, exists, err := loadRawTerraformState(commandCanonicalStatePath(cmd))
	if err != nil {
		return true, err
	}
	if !exists || len(state.Resources) == 0 && len(state.Outputs) == 0 {
		return true, fmt.Errorf("legacy Terraform state %q is no longer available", commandCanonicalStatePath(cmd))
	}
	discovery, err := discoverAWSLegacyScope(state)
	if err != nil {
		return true, err
	}
	if len(discovery.Missing) > 0 && !awsLegacyScopeInputIsInteractive() {
		return true, fmt.Errorf(
			"legacy state does not contain enough unambiguous evidence to generate a default scope; missing required fields: %s; no scopes file was written",
			strings.Join(discovery.Missing, ", "),
		)
	}
	if len(discovery.Missing) > 0 {
		discovery, err = completeAWSLegacyScopeDiscovery(discovery)
		if err != nil {
			return true, err
		}
	}
	if err := validateAWSDeploymentScopeFields("generated legacy default", discovery.Scope); err != nil {
		return true, err
	}

	scopeRef, err := generateUnusedAWSDeploymentScopeReference(nil)
	if err != nil {
		return true, err
	}
	document, err := loadAWSDeploymentScopesDocument(fileLock.canonicalPath)
	if err != nil {
		return true, err
	}
	if !document.empty {
		return true, fmt.Errorf("legacy scopes-file generation refuses to overwrite non-empty scopes file %q", fileLock.canonicalPath)
	}
	encodedDocument, err := document.appendScope(scopeRef, discovery.Scope)
	if err != nil {
		return true, err
	}
	if err := persistAWSDeploymentScopesFile(fileLock.canonicalPath, encodedDocument, document.permissions); err != nil {
		return true, err
	}
	if err := fileLock.Release(); err != nil {
		return true, fmt.Errorf("unable to release Dittocloud scopes-file lock: %w", err)
	}
	fileLock = nil

	skipCommandTerraformLifecycle(cmd)
	if err := printAWSLegacyScopeEvidence(cmd, canonicalScopesFilePath, scopeRef, discovery); err != nil {
		return true, err
	}
	return true, nil
}

func validateAWSLegacyScopesGenerationFlags(flags *pflag.FlagSet) error {
	allowed := map[string]struct{}{
		"generate-scopes-file": {},
		"log-level":            {},
		"no-color":             {},
		"scopes":               {},
		"scopes-file":          {},
		"state":                {},
	}
	var unsupported []string
	flags.Visit(func(flag *pflag.Flag) {
		if _, accepted := allowed[flag.Name]; !accepted {
			unsupported = append(unsupported, "--"+flag.Name)
		}
	})
	if len(unsupported) == 0 {
		return nil
	}
	sort.Strings(unsupported)
	return fmt.Errorf("%s cannot be used with review-only legacy scopes-file generation", strings.Join(unsupported, ", "))
}

func awsLegacyScopesDestinationIsEmpty(path string) (bool, error) {
	info, err := os.Stat(path)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("unable to inspect AWS scopes-file destination %q: %w", path, err)
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("AWS scopes-file destination %q must be a regular file", path)
	}
	return info.Size() == 0, nil
}

func completeAWSLegacyScopeDiscovery(discovery awsLegacyScopeDiscovery) (awsLegacyScopeDiscovery, error) {
	setPrompted := func(field, value string) {
		value = strings.TrimSpace(value)
		if value != "" {
			discovery.Evidence[field] = awsLegacyScopeFieldEvidence{Value: value, Sources: []string{"interactive confirmation"}}
		}
	}

	if discovery.Scope.Region == "" {
		discovery.Scope.Region = awsLegacyScopePromptString("Enter the legacy deployment AWS Region", "")
		setPrompted(legacyScopeFieldRegion, discovery.Scope.Region)
	}
	if discovery.Scope.VPC.Mode == "" {
		discovery.Scope.VPC.Mode = awsLegacyScopePromptOption(
			"Select the legacy deployment VPC mode",
			[]string{awsVPCModeDittocloud, awsVPCModeExisting, awsVPCModeCAPI},
		)
		setPrompted(legacyScopeFieldVPCMode, discovery.Scope.VPC.Mode)
	}
	switch discovery.Scope.VPC.Mode {
	case awsVPCModeDittocloud:
		if discovery.Scope.VPC.Name == "" {
			discovery.Scope.VPC.Name = awsLegacyScopePromptString("Enter the Terraform-managed VPC name", "")
			setPrompted(legacyScopeFieldVPCName, discovery.Scope.VPC.Name)
		}
		if discovery.Scope.VPC.CIDR == "" {
			discovery.Scope.VPC.CIDR = awsLegacyScopePromptString("Enter the Terraform-managed VPC CIDR", "")
			setPrompted(legacyScopeFieldVPCCIDR, discovery.Scope.VPC.CIDR)
		}
		discovery.Scope.VPC.ID = ""
	case awsVPCModeExisting:
		if discovery.Scope.VPC.ID == "" {
			discovery.Scope.VPC.ID = awsLegacyScopePromptString("Enter the existing VPC ID", "")
			setPrompted(legacyScopeFieldVPCID, discovery.Scope.VPC.ID)
		}
	}
	if discovery.Scope.ClusterType == "" {
		confirmedType := strings.TrimSpace(awsLegacyScopePromptString(
			"No EKS-specific state evidence was found; enter kubeadm to confirm the legacy cluster type",
			"",
		))
		if confirmedType != awsClusterTypeKubeadm {
			return discovery, fmt.Errorf("clusterType remains unresolved; only explicit kubeadm confirmation is accepted when EKS state evidence is absent")
		}
		discovery.Scope.ClusterType = confirmedType
		setPrompted(legacyScopeFieldClusterType, discovery.Scope.ClusterType)
	}

	discovery.Missing = missingAWSLegacyScopeFields(discovery.Scope)
	if len(discovery.Missing) > 0 {
		return discovery, fmt.Errorf(
			"required legacy scope fields remain unresolved: %s; no scopes file was written",
			strings.Join(discovery.Missing, ", "),
		)
	}
	return discovery, nil
}

func printAWSLegacyScopeEvidence(
	cmd *cobra.Command,
	scopesFilePath string,
	scopeRef string,
	discovery awsLegacyScopeDiscovery,
) error {
	output := cmd.OutOrStdout()
	if _, err := fmt.Fprintf(output, "Generated legacy default-scope draft at %q.\n", scopesFilePath); err != nil {
		return err
	}
	lines := []awsLegacyScopeSummaryLine{
		{field: "scopeRef", value: scopeRef, sources: []string{"generated persistent reference"}},
		{field: "default", value: "true", sources: []string{"legacy migration contract"}},
		{field: legacyScopeFieldRegion, value: discovery.Scope.Region, sources: discovery.Evidence[legacyScopeFieldRegion].Sources},
		{field: legacyScopeFieldVPCMode, value: discovery.Scope.VPC.Mode, sources: discovery.Evidence[legacyScopeFieldVPCMode].Sources},
	}
	switch discovery.Scope.VPC.Mode {
	case awsVPCModeDittocloud:
		lines = append(lines,
			awsLegacyScopeSummaryLine{legacyScopeFieldVPCName, discovery.Scope.VPC.Name, discovery.Evidence[legacyScopeFieldVPCName].Sources},
			awsLegacyScopeSummaryLine{legacyScopeFieldVPCCIDR, discovery.Scope.VPC.CIDR, discovery.Evidence[legacyScopeFieldVPCCIDR].Sources},
		)
	case awsVPCModeExisting:
		lines = append(lines, awsLegacyScopeSummaryLine{legacyScopeFieldVPCID, discovery.Scope.VPC.ID, discovery.Evidence[legacyScopeFieldVPCID].Sources})
	case awsVPCModeCAPI:
		if discovery.Scope.VPC.ID != "" {
			lines = append(lines, awsLegacyScopeSummaryLine{legacyScopeFieldVPCID, discovery.Scope.VPC.ID, discovery.Evidence[legacyScopeFieldVPCID].Sources})
		}
	}
	lines = append(lines, awsLegacyScopeSummaryLine{legacyScopeFieldClusterType, discovery.Scope.ClusterType, discovery.Evidence[legacyScopeFieldClusterType].Sources})
	if discovery.Scope.ClusterName != "" {
		lines = append(lines, awsLegacyScopeSummaryLine{legacyScopeFieldClusterName, discovery.Scope.ClusterName, discovery.Evidence[legacyScopeFieldClusterName].Sources})
	}
	lines = append(lines, awsLegacyScopeSummaryLine{"scopeTagPolicyVersion", "0", []string{"legacy migration contract"}})
	for _, line := range lines {
		if _, err := fmt.Fprintf(output, "  %s: %s (%s)\n", line.field, line.value, strings.Join(line.sources, ", ")); err != nil {
			return err
		}
	}
	_, err := fmt.Fprintln(output, "Review this draft before a separate migration invocation. Terraform state was not modified and Terraform was not run.")
	return err
}
