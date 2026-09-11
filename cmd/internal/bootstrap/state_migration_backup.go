package bootstrap

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strings"
	"time"
)

const terraformMigrationManifestSchemaVersion = 1

type terraformMigrationBackup struct {
	StatePath    string
	ManifestPath string
}

type terraformMigrationManifest struct {
	SchemaVersion      int      `json:"schemaVersion"`
	Operation          string   `json:"operation"`
	CreatedAt          string   `json:"createdAt"`
	CanonicalStatePath string   `json:"canonicalStatePath"`
	StateBackupPath    string   `json:"stateBackupPath"`
	StateSHA256        string   `json:"stateSha256"`
	ScopesSHA256       string   `json:"scopesSha256"`
	TerraformVersion   string   `json:"terraformVersion"`
	StateSerial        int64    `json:"stateSerial"`
	StateLineage       string   `json:"stateLineage"`
	ScopeRef           string   `json:"scopeRef,omitempty"`
	TargetAddress      string   `json:"targetAddress,omitempty"`
	ImportAddresses    []string `json:"importAddresses,omitempty"`
	OutputNames        []string `json:"outputNames"`
	ResourceAddresses  []string `json:"resourceAddresses"`
}

type terraformMigrationManifestOperation struct {
	Name            string
	ScopeRef        string
	TargetAddress   string
	ImportAddresses []string
}

var (
	terraformStateBackupNow  = time.Now
	syncStateBackupDirectory = syncParentDirectory
)

func createTerraformMigrationBackup(
	statePath string,
	expectedState []byte,
	state rawTerraformState,
	scopeRef string,
	targetAddress string,
	scopesContent []byte,
) (terraformMigrationBackup, error) {
	return createTerraformOperationBackup(
		statePath,
		expectedState,
		state,
		scopesContent,
		terraformMigrationManifestOperation{
			Name:          "scope-registry-seed",
			ScopeRef:      scopeRef,
			TargetAddress: targetAddress,
		},
	)
}

func createTerraformImportBackup(
	statePath string,
	expectedState []byte,
	state rawTerraformState,
	importAddresses []string,
	scopesContent []byte,
) (terraformMigrationBackup, error) {
	if len(importAddresses) == 0 {
		return terraformMigrationBackup{}, fmt.Errorf("at least one Terraform import address is required for a pre-import backup")
	}
	return createTerraformOperationBackup(
		statePath,
		expectedState,
		state,
		scopesContent,
		terraformMigrationManifestOperation{
			Name:            "scope-import",
			ImportAddresses: slices.Clone(importAddresses),
		},
	)
}

