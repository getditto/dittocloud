package bootstrap

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"

	"github.com/getditto/ditto-cloud-bootstrap/cmd/internal/log"
)

func TestGetTerraform(t *testing.T) {
	ctx := context.Background()
	logger := log.Setup("debug")
	ctx = log.WithLogger(ctx, logger)

	t.Run("force download when shouldDownload is true", func(t *testing.T) {
		// Set up clean isolated environment
		cleanEnv := setupCleanEnvironment(t)
		defer cleanEnv.cleanup()

		execPath, err := getTerraform(ctx, true)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if execPath == "" {
			t.Fatal("expected non-empty execPath")
		}

		// Verify terraform works and is cached
		if err := exec.Command(execPath, "version").Run(); err != nil {
			t.Errorf("terraform executable at %s is not working: %v", execPath, err)
		}

		// Should be in cache directory
		if !strings.Contains(execPath, "ditto-cloud-bootstrap/terraform") {
			t.Errorf("expected terraform to be cached, got path: %s", execPath)
		}
	})

	t.Run("use cached terraform when available", func(t *testing.T) {
		// Set up clean environment and pre-populate cache
		cleanEnv := setupCleanEnvironment(t)
		defer cleanEnv.cleanup()

		// Pre-install terraform to cache using real code
		_, err := downloadAndCacheTerraform(ctx, RequiredTerraformVersion)
		if err != nil {
			t.Fatalf("Failed to download and cache terraform: %v", err)
		}

		execPath, err := getTerraform(ctx, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if execPath == "" {
			t.Fatal("expected non-empty execPath")
		}

		// Verify terraform works
		if err := exec.Command(execPath, "version").Run(); err != nil {
			t.Errorf("terraform executable at %s is not working: %v", execPath, err)
		}

		// Should be in cache directory
		if !strings.Contains(execPath, "ditto-cloud-bootstrap/terraform") {
			t.Errorf("expected terraform to be from cache, got path: %s", execPath)
		}
	})

	t.Run("use system terraform when available and compatible", func(t *testing.T) {
		// Set up clean environment with compatible system terraform
		cleanEnv := setupCleanEnvironment(t)
		defer cleanEnv.cleanup()

		systemTerraformSetup := installSystemTerraform(t, RequiredTerraformVersion)
		defer systemTerraformSetup.cleanup()

		execPath, err := getTerraform(ctx, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if execPath == "" {
			t.Fatal("expected non-empty execPath")
		}

		// Verify terraform works
		if err := exec.Command(execPath, "version").Run(); err != nil {
			t.Errorf("terraform executable at %s is not working: %v", execPath, err)
		}

		// Should be system terraform (not cached)
		if strings.Contains(execPath, "ditto-cloud-bootstrap/terraform") {
			t.Errorf("expected system terraform, but got cached: %s", execPath)
		}

		// Should be our installed system terraform
		if !strings.Contains(execPath, systemTerraformSetup.dir) {
			t.Errorf("expected system terraform from %s, got: %s", systemTerraformSetup.dir, execPath)
		}
	})

	t.Run("download when no cached and no system terraform", func(t *testing.T) {
		// Set up completely empty environment
		cleanEnv := setupCleanEnvironment(t)
		defer cleanEnv.cleanup()

		execPath, err := getTerraform(ctx, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if execPath == "" {
			t.Fatal("expected non-empty execPath")
		}

		// Verify terraform works
		if err := exec.Command(execPath, "version").Run(); err != nil {
			t.Errorf("terraform executable at %s is not working: %v", execPath, err)
		}

		// Should be cached (downloaded and then cached)
		if !strings.Contains(execPath, "ditto-cloud-bootstrap/terraform") {
			t.Errorf("expected downloaded terraform to be cached, got path: %s", execPath)
		}
	})

	t.Run("download when system terraform incompatible", func(t *testing.T) {
		// Set up clean environment with incompatible system terraform
		cleanEnv := setupCleanEnvironment(t)
		defer cleanEnv.cleanup()

		systemTerraformSetup := installSystemTerraform(t, "1.10.0") // incompatible version
		defer systemTerraformSetup.cleanup()

		execPath, err := getTerraform(ctx, false)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if execPath == "" {
			t.Fatal("expected non-empty execPath")
		}

		// Verify terraform works
		if err := exec.Command(execPath, "version").Run(); err != nil {
			t.Errorf("terraform executable at %s is not working: %v", execPath, err)
		}

		// Should be cached (downloaded because system was incompatible)
		if !strings.Contains(execPath, "ditto-cloud-bootstrap/terraform") {
			t.Errorf("expected downloaded terraform to be cached, got path: %s", execPath)
		}
	})
}

type cleanEnvironment struct {
	originalPath string
	cacheDir     string
	cleanup      func()
}

// setupCleanEnvironment creates a completely isolated test environment
func setupCleanEnvironment(t *testing.T) *cleanEnvironment {
	// Save original PATH
	originalPath := os.Getenv("PATH")

	os.Setenv("PATH", "")

	// Create isolated cache directory for this test - only works for posix flavors that honor XDG_CACHE_HOME
	tempCacheDir := t.TempDir()
	os.Setenv("XDG_CACHE_HOME", tempCacheDir)

	return &cleanEnvironment{
		originalPath: originalPath,
		cacheDir:     tempCacheDir,
		cleanup: func() {
			os.Setenv("PATH", originalPath)
			os.Unsetenv("XDG_CACHE_HOME")
		},
	}
}

type systemTerraformSetup struct {
	dir     string
	cleanup func()
}

// installSystemTerraform downloads and installs a specific terraform version
// to temp directory and adds it to PATH to simulate "system" terraform
func installSystemTerraform(t *testing.T, terraformVersion string) *systemTerraformSetup {
	ctx := context.Background()

	// Create temp directory for "system" terraform
	tempDir := t.TempDir()

	// Use hc-install to download the specific version
	installer := &releases.ExactVersion{
		Product: product.Terraform,
		Version: version.Must(version.NewVersion(terraformVersion)),
	}

	terraformPath, err := installer.Install(ctx)
	if err != nil {
		t.Fatalf("Failed to install terraform %s: %v", terraformVersion, err)
	}

	// Copy to our temp directory and rename to "terraform"
	systemTerraformPath := filepath.Join(tempDir, "terraform")
	terraformData, err := os.ReadFile(terraformPath)
	if err != nil {
		t.Fatalf("Failed to read downloaded terraform: %v", err)
	}

	if err := os.WriteFile(systemTerraformPath, terraformData, 0755); err != nil {
		t.Fatalf("Failed to copy terraform to temp dir: %v", err)
	}

	// Add temp dir to front of current PATH
	currentPath := os.Getenv("PATH")
	newPath := tempDir + string(os.PathListSeparator) + currentPath
	os.Setenv("PATH", newPath)

	return &systemTerraformSetup{
		dir: tempDir,
		cleanup: func() {
			os.Setenv("PATH", currentPath)
		},
	}
}
