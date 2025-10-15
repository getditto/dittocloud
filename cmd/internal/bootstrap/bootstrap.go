package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fatih/color"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"

	"github.com/getditto/ditto-cloud-bootstrap/cmd/internal/log"
	"github.com/getditto/ditto-cloud-bootstrap/terraform"
)

// Constants for user confirmation responses
const (
	ConfirmYes     = "y"
	ConfirmYesFull = "yes"
	ConfirmNo      = "n"
	ConfirmNoFull  = "no"
	StateFile      = "terraform.tfstate"
)

// ValidConfirmationResponses contains all valid user confirmation responses
var ValidConfirmationResponses = []string{ConfirmYes, ConfirmYesFull, ConfirmNo, ConfirmNoFull}

// ProviderConfig holds the configuration for a specific cloud provider
type ProviderConfig interface {
	BuildTFVars() []*tfexec.VarOption
	BucketURL() (string, error)
	GetBackendConfig() (TerraformBackendConfig, error)
}

func BootstrapCmd() *cobra.Command {
	// configuration initialization for all providers, scoped to this functions closure.
	// config is set to a specific provider config based on the subcommand during BootstrapCmd execution.
	awsConfig := &AWSConfig{}
	gcpConfig := &GCPConfig{}
	var config ProviderConfig
	var logLevel string

	header := color.New(color.FgCyan, color.Bold)
	progress := color.New(color.FgMagenta)
	cmd := &cobra.Command{
		Use:   "bootstrap",
		Short: "Bootstrap a cloud provider",
		Long:  "Bootstrap a cloud provider",
		// Persistent methods run in the context of the subcommand, not the root command,
		// so the cloud provider specifc context is available here.
		// The cloud provider specific operations are handled in the subcommand.
		// Common operations are handled here.
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			// Setup logger first
			logger := log.Setup(logLevel)
			ctx := log.WithLogger(cmd.Context(), logger)
			cmd.SetContext(ctx)

			// Log the start of bootstrap
			logger.Debug("Starting Ditto Cloud Bootstrap", "command", cmd.Name())

			header.Println("══════════════════════════════════════════════════")
			header.Println("               Ditto Cloud Bootstrap")
			header.Println("══════════════════════════════════════════════════")
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) error {
			logger := log.FromContext(cmd.Context())
			color.NoColor = cmd.Flag("no-color").Value.String() == "true"
			// Copy the packaged terrafrom files into a temporary directory
			tmpDir, err := os.MkdirTemp(os.TempDir(), "ditto-cloud-bootstrap")
			if err != nil {
				return fmt.Errorf("unable to create temporary directory: %w", err)
			}
			if cmd.Flag("remove-tmpdir").Value.String() == "true" {
				defer os.RemoveAll(tmpDir)
			}

			progress.Printf("Copying terraform files to temporary directory %q\n", tmpDir)
			if err := os.CopyFS(tmpDir, terraform.TerraformFiles); err != nil {
				return fmt.Errorf("unable to copy terraform files: %w", err)
			}
			// Change permissions so that the script can write to the directory
			if err := os.Chmod(tmpDir, 0700); err != nil {
				return fmt.Errorf("unable to change permissions on temporary directory: %w", err)
			}

			// provider is the subcommand name
			provider := cmd.Name()
			workingDir := filepath.Join(tmpDir, provider)
			logger.Debug("Working directory for provider", "provider", provider, "workingDir", workingDir)
			progress.Printf("Using %q provider\n", provider)

			localStateFilePath := cmd.Flag("state").Value.String()

			// initialize provider config based on the subcommand
			switch provider {
			case "aws":
				config = awsConfig
			case "gcp":
				config = gcpConfig
			default:
				return fmt.Errorf("unsupported provider: %s", provider)
			}

			// Convert provider config to terraform vars
			vars := config.BuildTFVars()

			var execPath string

			// this will be set to true if a valid terraform executable is not found
			shouldDownload := cmd.Flag("force-terraform-download").Value.String() == "true"

			execPath, err = getTerraform(cmd.Context(), shouldDownload)
			if err != nil {
				return fmt.Errorf("terraform executable not available: %w", err)
			}
			tf, err := tfexec.NewTerraform(workingDir, execPath)
			if err != nil {
				return fmt.Errorf("unable to create terraform instance: %w", err)
			}

			// Create a state manager and initialize Terraform with appropriate backend
			stateManager := NewStateManager(cmd.Context(), config, workingDir, tf, localStateFilePath)

			if err := stateManager.InitializeWithBackend(cmd.Context()); err != nil {
				return fmt.Errorf("unable to initialize terraform with backend: %w", err)
			}

			progress.Println("Running terraform plan...")

			// Check if debug logging is enabled to show detailed plan output
			showDetailedPlan := logger.Enabled(cmd.Context(), slog.LevelDebug)

			if showDetailedPlan {
				// For debug mode, configure terraform to show output to user
				logger.Debug("Debug mode enabled - showing detailed terraform plan output")
				tf.SetStdout(os.Stdout)
				tf.SetStderr(os.Stderr)

				// Show the human-readable plan
				planChanged, err := tf.Plan(cmd.Context(), toPlanOptions(vars)...)
				if err != nil {
					return fmt.Errorf("unable to run terraform plan: %w", err)
				}

				if !planChanged {
					color.Green("\n✅ No changes detected. Infrastructure is up to date.\n")
					if err := showOutputs(cmd.Context(), tf); err != nil {
						return err
					}
					return nil
				}
				color.Yellow("\n📋 Changes detected and will be applied.\n")
			} else {
				// For normal operation, suppress terraform output and just check if changes exist
				tf.SetStdout(io.Discard)
				tf.SetStderr(io.Discard)

				planChanged, err := tf.Plan(cmd.Context(), toPlanOptions(vars)...)
				if err != nil {
					return fmt.Errorf("unable to run terraform plan: %w", err)
				}

				if !planChanged {
					color.Green("\n✅ No changes detected. Infrastructure is up to date.\n")
					if err := showOutputs(cmd.Context(), tf); err != nil {
						return err
					}
					return nil
				}
				color.Yellow("\n📋 Terraform Plan Summary:")
				color.Yellow("Changes have been detected and will be applied.")
				color.Yellow("Use --log-level debug to see detailed plan output.\n")
			}

			if cmd.Flag("dry-run").Value.String() == "true" {
				progress.Println("Terraform plan complete. Run command without `--dry-run` to apply the changes.")
				return nil
			}

			// Get user confirmation before applying changes
			if !getUserConfirmation("Are you sure you want to apply these changes?", progress) {
				progress.Println("Aborting...")
				return nil
			}

			defer func() {
				// Handle state file transfer back and check for remote backend migration
				stateManager.FinalizeStateTransfer(cmd.Context())
			}()

			progress.Println("Running terraform apply...")
			tf.SetStdout(os.Stdout) // Always show apply output
			tf.SetStderr(os.Stderr)
			if err := tf.Apply(cmd.Context(), toApplyOptions(vars)...); err != nil {
				return fmt.Errorf("unable to run terraform apply: %w", err)
			}

			if err := showOutputs(cmd.Context(), tf); err != nil {
				return err
			}

			return nil
		},
	}
	cmd.PersistentFlags().Bool("dry-run", false, "Run terraform plan instead of terraform apply")
	cmd.PersistentFlags().Bool("no-color", false, "Disable color output")
	cmd.PersistentFlags().String("state", "terraform.tfstate", "Path to the terraform state file")
	cmd.PersistentFlags().Bool("remove-tmpdir", true, "Remove the temporary directory after running")
	cmd.PersistentFlags().StringVar(&logLevel, "log-level", "info", "Set the log level")
	cmd.PersistentFlags().Bool("force-terraform-download", false, "Force download terraform")

	// The subcommands will handle cloud provider specific variables and populate the config
	cmd.AddCommand(awsCmd(awsConfig))
	cmd.AddCommand(gcpCmd(gcpConfig))
	return cmd
}

