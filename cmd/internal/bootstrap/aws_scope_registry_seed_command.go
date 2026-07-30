package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"

	"github.com/getditto/dittocloud/terraform"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/mattn/go-isatty"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const terraformBuiltinProviderName = "terraform.io/builtin/terraform"

type awsScopeRegistrySeedPreflight struct {
	ScopeRef       string
	TargetAddress  string
	EncodedScopes  string
	ScopesContent  []byte
	OriginalState  []byte
	TerraformState rawTerraformState
}

var (
	awsScopeRegistrySeedInputIsInteractive = func() bool {
		return isatty.IsTerminal(os.Stdin.Fd()) || isatty.IsCygwinTerminal(os.Stdin.Fd())
	}
	awsScopeRegistrySeedConfirm = func() bool {
		for {
			switch strings.ToLower(StringPrompt("Apply the reviewed default-scope registry seed? (y/n)", "")) {
			case "y", "yes":
				return true
			case "n", "no":
				return false
			}
		}
	}
)

func awsScopesMigrateCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Run guarded AWS scope migration operations",
	}
	cmd.AddCommand(awsScopesSeedRegistryCmd())
	return cmd
}

func awsScopesSeedRegistryCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "seed-registry",
		Short: "Seed the default scope registry in an existing legacy state",
		Args:  cobra.NoArgs,
		RunE:  runAWSScopesSeedRegistry,
	}
	cmd.Flags().String("scopes-file", "", "Path to the reviewed default-only AWS deployment scopes YAML file")
	return cmd
}

