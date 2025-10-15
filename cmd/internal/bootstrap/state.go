package bootstrap

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/fatih/color"
	"gocloud.dev/blob"
	_ "gocloud.dev/blob/gcsblob"
	_ "gocloud.dev/blob/s3blob"
	"gocloud.dev/gcerrors"

	"github.com/getditto/ditto-cloud-bootstrap/cmd/internal/log"
)

// StateManager handles remote state operations
type StateManager struct {
	BucketURL  string
	ObjectName string
}

// StateManagerFromConfig creates a state manager from the provider configuration
func StateManagerFromConfig(config ProviderConfig) (*StateManager, error) {
	var bucketURL string
	var err error

	bucketURL, err = config.BucketURL()
	if err != nil {
		return nil, fmt.Errorf("failed to get bucket URL: %w", err)
	}

	return &StateManager{
		BucketURL:  bucketURL,
		ObjectName: "terraform.tfstate",
	}, nil
}

func (s *StateManager) DownloadState(ctx context.Context, localPath string) error {
	progress := color.New(color.FgMagenta)

	// Open the bucket
	bucket, err := blob.OpenBucket(ctx, s.BucketURL)
	if err != nil {
		return fmt.Errorf("failed to open bucket: %w", err)
	}
	defer bucket.Close()

	progress.Printf("Attempting to download state file from bucket %s...\n", s.BucketURL)

	// Try to read the state file
	reader, err := bucket.NewReader(ctx, s.ObjectName, nil)
	if err != nil {
		if gcerrors.Code(err) == gcerrors.NotFound {
			return fmt.Errorf("State file %s not found in bucket %s", s.ObjectName, s.BucketURL)
		}
		return fmt.Errorf("failed to open state file from bucket: %w", err)
	}
	defer reader.Close()

	// Create local file
	localFile, err := os.Create(localPath)
	if err != nil {
		return fmt.Errorf("failed to create local state file: %w", err)
	}
	defer localFile.Close()

	// Copy the content
	if _, err := io.Copy(localFile, reader); err != nil {
		return fmt.Errorf("failed to copy state file content: %w", err)
	}

	progress.Printf("Successfully downloaded state file from bucket\n")
	return nil
}

func (s *StateManager) UploadState(ctx context.Context, localPath string) error {
	logger := log.FromContext(ctx)
	progress := color.New(color.FgMagenta)

	// Check if local state file exists
	if _, err := os.Stat(localPath); os.IsNotExist(err) {
		logger.Info("No local state file to upload")
		return nil
	}

	// Open the bucket
	bucket, err := blob.OpenBucket(ctx, s.BucketURL)
	if err != nil {
		return fmt.Errorf("failed to open bucket: %w", err)
	}
	defer bucket.Close()

	progress.Printf("Uploading state file to bucket %s...\n", s.BucketURL)

	// Open local file
	localFile, err := os.Open(localPath)
	if err != nil {
		return fmt.Errorf("failed to open local state file: %w", err)
	}
	defer localFile.Close()

	// Upload to bucket
	writer, err := bucket.NewWriter(ctx, s.ObjectName, nil)
	if err != nil {
		return fmt.Errorf("failed to create bucket writer: %w", err)
	}

	if _, err := io.Copy(writer, localFile); err != nil {
		writer.Close()
		return fmt.Errorf("failed to upload state file content: %w", err)
	}

	if err := writer.Close(); err != nil {
		return fmt.Errorf("failed to close bucket writer: %w", err)
	}

	progress.Printf("Successfully uploaded state file to bucket\n")
	return nil
}
