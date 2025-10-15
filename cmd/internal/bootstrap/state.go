package bootstrap

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"time"

	"github.com/fatih/color"
	"github.com/hashicorp/terraform-exec/tfexec"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"

	"github.com/getditto/ditto-cloud-bootstrap/cmd/internal/log"
)

// StateManager handles both local and remote Terraform state operations
type StateManager struct {
	config           ProviderConfig
	workingDir       string
	tf               *tfexec.Terraform
	logger           *slog.Logger
	progressColor    *color.Color
	localStatePath   string
	workingStatePath string
}

// NewStateManager creates a new StateManager
func NewStateManager(ctx context.Context, config ProviderConfig, workingDir string, tf *tfexec.Terraform, localStatePath string) *StateManager {
	return &StateManager{
		config:           config,
		workingDir:       workingDir,
		tf:               tf,
		logger:           log.FromContext(ctx),
		progressColor:    color.New(color.FgMagenta),
		localStatePath:   localStatePath,
		workingStatePath: path.Join(workingDir, StateFile),
	}
}

// InitializeWithBackend performs Terraform initialization with the appropriate backend configuration
// It checks for remote backend first, then falls back to local backend if needed
func (s *StateManager) InitializeWithBackend(ctx context.Context) error {
	// First, check if remote state bucket exists
	s.logger.Debug("Checking for remote backend availability")
	exists, err := s.checkBucketAccess(ctx)
	if err != nil {
		s.logger.Warn("Failed to check remote state, will use local backend", "error", err)
	}

	if exists && err == nil {
		s.logger.Debug("Remote backend is available, configuring remote backend")
		// Handle local state file transfer for potential migration
		if err := s.handleLocalStateTransfer(); err != nil {
			return fmt.Errorf("failed to handle local state transfer for migration: %w", err)
		}
		return s.configureRemoteBackend(ctx)
	}

	// Remote backend not available, use local backend
	s.logger.Debug("Remote backend not available, using local backend")
	s.progressColor.Println("Remote backend not available, using local backend...")

	// Handle local state file transfer for local backend scenarios
	if err := s.handleLocalStateTransfer(); err != nil {
		return fmt.Errorf("failed to handle local state transfer: %w", err)
	}

	// Initialize with local backend (no backend configuration)
	s.logger.Debug("Initializing Terraform with local backend")
	s.progressColor.Println("Initializing Terraform with local backend...")

	if err := s.tf.Init(ctx, tfexec.Upgrade(true)); err != nil {
		s.logger.Error("Failed to initialize terraform with local backend", "error", err)
		return fmt.Errorf("unable to initialize terraform with local backend: %w", err)
	}

	s.logger.Debug("Successfully initialized Terraform with local backend")
	s.progressColor.Println("✅ Successfully initialized with local backend")

	return nil
}

// handleLocalStateTransfer handles copying state files for local backend operations
func (s *StateManager) handleLocalStateTransfer() error {
	// Only copy state file if we have both paths (local backend scenario)
	if s.localStatePath == "" || s.workingStatePath == "" {
		return nil
	}

	// Copy the local state file to the temporary directory if it exists
	if _, err := os.Stat(s.localStatePath); err == nil {
		s.progressColor.Printf("Copying local state file %q to temporary directory\n", s.localStatePath)
		input, err := os.ReadFile(s.localStatePath)
		if err != nil {
			return fmt.Errorf("unable to read local state file: %w", err)
		}
		if err := os.WriteFile(s.workingStatePath, input, 0600); err != nil {
			return fmt.Errorf("unable to write state file to temporary directory: %w", err)
		}
	} else {
		s.progressColor.Printf("No local state file found at %q, a new one will be created\n", s.localStatePath)
	}
	return nil
}

// checkAndMigrateToRemoteBackend checks if remote state is available after Terraform apply
// and migrates local state to remote backend if needed
func (s *StateManager) checkAndMigrateToRemoteBackend(ctx context.Context) error {
	// Check if remote state bucket exists now (after first apply, that creates this bucket)
	// we're not erroring out on these checks and going ahead and trying to reconfigure anyway
	exists, err := s.checkBucketAccess(ctx)
	if err != nil {
		s.logger.Warn("Failed to check remote state after apply", "error", err)
		return nil
	}

	if !exists {
		s.logger.Warn("Remote state bucket still doesn't exist after apply")
		return nil
	}

	// If remote state exists now, configure and reinitialize with remote backend
	return s.configureRemoteBackend(ctx)
}

// FinalizeStateTransfer handles copying state files back and cleanup operations
func (s *StateManager) FinalizeStateTransfer(ctx context.Context) {
	// Only handle finalization if we have both paths (local backend scenario)
	if s.localStatePath == "" || s.workingStatePath == "" {
		return
	}

	// Copy the state file back to the original location
	s.progressColor.Printf("Copying state file back to %q\n", s.localStatePath)
	stateFileData, err := os.ReadFile(s.workingStatePath)
	if err != nil {
		s.progressColor.Printf("unable to read state file from temporary directory: %v", err)
		return
	}
	if err := os.WriteFile(s.localStatePath, stateFileData, 0600); err != nil {
		s.progressColor.Printf("unable to write state file to %q: %v", s.localStatePath, err)
		return
	}

	// If remote backend wasn't available before apply, check again now
	s.logger.Debug("Checking if remote backend is available after apply")
	if err := s.checkAndMigrateToRemoteBackend(ctx); err != nil {
		s.logger.Warn("Failed to migrate to remote backend after apply", "error", err)
	}
}