func createTerraformOperationBackup(
	statePath string,
	expectedState []byte,
	state rawTerraformState,
	scopesContent []byte,
	operation terraformMigrationManifestOperation,
) (terraformMigrationBackup, error) {
	canonicalStatePath, err := canonicalizeStatePath(statePath)
	if err != nil {
		return terraformMigrationBackup{}, fmt.Errorf("unable to prepare Terraform migration backup: %w", err)
	}
	info, err := os.Stat(canonicalStatePath)
	if err != nil {
		return terraformMigrationBackup{}, fmt.Errorf("unable to inspect Terraform state before migration backup: %w", err)
	}
	if !info.Mode().IsRegular() {
		return terraformMigrationBackup{}, fmt.Errorf("terraform state %q must be a regular file before migration backup", canonicalStatePath)
	}
	currentState, err := os.ReadFile(canonicalStatePath)
	if err != nil {
		return terraformMigrationBackup{}, fmt.Errorf("unable to read Terraform state before migration backup: %w", err)
	}
	if !bytes.Equal(currentState, expectedState) {
		return terraformMigrationBackup{}, fmt.Errorf("terraform state %q changed after migration preflight; rerun the command", canonicalStatePath)
	}

	createdAt := terraformStateBackupNow().UTC()
	backupPath, err := nextTerraformMigrationBackupPath(canonicalStatePath, createdAt)
	if err != nil {
		return terraformMigrationBackup{}, err
	}
	if err := writeExclusiveSyncedFile(backupPath, currentState, 0600); err != nil {
		return terraformMigrationBackup{}, fmt.Errorf("unable to create Terraform migration state backup %q: %w", backupPath, err)
	}
	backupContent, err := os.ReadFile(backupPath)
	if err != nil {
		return terraformMigrationBackup{}, fmt.Errorf("unable to verify terraform migration state backup %q: %w", backupPath, err)
	}
	if !bytes.Equal(backupContent, currentState) {
		return terraformMigrationBackup{}, fmt.Errorf("terraform migration state backup %q failed integrity check", backupPath)
	}
	if err := syncStateBackupDirectory(filepath.Dir(backupPath)); err != nil {
		return terraformMigrationBackup{}, fmt.Errorf("terraform migration state backup %q was written but its directory could not be flushed: %w", backupPath, err)
	}

	stateDigest := sha256.Sum256(currentState)
	scopesDigest := sha256.Sum256(scopesContent)
	manifest := terraformMigrationManifest{
		SchemaVersion:      terraformMigrationManifestSchemaVersion,
		Operation:          operation.Name,
		CreatedAt:          createdAt.Format(time.RFC3339Nano),
		CanonicalStatePath: canonicalStatePath,
		StateBackupPath:    backupPath,
		StateSHA256:        hex.EncodeToString(stateDigest[:]),
		ScopesSHA256:       hex.EncodeToString(scopesDigest[:]),
		TerraformVersion:   state.TerraformVersion,
		StateSerial:        state.Serial,
		StateLineage:       state.Lineage,
		ScopeRef:           operation.ScopeRef,
		TargetAddress:      operation.TargetAddress,
		ImportAddresses:    operation.ImportAddresses,
		OutputNames:        sortedTerraformStateOutputNames(state),
		ResourceAddresses:  sortedTerraformStateResourceAddresses(state),
	}
	manifestContent, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return terraformMigrationBackup{}, fmt.Errorf("unable to encode Terraform migration manifest: %w", err)
	}
	manifestContent = append(manifestContent, '\n')
	manifestPath := backupPath + ".manifest.json"
	if err := writeExclusiveSyncedFile(manifestPath, manifestContent, 0600); err != nil {
		return terraformMigrationBackup{}, fmt.Errorf(
			"terraform migration state backup was retained at %q, but its manifest could not be written to %q: %w",
			backupPath,
			manifestPath,
			err,
		)
	}
	if err := syncStateBackupDirectory(filepath.Dir(manifestPath)); err != nil {
		return terraformMigrationBackup{}, fmt.Errorf(
			"terraform migration backup and manifest were retained at %q and %q, but their directory could not be flushed: %w",
			backupPath,
			manifestPath,
			err,
		)
	}
	return terraformMigrationBackup{StatePath: backupPath, ManifestPath: manifestPath}, nil
}

func nextTerraformMigrationBackupPath(statePath string, createdAt time.Time) (string, error) {
	basePath := statePath + ".dittocloud-backup-" + createdAt.UTC().Format("20060102T150405.000000000Z")
	for attempt := 0; attempt < 1000; attempt++ {
		candidate := basePath
		if attempt > 0 {
			candidate = fmt.Sprintf("%s-%d", basePath, attempt)
		}
		_, err := os.Lstat(candidate)
		if errors.Is(err, os.ErrNotExist) {
			return candidate, nil
		}
		if err != nil {
			return "", fmt.Errorf("unable to inspect Terraform migration backup candidate %q: %w", candidate, err)
		}
	}
	return "", fmt.Errorf("unable to allocate a unique Terraform migration backup path for %q", statePath)
}

func writeExclusiveSyncedFile(path string, content []byte, permissions os.FileMode) (runErr error) {
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, permissions.Perm())
	if err != nil {
		return err
	}
	complete := false
	open := true
	defer func() {
		if open {
			runErr = errors.Join(runErr, file.Close())
		}
		if !complete {
			_ = os.Remove(path)
		}
	}()
	if err := file.Chmod(permissions.Perm()); err != nil {
		return err
	}
	if _, err := file.Write(content); err != nil {
		return err
	}
	if err := file.Sync(); err != nil {
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	open = false
	complete = true
	return nil
}

func sortedTerraformStateOutputNames(state rawTerraformState) []string {
	names := make([]string, 0, len(state.Outputs))
	for name := range state.Outputs {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sortedTerraformStateResourceAddresses(state rawTerraformState) []string {
	addresses := map[string]struct{}{}
	for _, resource := range state.Resources {
		baseAddress := awsLegacyResourceAddress(resource)
		for _, instance := range resource.Instances {
			if instance.Deposed != "" {
				continue
			}
			address := baseAddress
			indexKey := strings.TrimSpace(string(instance.IndexKey))
			if indexKey != "" && indexKey != "null" {
				var compactIndex bytes.Buffer
				if json.Compact(&compactIndex, instance.IndexKey) == nil {
					address += "[" + compactIndex.String() + "]"
				}
			}
			addresses[address] = struct{}{}
		}
	}
	result := make([]string, 0, len(addresses))
	for address := range addresses {
		result = append(result, address)
	}
	sort.Strings(result)
	return result
}