func runAWSScopesSeedRegistry(cmd *cobra.Command, args []string) (runErr error) {
	if err := validateAWSRegistrySeedFlags(cmd.Flags()); err != nil {
		return err
	}
	scopesFilePath, err := cmd.Flags().GetString("scopes-file")
	if err != nil {
		return fmt.Errorf("unable to get scopes-file: %w", err)
	}
	if strings.TrimSpace(scopesFilePath) == "" {
		return fmt.Errorf("--scopes-file is required")
	}

	operationLock, err := acquireStateOperationLock(cmd.Flag("state").Value.String(), "bootstrap aws scopes migrate seed-registry")
	if err != nil {
		return err
	}
	defer func() {
		if operationLock != nil {
			if err := operationLock.Release(); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("unable to release Dittocloud operation lock: %w", err))
			}
		}
	}()

	fileLock, err := acquireScopesFileLock(scopesFilePath, "bootstrap aws scopes migrate seed-registry")
	if err != nil {
		return err
	}
	defer func() {
		if fileLock != nil {
			if err := fileLock.Release(); err != nil {
				runErr = errors.Join(runErr, fmt.Errorf("unable to release Dittocloud scopes-file lock: %w", err))
			}
		}
	}()

	preflight, err := preflightAWSRegistrySeed(operationLock.canonicalStatePath, fileLock.canonicalPath)
	if err != nil {
		return err
	}

	temporaryDirectory, err := os.MkdirTemp(os.TempDir(), "dittocloud-registry-seed-")
	if err != nil {
		return fmt.Errorf("unable to create registry-seed temporary directory: %w", err)
	}
	removeTemporaryDirectory, err := cmd.Flags().GetBool("remove-tmpdir")
	if err != nil {
		return fmt.Errorf("unable to get remove-tmpdir: %w", err)
	}
	retainTemporaryDirectory := !removeTemporaryDirectory
	defer func() {
		if !retainTemporaryDirectory {
			_ = os.RemoveAll(temporaryDirectory)
		}
	}()

	if err := os.CopyFS(temporaryDirectory, terraform.TerraformFiles); err != nil {
		return fmt.Errorf("unable to copy Terraform files for registry seed: %w", err)
	}
	if err := os.Chmod(temporaryDirectory, 0700); err != nil {
		return fmt.Errorf("unable to secure registry-seed temporary directory: %w", err)
	}
	workingDirectory := filepath.Join(temporaryDirectory, "aws")
	temporaryStatePath := filepath.Join(workingDirectory, "terraform.tfstate")
	if err := os.WriteFile(temporaryStatePath, preflight.OriginalState, 0600); err != nil {
		return fmt.Errorf("unable to copy legacy Terraform state for registry seed: %w", err)
	}

	forceDownload, err := cmd.Flags().GetBool("force-terraform-download")
	if err != nil {
		return fmt.Errorf("unable to get force-terraform-download: %w", err)
	}
	terraformPath, err := GetTerraform(cmd.Context(), forceDownload)
	if err != nil {
		return fmt.Errorf("terraform executable not available: %w", err)
	}
	tf, err := terraformFactory(workingDirectory, terraformPath)
	if err != nil {
		return fmt.Errorf("unable to create Terraform executor for registry seed: %w", err)
	}
	tf.SetStdout(cmd.OutOrStdout())
	tf.SetStderr(cmd.ErrOrStderr())
	if err := tf.Init(cmd.Context(), tfexec.Upgrade(true)); err != nil {
		return fmt.Errorf("unable to initialize Terraform for registry seed: %w", err)
	}

	planPath := filepath.Join(workingDirectory, "scope-registry-seed.tfplan")
	planChanged, err := tf.Plan(
		cmd.Context(),
		tfexec.Var("deployment_scopes="+preflight.EncodedScopes),
		tfexec.Target(preflight.TargetAddress),
		tfexec.Out(planPath),
	)
	if err != nil {
		return fmt.Errorf("unable to create default-scope registry-seed plan: %w", err)
	}
	if !planChanged {
		return fmt.Errorf("default-scope registry-seed plan contains no change; rerun preflight against the selected state")
	}
	// terraform-exec mirrors `terraform show -json` to its configured stdout
	// while also decoding it. Keep that internal validation payload out of the
	// user-facing command output, then restore normal command output.
	tf.SetStdout(io.Discard)
	plan, err := tf.ShowPlanFile(cmd.Context(), planPath)
	tf.SetStdout(cmd.OutOrStdout())
	if err != nil {
		return fmt.Errorf("unable to inspect default-scope registry-seed plan: %w", err)
	}
	if err := validateAWSRegistrySeedPlan(plan, preflight.TargetAddress, preflight.ScopeRef); err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Validated registry-seed plan: create %s and no other resource action.\n",
		preflight.TargetAddress,
	); err != nil {
		return err
	}

	dryRun, err := cmd.Flags().GetBool("dry-run")
	if err != nil {
		return fmt.Errorf("unable to get dry-run: %w", err)
	}
	if dryRun {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "Registry-seed dry run complete. No backup was created and Terraform state was not changed.")
		return err
	}
	if !awsScopeRegistrySeedInputIsInteractive() {
		return fmt.Errorf("registry seeding requires an interactive confirmation; use --dry-run for non-interactive validation")
	}
	if !awsScopeRegistrySeedConfirm() {
		_, err := fmt.Fprintln(cmd.OutOrStdout(), "Registry seeding cancelled. No backup was created and Terraform state was not changed.")
		return err
	}
	currentScopesContent, err := os.ReadFile(fileLock.canonicalPath)
	if err != nil {
		return fmt.Errorf("unable to re-read reviewed scopes file before registry seed: %w", err)
	}
	if !bytes.Equal(currentScopesContent, preflight.ScopesContent) {
		return fmt.Errorf("AWS scopes file %q changed after migration preflight; rerun the command", fileLock.canonicalPath)
	}

	backup, err := createTerraformMigrationBackup(
		operationLock.canonicalStatePath,
		preflight.OriginalState,
		preflight.TerraformState,
		preflight.ScopeRef,
		preflight.TargetAddress,
		preflight.ScopesContent,
	)
	if err != nil {
		return err
	}
	if _, err := fmt.Fprintf(
		cmd.OutOrStdout(),
		"Created pre-migration state backup %q and manifest %q.\n",
		backup.StatePath,
		backup.ManifestPath,
	); err != nil {
		return err
	}

	// Applying the one-resource saved plan otherwise prints every unchanged
	// Terraform output, including the full legacy AWS output. The command emits
	// its own concise backup and completion messages around this guarded apply.
	tf.SetStdout(io.Discard)
	applyErr := tf.Apply(cmd.Context(), tfexec.DirOrPlan(planPath))
	tf.SetStdout(cmd.OutOrStdout())
	if applyErr != nil {
		retainTemporaryDirectory = true
		return persistFailedAWSRegistrySeedApply(
			applyErr,
			temporaryStatePath,
			operationLock.canonicalStatePath,
			preflight.OriginalState,
			preflight.ScopeRef,
		)
	}
	if err := validateAppliedAWSRegistrySeedState(temporaryStatePath, preflight.ScopeRef, preflight.OriginalState); err != nil {
		retainTemporaryDirectory = true
		return fmt.Errorf("registry seed applied in the temporary directory but produced invalid state: %w; temporary files retained at %q", err, temporaryDirectory)
	}
	if err := persistTerraformState(temporaryStatePath, operationLock.canonicalStatePath); err != nil {
		retainTemporaryDirectory = true
		return err
	}
	if err := validateAppliedAWSRegistrySeedState(operationLock.canonicalStatePath, preflight.ScopeRef, preflight.OriginalState); err != nil {
		retainTemporaryDirectory = true
		return fmt.Errorf("persisted registry-seed state failed validation: %w; state backup remains at %q", err, backup.StatePath)
	}

	_, err = fmt.Fprintf(
		cmd.OutOrStdout(),
		"Seeded immutable default scope %q in %q. Review a separate untargeted scope-mode plan before adding another scope.\n",
		preflight.ScopeRef,
		operationLock.canonicalStatePath,
	)
	return err
}

