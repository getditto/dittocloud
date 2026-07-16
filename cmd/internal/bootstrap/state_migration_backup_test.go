package bootstrap

import (
	"bytes"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

func TestCreateTerraformMigrationBackup(t *testing.T) {
	fixedTime := time.Date(2026, time.July, 16, 12, 34, 56, 789, time.UTC)
	originalNow := terraformStateBackupNow
	terraformStateBackupNow = func() time.Time { return fixedTime }
	t.Cleanup(func() { terraformStateBackupNow = originalNow })

	stateValue := rawTerraformStateWithResources([]any{
		map[string]any{
			"mode": "managed", "type": "aws_vpc", "name": "legacy",
			"instances": []any{map[string]any{"index_key": "primary", "attributes": map[string]any{}}},
		},
		map[string]any{
			"mode": "data", "type": "aws_region", "name": "current",
			"instances": []any{map[string]any{"attributes": map[string]any{}}},
		},
	})
	stateValue["outputs"] = map[string]any{
		"aws":    map[string]any{"value": map[string]any{"region": "ap-southeast-2"}},
		"secret": map[string]any{"value": "not-copied-to-manifest"},
	}
	statePath := writeTerraformStateTestFile(t, stateValue)
	stateContent, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	state, exists, err := loadRawTerraformState(statePath)
	if err != nil || !exists {
		t.Fatalf("unable to load raw state fixture: exists=%v err=%v", exists, err)
	}
	scopesContent := []byte(testDefaultScopeRef + ":\n  default: true\n")
	target := `terraform_data.scope_registry["` + testDefaultScopeRef + `"]`

	backup, err := createTerraformMigrationBackup(
		statePath,
		stateContent,
		state,
		testDefaultScopeRef,
		target,
		scopesContent,
	)
	if err != nil {
		t.Fatalf("unexpected migration backup error: %v", err)
	}
	if !strings.Contains(backup.StatePath, "20260716T123456.000000789Z") {
		t.Fatalf("backup path does not include the UTC timestamp: %q", backup.StatePath)
	}
	backupContent, err := os.ReadFile(backup.StatePath)
	if err != nil {
		t.Fatalf("unable to read state backup: %v", err)
	}
	if !bytes.Equal(backupContent, stateContent) {
		t.Fatal("state backup is not byte-for-byte identical")
	}
	for _, path := range []string{backup.StatePath, backup.ManifestPath} {
		info, err := os.Stat(path)
		if err != nil {
			t.Fatalf("unable to inspect backup artifact %q: %v", path, err)
		}
		if got := info.Mode().Perm(); got != 0600 {
			t.Errorf("backup artifact %q permissions: got %04o, want 0600", path, got)
		}
	}

	manifestContent, err := os.ReadFile(backup.ManifestPath)
	if err != nil {
		t.Fatalf("unable to read migration manifest: %v", err)
	}
	if bytes.Contains(manifestContent, []byte("not-copied-to-manifest")) {
		t.Fatal("migration manifest copied an output value")
	}
	var manifest terraformMigrationManifest
	if err := json.Unmarshal(manifestContent, &manifest); err != nil {
		t.Fatalf("unable to decode migration manifest: %v", err)
	}
	if manifest.SchemaVersion != terraformMigrationManifestSchemaVersion ||
		manifest.Operation != "scope-registry-seed" ||
		manifest.ScopeRef != testDefaultScopeRef ||
		manifest.TargetAddress != target ||
		manifest.StateBackupPath != backup.StatePath {
		t.Fatalf("unexpected migration manifest: %#v", manifest)
	}
	if !slices.Equal(manifest.OutputNames, []string{"aws", "secret"}) {
		t.Fatalf("manifest output names: got %v", manifest.OutputNames)
	}
	if !slices.Equal(manifest.ResourceAddresses, []string{`aws_region.current`, `aws_vpc.legacy["primary"]`}) {
		t.Fatalf("manifest resource addresses: got %v", manifest.ResourceAddresses)
	}
}

func TestCreateTerraformMigrationBackupRejectsChangedState(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawTerraformStateWithResources(nil))
	expectedState, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	changedState := rawTerraformStateWithResources([]any{
		map[string]any{"mode": "managed", "type": "terraform_data", "name": "changed", "instances": []any{}},
	})
	changedContent, err := json.Marshal(changedState)
	if err != nil {
		t.Fatalf("unable to encode changed state: %v", err)
	}
	if err := os.WriteFile(statePath, changedContent, 0600); err != nil {
		t.Fatalf("unable to replace state fixture: %v", err)
	}
	state, exists, err := loadRawTerraformState(statePath)
	if err != nil || !exists {
		t.Fatalf("unable to load changed state: exists=%v err=%v", exists, err)
	}

	_, err = createTerraformMigrationBackup(statePath, expectedState, state, testDefaultScopeRef, "target", []byte("scopes"))
	if err == nil || !strings.Contains(err.Error(), "changed after migration preflight") {
		t.Fatalf("expected changed-state error, got %v", err)
	}
	matches, globErr := filepath.Glob(statePath + ".dittocloud-backup-*")
	if globErr != nil || len(matches) != 0 {
		t.Fatalf("backup artifacts created after changed-state error: %v err=%v", matches, globErr)
	}
}

func TestCreateTerraformMigrationBackupRetainsVerifiedBackupWhenDirectorySyncFails(t *testing.T) {
	statePath := writeTerraformStateTestFile(t, rawTerraformStateWithResources([]any{}))
	stateContent, err := os.ReadFile(statePath)
	if err != nil {
		t.Fatalf("unable to read state fixture: %v", err)
	}
	state, exists, err := loadRawTerraformState(statePath)
	if err != nil || !exists {
		t.Fatalf("unable to load state fixture: exists=%v err=%v", exists, err)
	}
	originalSync := syncStateBackupDirectory
	syncStateBackupDirectory = func(directoryPath string) error { return errors.New("injected sync failure") }
	t.Cleanup(func() { syncStateBackupDirectory = originalSync })

	_, err = createTerraformMigrationBackup(statePath, stateContent, state, testDefaultScopeRef, "target", []byte("scopes"))
	if err == nil || !strings.Contains(err.Error(), "directory could not be flushed") {
		t.Fatalf("expected directory sync error, got %v", err)
	}
	matches, globErr := filepath.Glob(statePath + ".dittocloud-backup-*")
	if globErr != nil || len(matches) != 1 {
		t.Fatalf("expected one retained state backup, got %v err=%v", matches, globErr)
	}
}