// TerraformBackendConfig is an interface for getting backend configuration for a specific provider
type TerraformBackendConfig interface {
	// BackendConfigFile returns the complete terraform backend configuration file content
	BackendConfigFile() (string, error)
	// GetBackendConfig returns the Terraform backend configuration options for a provider
	GetBackendConfig() ([]tfexec.InitOption, error)
}

// configureRemoteBackend configures and initializes Terraform with remote backend
func (s *StateManager) configureRemoteBackend(ctx context.Context) error {
	s.logger.Debug("Configuring remote backend")
	s.progressColor.Println("Configuring remote backend...")

	// Get backend configuration from the provider
	backendConfig, err := s.config.GetBackendConfig()
	if err != nil {
		return fmt.Errorf("failed to get backend configuration: %w", err)
	}

	s.logger.Debug("Creating backend configuration file")

	// Create the appropriate backend.tf file in the working directory
	if err := s.createBackendFile(ctx, backendConfig); err != nil {
		return fmt.Errorf("failed to create backend configuration file: %w", err)
	}

	// Get backend config options
	backendArgs, err := backendConfig.GetBackendConfig()
	if err != nil {
		return fmt.Errorf("failed to get backend config options: %w", err)
	}

	// Build init options starting with backend config
	initOptions := backendArgs

	// Add common init arguments
	initOptions = append(initOptions, tfexec.Upgrade(true))

	// Check if there's a local state file that needs migration
	localStateFile := filepath.Join(s.workingDir, "terraform.tfstate")
	if _, err := os.Stat(localStateFile); err == nil {
		s.logger.Debug("Local state file exists, will migrate to remote backend")
		s.progressColor.Println("Migrating local state to remote backend...")
		s.progressColor.Println("Note: State will be automatically migrated without prompts")

		// Add force-copy flag to automatically migrate state without user prompts
		initOptions = append(initOptions, tfexec.ForceCopy(true), tfexec.Reconfigure(true))
	}

	// Re-initialize with remote backend
	s.logger.Debug("Reinitializing Terraform with remote backend", "workingDir", s.workingDir)

	// Always show output for backend migration since it might require user confirmation
	s.tf.SetStdout(os.Stdout)
	s.tf.SetStderr(os.Stderr)

	if err := s.tf.Init(ctx, initOptions...); err != nil {
		s.logger.Error("Failed to initialize with remote backend, will use local state", "error", err)
		s.progressColor.Println("Failed to configure remote backend, continuing with local state")

		// Reset stdout/stderr to previous state
		s.tf.SetStdout(nil)
		s.tf.SetStderr(nil)

		// Reinitialize with local backend (no backend configuration)
		if err := s.tf.Init(ctx, tfexec.Upgrade(true)); err != nil {
			return fmt.Errorf("failed to reinitialize with local backend: %w", err)
		}

		return nil // Continue with local state, non-critical error
	}

	s.logger.Debug("Successfully initialized with remote backend")
	s.progressColor.Println("✅ Successfully configured remote backend")

	// Reset stdout/stderr to previous state
	s.tf.SetStdout(nil)
	s.tf.SetStderr(nil)

	return nil
}

// checkBucketAccess checks if the remote state bucket exists and is accessible
func (s *StateManager) checkBucketAccess(ctx context.Context) (bool, error) {
	// Add a timeout to the context to avoid hanging
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()

	bucketURL, err := s.config.BucketURL()
	if err != nil {
		return false, fmt.Errorf("failed to get bucket URL: %w", err)
	}

	s.logger.Debug("Checking if remote state bucket exists", "bucket", bucketURL)

	// Create a small test file
	testFilePath := filepath.Join(s.workingDir, ".bucket_test")
	if err := os.WriteFile(testFilePath, []byte("test bucket access"), 0600); err != nil {
		return false, fmt.Errorf("failed to create test file: %w", err)
	}
	defer os.Remove(testFilePath)

	// Try to test bucket access by attempting a simple operation
	bucket, err := blob.OpenBucket(ctx, bucketURL)
	if err != nil {
		s.logger.Debug("Remote state bucket doesn't exist or is not accessible",
			"error", err,
			"bucket", bucketURL)
		return false, nil
	}
	defer bucket.Close()

	// Try a simple write/read test
	testObjectName := ".test_file_" + time.Now().Format("20060102150405")
	writer, err := bucket.NewWriter(ctx, testObjectName, nil)
	if err != nil {
		s.logger.Debug("Remote state bucket doesn't exist or is not accessible",
			"error", err,
			"bucket", bucketURL)
		return false, nil
	}

	if _, err := writer.Write([]byte("test")); err != nil {
		writer.Close()
		return false, nil
	}

	if err := writer.Close(); err != nil {
		return false, nil
	}

	s.logger.Debug("Remote state bucket exists and is accessible", "bucket", bucketURL)
	return true, nil
}

// createBackendFile creates a backend.tf file in the working directory with the appropriate backend block
func (s *StateManager) createBackendFile(ctx context.Context, backendConfig TerraformBackendConfig) error {
	s.logger.Debug("Creating backend file", "workingDir", s.workingDir)

	backendContent, err := backendConfig.BackendConfigFile()
	if err != nil {
		return fmt.Errorf("failed to get backend config file content: %w", err)
	}

	backendFilePath := filepath.Join(s.workingDir, "backend.tf")
	if err := os.WriteFile(backendFilePath, []byte(backendContent), 0644); err != nil {
		return fmt.Errorf("failed to write backend.tf file: %w", err)
	}

	s.logger.Debug("Created backend configuration file", "path", backendFilePath)
	return nil
}