func validateAWSRegistrySeedFlags(flags *pflag.FlagSet) error {
	for _, flagName := range []string{"import-resource", "tf-var"} {
		if flags.Changed(flagName) {
			return fmt.Errorf("--%s cannot be used with the dedicated registry-seed migration", flagName)
		}
	}
	return nil
}

func preflightAWSRegistrySeed(statePath, scopesFilePath string) (awsScopeRegistrySeedPreflight, error) {
	document, err := loadAWSDeploymentScopesDocument(scopesFilePath)
	if err != nil {
		return awsScopeRegistrySeedPreflight{}, err
	}
	if document.empty || len(document.scopes) != 1 {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("registry seeding requires a reviewed scopes file containing exactly one default scope")
	}
	var scopeRef string
	var scope AWSDeploymentScope
	for candidateRef, candidateScope := range document.scopes {
		scopeRef = candidateRef
		scope = candidateScope
	}
	if !scope.Default {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("registry seeding requires the only configured scope %q to set default: true", scopeRef)
	}
	if scope.ScopeTagPolicyVersion != 0 {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("legacy default scope %q must use scopeTagPolicyVersion: 0 before registry seeding", scopeRef)
	}

	originalState, err := os.ReadFile(statePath)
	if err != nil {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("unable to read legacy Terraform state %q: %w", statePath, err)
	}
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return awsScopeRegistrySeedPreflight{}, err
	}
	if registry.Present {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("terraform state %q already contains a scope registry; registry seeding is a one-time legacy migration", statePath)
	}
	if registry.StateEmpty {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("registry seeding requires an existing non-empty legacy Terraform state")
	}
	state, exists, err := loadRawTerraformState(statePath)
	if err != nil {
		return awsScopeRegistrySeedPreflight{}, err
	}
	if !exists {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("legacy Terraform state %q does not exist", statePath)
	}
	stateAfterDecode, err := os.ReadFile(statePath)
	if err != nil {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("unable to re-read legacy Terraform state %q: %w", statePath, err)
	}
	if !bytes.Equal(originalState, stateAfterDecode) {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("terraform state %q changed during migration preflight; rerun the command", statePath)
	}
	discovery, err := discoverAWSLegacyScope(state)
	if err != nil {
		return awsScopeRegistrySeedPreflight{}, err
	}
	if err := validateAWSLegacyScopeCompatibility(scopeRef, scope, discovery); err != nil {
		return awsScopeRegistrySeedPreflight{}, err
	}
	encodedScopes, err := marshalAWSDeploymentScopes(document.scopes)
	if err != nil {
		return awsScopeRegistrySeedPreflight{}, err
	}
	scopesContent, err := os.ReadFile(scopesFilePath)
	if err != nil {
		return awsScopeRegistrySeedPreflight{}, fmt.Errorf("unable to read reviewed scopes file %q: %w", scopesFilePath, err)
	}
	return awsScopeRegistrySeedPreflight{
		ScopeRef:       scopeRef,
		TargetAddress:  fmt.Sprintf("terraform_data.scope_registry[%q]", scopeRef),
		EncodedScopes:  encodedScopes,
		ScopesContent:  scopesContent,
		OriginalState:  originalState,
		TerraformState: state,
	}, nil
}

