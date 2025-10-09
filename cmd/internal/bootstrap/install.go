package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/fatih/color"
	"github.com/hashicorp/go-version"
	"github.com/hashicorp/hc-install/product"
	"github.com/hashicorp/hc-install/releases"

	"github.com/getditto/ditto-cloud-bootstrap/cmd/internal/log"
)

func isCompatibleTerraformVersion(execPath, requiredVersion string) bool {
	versionInfo, err := getTerraformVersionInfo(execPath)
	if err != nil {
		return false
	}

	systemVersion, err := version.NewVersion(versionInfo.TerraformVersion)
	if err != nil {
		return false
	}

	required := version.Must(version.NewVersion(requiredVersion))

	// Allow same major.minor version (e.g., 1.11.x is compatible with 1.11.4)
	return systemVersion.Segments()[0] == required.Segments()[0] &&
		systemVersion.Segments()[1] == required.Segments()[1]
}

func isExactTerraformVersion(execPath, requiredVersion string) bool {
	versionInfo, err := getTerraformVersionInfo(execPath)
	if err != nil {
		return false
	}

	return versionInfo.TerraformVersion == requiredVersion
}

func isTerraformExecutable(execPath string) bool {
	return exec.Command(execPath, "version").Run() == nil
}

func getTerraformVersionInfo(execPath string) (terraformVersionInfo, error) {
	cmd := exec.Command(execPath, "version", "-json")
	output, err := cmd.Output()
	if err != nil {
		return terraformVersionInfo{}, err
	}

	var versionInfo terraformVersionInfo
	if err := json.Unmarshal(output, &versionInfo); err != nil {
		return terraformVersionInfo{}, err
	}

	return versionInfo, nil
}

type terraformVersionInfo struct {
	TerraformVersion string `json:"terraform_version"`
}

const RequiredTerraformVersion = "1.11.4"

func getTerraform(ctx context.Context, shouldDownload bool) (string, error) {
	if !shouldDownload {
		if execPath, found := findExistingTerraform(ctx, RequiredTerraformVersion); found {
			return execPath, nil
		}
		shouldDownload = true
	}

	if shouldDownload {
		// Inform the user that terraform is being downloaded
		progress := color.New(color.FgMagenta)
		progress.Printf("Terraform not found or incompatible version detected. Downloading Terraform %s\n", RequiredTerraformVersion)
		return downloadAndCacheTerraform(ctx, RequiredTerraformVersion)
	}

	return "", fmt.Errorf("terraform executable not found and download was not attempted")
}

func findExistingTerraform(ctx context.Context, requiredVersion string) (string, bool) {
	logger := log.FromContext(ctx)

	// Check system terraform first (prioritize over cached)
	if systemPath, found := getSystemTerraform(ctx, requiredVersion); found {
		logger.Debug("Using system terraform", "path", systemPath)
		return systemPath, true
	}

	// Try cached terraform
	if cachedPath, found := getCachedTerraform(ctx, requiredVersion); found {
		logger.Debug("Using cached terraform", "path", cachedPath)
		return cachedPath, true
	}

	logger.Debug("compatible terraform executable not found in either path or cache")
	return "", false
}

func getSystemTerraform(ctx context.Context, requiredVersion string) (string, bool) {
	logger := log.FromContext(ctx)

	systemTerraform, err := exec.LookPath("terraform")
	if err != nil {
		return "", false
	}

	if isCompatibleTerraformVersion(systemTerraform, requiredVersion) {
		return systemTerraform, true
	}

	systemVersion, err := getTerraformVersionInfo(systemTerraform)
	if err != nil {
		logger.Debug(
			fmt.Sprintf(
				"Could not determine system terraform version provided (%s): %v",
				systemTerraform, err,
			),
		)
		return "", false
	}
	logger.Debug(
		fmt.Sprintf(
			"System terraform version %s at %s incompatible with required version %s",
			systemVersion.TerraformVersion, systemTerraform, requiredVersion,
		),
	)
	return "", false
}

func downloadAndCacheTerraform(ctx context.Context, requiredVersion string) (string, error) {
	logger := log.FromContext(ctx)

	installer := &releases.ExactVersion{
		Product: product.Terraform,
		Version: version.Must(version.NewVersion(requiredVersion)),
	}

	logger.Debug("Downloading terraform", "version", requiredVersion)
	downloadedPath, err := installer.Install(ctx)
	if err != nil {
		return "", fmt.Errorf("unable to install terraform: %w", err)
	}

	// Cache the downloaded terraform for future use
	cachedPath, err := cacheTerraform(downloadedPath, requiredVersion)
	if err != nil {
		logger.Debug("Warning: Failed to cache terraform", "error", err)
		return downloadedPath, nil
	}

	logger.Debug("Cached terraform for future use", "path", cachedPath)
	return cachedPath, nil
}

// getCachedTerraform checks if terraform is cached and returns its path
func getCachedTerraform(ctx context.Context, requiredVersion string) (string, bool) {
	logger := log.FromContext(ctx)
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", false
	}

	// Create cache path: ~/.cache/ditto-cloud-bootstrap/terraform/{version}/terraform
	cachedPath := filepath.Join(cacheDir, "ditto-cloud-bootstrap", "terraform", requiredVersion, "terraform")

	// Check if cached terraform exists, is executable and the right version
	if info, err := os.Stat(cachedPath); err == nil && !info.IsDir() {
		if isTerraformExecutable(cachedPath) && isExactTerraformVersion(cachedPath, requiredVersion) {
			return cachedPath, true
		}
	}
	logger.Debug("Cached terraform not found or incompatible", "path", cachedPath)
	return "", false
}

// cacheTerraform copies the downloaded terraform to cache directory
func cacheTerraform(sourcePath, requiredVersion string) (string, error) {
	cacheDir, err := os.UserCacheDir()
	if err != nil {
		return "", fmt.Errorf("unable to get user cache directory: %w", err)
	}

	// Create cache directory structure
	terraformCacheDir := filepath.Join(cacheDir, "ditto-cloud-bootstrap", "terraform", requiredVersion)
	if err := os.MkdirAll(terraformCacheDir, 0755); err != nil {
		return "", fmt.Errorf("unable to create cache directory: %w", err)
	}

	cachedPath := filepath.Join(terraformCacheDir, "terraform")

	// Copy the terraform binary to cache
	sourceData, err := os.ReadFile(sourcePath)
	if err != nil {
		return "", fmt.Errorf("unable to read source terraform binary: %w", err)
	}

	if err := os.WriteFile(cachedPath, sourceData, 0755); err != nil {
		return "", fmt.Errorf("unable to write cached terraform binary: %w", err)
	}

	return cachedPath, nil
}
