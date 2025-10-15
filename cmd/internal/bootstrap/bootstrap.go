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
	"github.com/getditto/ditto-cloud-bootstrap/cmd/internal/log"
	"github.com/getditto/ditto-cloud-bootstrap/terraform"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TerraformExecutor is an interface that abstracts terraform operations for testing
type TerraformExecutor interface {
	Init(context.Context, ...tfexec.InitOption) error
	Plan(context.Context, ...tfexec.PlanOption) (bool, error)
	Apply(context.Context, ...tfexec.ApplyOption) error
	Output(context.Context, ...tfexec.OutputOption) (map[string]tfexec.OutputMeta, error)
	SetStdout(io.Writer)
	SetStderr(io.Writer)
}

// TerraformFactory creates a TerraformExecutor
type TerraformFactory func(workingDir string, execPath string) (TerraformExecutor, error)

// defaultTerraformFactory is the default factory that creates real terraform instances
var defaultTerraformFactory TerraformFactory = func(workingDir string, execPath string) (TerraformExecutor, error) {
	return tfexec.NewTerraform(workingDir, execPath)
}

// terraformFactory is the factory used by the code (can be replaced in tests)
var terraformFactory = defaultTerraformFactory

func BootstrapCmd() *cobra.Command {
	// Shared variables for all providers, scoped to this functions closure. At least they aren't globals.
	var vars []*tfexec.VarOption
	var logLevel string
	var tfVars []string

	header := color.New(color.FgCyan, color.Bold)
	progress := color.New(color.FgMagenta)
	failure := color.New(color.FgRed, color.Bold)
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
				defer os.Remove(tmpDir)
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
			progress.Printf("Using %q provider\n", provider)

			localStateFilePath := cmd.Flag("state").Value.String()
			tmpStateFilePath := filepath.Join(workingDir, "terraform.tfstate")

			if _, err := os.Stat(localStateFilePath); err == nil {
				progress.Printf("Copying local state file %q to temporary directory %q\n", localStateFilePath, workingDir)
				input, err := os.ReadFile(localStateFilePath)
				if err != nil {
					return fmt.Errorf("unable to read local state file: %w", err)
				}
				if err := os.WriteFile(tmpStateFilePath, input, 0600); err != nil {
					return fmt.Errorf("unable to write state file to temporary directory: %w", err)
				}
			} else {
				progress.Printf(
					"No local state file found, new state file will be created at %q\n",
					localStateFilePath,
				)
			}

			var execPath string

			// this will be set to true if a valid terraform executable is not found
			shouldDownload := cmd.Flag("force-terraform-download").Value.String() == "true"

			execPath, err = getTerraform(cmd.Context(), shouldDownload)
			if err != nil {
				return fmt.Errorf("terraform executable not available: %w", err)
			}
			tf, err := terraformFactory(workingDir, execPath)
			if err != nil {
				return fmt.Errorf("unable to create terraform instance: %w", err)
			}
			progress.Println("Initializing terraform...")
			if err := tf.Init(cmd.Context(), tfexec.Upgrade(true)); err != nil {
				return fmt.Errorf("unable to initialize terraform: %w", err)
			}

			// Parse and append any --tf-var flags to the vars slice
			for _, tfVar := range tfVars {
				if !strings.Contains(tfVar, "=") {
					return fmt.Errorf("invalid --tf-var format %q: must be in key=value format", tfVar)
				}
				vars = append(vars, tfexec.Var(tfVar))
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

			// Only accept yes/no as inputs and re-prompt if it wasn't provided
			// to prevent errant ENTER smashes as an approval.
			color.White("%s", color.New(color.Bold).Sprint("Are you sure you want to apply these changes?"))
			for {
				v := StringPrompt("(y/n)", "")
				if v == "n" || v == "no" {
					progress.Println("Aborting...")
					return nil
				}
				if v == "y" || v == "yes" {
					break
				}
				progress.Println("Only \"y\" or \"n\" inputs are accepted.")
			}

			defer func() {
				// Copy the state file back to the original location
				progress.Printf("Copying state file back to %q\n", localStateFilePath)
				stateFileData, err := os.ReadFile(tmpStateFilePath)
				if err != nil {
					failure.Printf("unable to read state file from temporary directory: %v", err)
				}
				if err := os.WriteFile(localStateFilePath, stateFileData, 0600); err != nil {
					failure.Printf("unable to write state file to %q: %v", localStateFilePath, err)
				}
			}()

			progress.Println("Running terraform apply...")
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
	cmd.PersistentFlags().StringArrayVar(&tfVars, "tf-var", []string{}, "Pass arbitrary variables to terraform (can be specified multiple times)")
	_ = cmd.PersistentFlags().MarkHidden("tf-var")

	// The subcommands will handle cloud provider specific variables and mutate the list of vars to be passed to terraform plan/apply
	cmd.AddCommand(awsCmd(&vars))
	cmd.AddCommand(gcpCmd(&vars))
	return cmd
}

func toPlanOptions(vars []*tfexec.VarOption) []tfexec.PlanOption {
	planOpts := make([]tfexec.PlanOption, len(vars))
	for i, v := range vars {
		planOpts[i] = v
	}
	return planOpts
}

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

// showOutputs will pretty-print the TF outputs as JSON
func showOutputs(ctx context.Context, tf TerraformExecutor) error {
	output, err := tf.Output(ctx)
	if err != nil {
		return fmt.Errorf("unable to get terraform output: %w", err)
	}
	color.Green("Terraform output:")
	for k, v := range output {
		raw, _ := v.Value.MarshalJSON()
		var m any

		err := json.Unmarshal(raw, &m)
		if err != nil {
			return fmt.Errorf("unable to unmarshal terraform output: %w", err)
		}
		color.Green("%s: %s", color.New(color.Bold).Sprint(k), raw)
	}
	return nil
}