func validateAWSLegacyScopeCompatibility(scopeRef string, scope AWSDeploymentScope, discovery awsLegacyScopeDiscovery) error {
	fields := []struct {
		name    string
		desired string
	}{
		{name: legacyScopeFieldRegion, desired: scope.Region},
		{name: legacyScopeFieldVPCMode, desired: scope.VPC.Mode},
		{name: legacyScopeFieldClusterType, desired: scope.ClusterType},
	}
	switch scope.VPC.Mode {
	case awsVPCModeDittocloud:
		fields = append(fields,
			struct {
				name    string
				desired string
			}{name: legacyScopeFieldVPCName, desired: scope.VPC.Name},
			struct {
				name    string
				desired string
			}{name: legacyScopeFieldVPCCIDR, desired: scope.VPC.CIDR},
		)
	case awsVPCModeExisting, awsVPCModeCAPI:
		fields = append(fields, struct {
			name    string
			desired string
		}{name: legacyScopeFieldVPCID, desired: scope.VPC.ID})
	}
	for _, field := range fields {
		evidence, exists := discovery.Evidence[field.name]
		if !exists || evidence.Value == "" {
			continue
		}
		if field.desired != evidence.Value {
			return fmt.Errorf(
				"legacy default scope %q %s %q conflicts with state evidence %q from %s",
				scopeRef,
				field.name,
				field.desired,
				evidence.Value,
				strings.Join(evidence.Sources, ", "),
			)
		}
	}
	clusterTypeEvidence := discovery.Evidence[legacyScopeFieldClusterType]
	if clusterTypeEvidence.Value == "" && scope.ClusterType != awsClusterTypeKubeadm {
		return fmt.Errorf("legacy default scope %q cannot use clusterType %q without EKS-specific state evidence", scopeRef, scope.ClusterType)
	}
	clusterNameEvidence := discovery.Evidence[legacyScopeFieldClusterName]
	if clusterNameEvidence.Value == "" {
		if scope.ClusterName != "" {
			return fmt.Errorf("legacy default scope %q must omit clusterName because state contains no unique phase-two IAM condition", scopeRef)
		}
	} else if scope.ClusterName != clusterNameEvidence.Value {
		return fmt.Errorf(
			"legacy default scope %q clusterName %q conflicts with state evidence %q from %s",
			scopeRef,
			scope.ClusterName,
			clusterNameEvidence.Value,
			strings.Join(clusterNameEvidence.Sources, ", "),
		)
	}
	return nil
}