// toPlanOptions converts VarOptions to PlanOptions
func toPlanOptions(vars []*tfexec.VarOption) []tfexec.PlanOption {
	planOpts := make([]tfexec.PlanOption, len(vars))
	for i, v := range vars {
		planOpts[i] = v
	}
	return planOpts
}

// toApplyOptions converts VarOptions to ApplyOptions
func toApplyOptions(vars []*tfexec.VarOption) []tfexec.ApplyOption {
	applyOpts := make([]tfexec.ApplyOption, len(vars))
	for i, v := range vars {
		applyOpts[i] = v
	}
	return applyOpts
}

// Prompt prompts the user for a value and returns it.
func StringPrompt(label string, def string) string {
	prompt := color.New(color.FgHiWhite, color.Bold)
	var value string
	if def != "" {
		prompt.Printf("%s (default: %s): ", label, color.WhiteString(def))
	} else {
		prompt.Printf("%s: ", label)
	}
	_, _ = fmt.Scanln(&value)
	value = strings.TrimSpace(value)
	if value == "" {
		value = def
	}
	return value
}

// OptionsPrompt prompts the user for a value from a list of options,
// if the user enters an invalid option, it will prompt again
// until a valid option is entered.
func OptionsPrompt(label string, options []string) string {
	prompt := color.New(color.FgHiWhite, color.Bold)
	failed := color.New(color.FgRed)
	var value string
	for {
		prompt.Printf("%s %s: ", label, color.WhiteString("%v", options))
		_, err := fmt.Scanln(&value)
		if err != nil {
			return ""
		}
		if slices.Contains(options, value) {
			return value
		}
		failed.Println("Invalid option, please try again.")
	}
}

// FlagOrPrompt checks if the flag is set, if it is, it returns the value of the flag,
// otherwise it prompts the user for a value and returns that.
func FlagOrPrompt(flag *pflag.Flag, label string, def string) string {
	if flag.Changed {
		return flag.Value.String()
	}
	return StringPrompt(label, def)
}

// getUserConfirmation prompts the user for confirmation and returns true for yes, false for no
// Only accepts yes/no inputs and re-prompts if invalid input is provided
func getUserConfirmation(message string, progress *color.Color) bool {
	color.White("%s", color.New(color.Bold).Sprint(message))
	for {
		response := StringPrompt("(y/n)", "")
		switch response {
		case ConfirmNo, ConfirmNoFull:
			return false
		case ConfirmYes, ConfirmYesFull:
			return true
		default:
			progress.Println("Only \"y\" or \"n\" inputs are accepted.")
		}
	}
}

// showOutputs will pretty-print the TF outputs as JSON
func showOutputs(ctx context.Context, tf *tfexec.Terraform) error {
	output, err := tf.Output(ctx)
	if err != nil {
		return fmt.Errorf("unable to get terraform output: %w", err)
	}
	color.Green("Terraform output:")
	for k, v := range output {
		raw, err := v.Value.MarshalJSON()
		if err != nil {
			return fmt.Errorf("unable to marshal terraform output value for %s: %w", k, err)
		}
		var m any

		err = json.Unmarshal(raw, &m)
		if err != nil {
			return fmt.Errorf("unable to unmarshal terraform output: %w", err)
		}
		color.Green("%s: %s", color.New(color.Bold).Sprint(k), raw)
	}
	return nil
}