func validateAWSRegistrySeedPlan(plan *tfjson.Plan, targetAddress, scopeRef string) error {
	if plan == nil {
		return fmt.Errorf("default-scope registry-seed plan is unavailable")
	}
	if len(plan.DeferredChanges) > 0 {
		return fmt.Errorf("default-scope registry-seed plan contains deferred resource changes")
	}
	for _, drift := range plan.ResourceDrift {
		if drift != nil && drift.Change != nil && !drift.Change.Actions.NoOp() {
			return fmt.Errorf("default-scope registry-seed plan contains resource drift at %q", drift.Address)
		}
	}
	nonNoOpChanges := make([]*tfjson.ResourceChange, 0, 1)
	for _, change := range plan.ResourceChanges {
		if change == nil || change.Change == nil {
			return fmt.Errorf("default-scope registry-seed plan contains a malformed resource change")
		}
		if !change.Change.Actions.NoOp() {
			nonNoOpChanges = append(nonNoOpChanges, change)
		}
	}
	if len(nonNoOpChanges) != 1 {
		return fmt.Errorf(
			"default-scope registry-seed plan must contain exactly one resource action; found %d: %s",
			len(nonNoOpChanges),
			strings.Join(describeTerraformResourceChanges(nonNoOpChanges), ", "),
		)
	}
	change := nonNoOpChanges[0]
	index, indexIsString := change.Index.(string)
	if change.Address != targetAddress ||
		change.PreviousAddress != "" ||
		change.ModuleAddress != "" ||
		change.Mode != tfjson.ManagedResourceMode ||
		change.Type != "terraform_data" ||
		change.Name != "scope_registry" ||
		change.ProviderName != terraformBuiltinProviderName ||
		change.DeposedKey != "" ||
		!change.Change.Actions.Create() ||
		change.Change.Importing != nil ||
		!indexIsString || index != scopeRef {
		return fmt.Errorf(
			"default-scope registry-seed plan action is not the exact built-in create for %q: %s",
			targetAddress,
			strings.Join(describeTerraformResourceChanges(nonNoOpChanges), ", "),
		)
	}
	return nil
}

func describeTerraformResourceChanges(changes []*tfjson.ResourceChange) []string {
	descriptions := make([]string, 0, len(changes))
	for _, change := range changes {
		if change == nil {
			descriptions = append(descriptions, "<nil>")
			continue
		}
		actions := []string{"<missing>"}
		if change.Change != nil {
			actions = make([]string, 0, len(change.Change.Actions))
			for _, action := range change.Change.Actions {
				actions = append(actions, string(action))
			}
		}
		descriptions = append(descriptions, fmt.Sprintf("%s [%s]", change.Address, strings.Join(actions, ",")))
	}
	sort.Strings(descriptions)
	return descriptions
}

func validateAppliedAWSRegistrySeedState(statePath, scopeRef string, originalState []byte) error {
	registry, err := loadAWSStateScopeRegistry(statePath)
	if err != nil {
		return err
	}
	if !registry.Present || len(registry.Scopes) != 1 || registry.DefaultScopeRef != scopeRef {
		return fmt.Errorf("state must contain exactly the seeded default scope %q", scopeRef)
	}
	identity := registry.Scopes[scopeRef]
	if identity.ScopeRef != scopeRef || !identity.Default {
		return fmt.Errorf("state contains an inconsistent identity for seeded default scope %q", scopeRef)
	}
	appliedState, err := os.ReadFile(statePath)
	if err != nil {
		return fmt.Errorf("unable to read registry-seed state for preservation validation: %w", err)
	}
	return validateAWSRegistrySeedStatePreservation(originalState, appliedState)
}

type terraformRegistrySeedStateEnvelope struct {
	Version   int                        `json:"version"`
	Serial    int64                      `json:"serial"`
	Lineage   string                     `json:"lineage"`
	Outputs   map[string]json.RawMessage `json:"outputs"`
	Resources []json.RawMessage          `json:"resources"`
}

func validateAWSRegistrySeedStatePreservation(originalContent, appliedContent []byte) error {
	var original terraformRegistrySeedStateEnvelope
	if err := json.Unmarshal(originalContent, &original); err != nil {
		return fmt.Errorf("unable to decode preflight state for preservation validation: %w", err)
	}
	var applied terraformRegistrySeedStateEnvelope
	if err := json.Unmarshal(appliedContent, &applied); err != nil {
		return fmt.Errorf("unable to decode applied state for preservation validation: %w", err)
	}
	if applied.Version != original.Version || applied.Lineage != original.Lineage {
		return fmt.Errorf("registry-seed state changed the Terraform state version or lineage")
	}
	if applied.Serial <= original.Serial {
		return fmt.Errorf(
			"registry-seed state serial %d did not advance beyond preflight serial %d",
			applied.Serial,
			original.Serial,
		)
	}
	originalOutputNames := sortedRawJSONMapKeys(original.Outputs)
	appliedOutputNames := sortedRawJSONMapKeys(applied.Outputs)
	if !slices.Equal(originalOutputNames, appliedOutputNames) {
		return fmt.Errorf(
			"registry-seed state changed Terraform output names: before %v, after %v",
			originalOutputNames,
			appliedOutputNames,
		)
	}
	originalResources, originalRegistryCount, err := canonicalNonRegistryResources(original.Resources)
	if err != nil {
		return fmt.Errorf("unable to inspect preflight resources for preservation validation: %w", err)
	}
	if originalRegistryCount != 0 {
		return fmt.Errorf("preflight state unexpectedly contains a scope registry resource")
	}
	appliedResources, appliedRegistryCount, err := canonicalNonRegistryResources(applied.Resources)
	if err != nil {
		return fmt.Errorf("unable to inspect applied resources for preservation validation: %w", err)
	}
	if appliedRegistryCount != 1 {
		return fmt.Errorf("registry-seed state must contain exactly one scope registry resource")
	}
	if !equalStringCounts(originalResources, appliedResources) {
		return fmt.Errorf("registry-seed state changed non-registry resources from the preflight state")
	}
	return nil
}

func sortedRawJSONMapKeys(values map[string]json.RawMessage) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func canonicalNonRegistryResources(resources []json.RawMessage) (map[string]int, int, error) {
	canonicalResources := make(map[string]int, len(resources))
	registryCount := 0
	for _, resourceContent := range resources {
		var identity struct {
			Module string `json:"module"`
			Mode   string `json:"mode"`
			Type   string `json:"type"`
			Name   string `json:"name"`
		}
		if err := json.Unmarshal(resourceContent, &identity); err != nil {
			return nil, 0, err
		}
		if identity.Module == "" && identity.Mode == "managed" && identity.Type == "terraform_data" && identity.Name == "scope_registry" {
			registryCount++
			continue
		}
		var resourceValue any
		decoder := json.NewDecoder(bytes.NewReader(resourceContent))
		decoder.UseNumber()
		if err := decoder.Decode(&resourceValue); err != nil {
			return nil, 0, err
		}
		canonicalContent, err := json.Marshal(resourceValue)
		if err != nil {
			return nil, 0, err
		}
		canonicalResources[string(canonicalContent)]++
	}
	return canonicalResources, registryCount, nil
}

func equalStringCounts(left, right map[string]int) bool {
	if len(left) != len(right) {
		return false
	}
	for value, count := range left {
		if right[value] != count {
			return false
		}
	}
	return true
}

func persistFailedAWSRegistrySeedApply(
	applyErr error,
	temporaryStatePath string,
	localStatePath string,
	originalState []byte,
	scopeRef string,
) error {
	temporaryState, readErr := os.ReadFile(temporaryStatePath)
	if readErr != nil {
		return errors.Join(
			fmt.Errorf("default-scope registry-seed apply failed: %w", applyErr),
			fmt.Errorf("unable to inspect partial Terraform state: %w", readErr),
		)
	}
	if bytes.Equal(temporaryState, originalState) {
		return fmt.Errorf("default-scope registry-seed apply failed without changing state: %w", applyErr)
	}
	if err := validateAppliedAWSRegistrySeedState(temporaryStatePath, scopeRef, originalState); err != nil {
		return errors.Join(
			fmt.Errorf("default-scope registry-seed apply failed: %w", applyErr),
			fmt.Errorf("partial Terraform state is not a valid registry seed and was not persisted: %w; temporary state retained at %q", err, temporaryStatePath),
		)
	}
	if err := persistTerraformState(temporaryStatePath, localStatePath); err != nil {
		return errors.Join(fmt.Errorf("default-scope registry-seed apply failed: %w", applyErr), err)
	}
	return fmt.Errorf("default-scope registry-seed apply failed, but valid partial state was saved to %q: %w", localStatePath, applyErr)
}
