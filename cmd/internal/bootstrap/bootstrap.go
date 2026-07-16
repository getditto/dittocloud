package bootstrap

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/fatih/color"
	"github.com/getditto/dittocloud/cmd/internal/log"
	"github.com/getditto/dittocloud/terraform"
	"github.com/hashicorp/terraform-exec/tfexec"
	tfjson "github.com/hashicorp/terraform-json"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// TerraformExecutor is an interface that abstracts terraform operations for testing
type TerraformExecutor interface {
	Init(context.Context, ...tfexec.InitOption) error
	Import(context.Context, string, string, ...tfexec.ImportOption) error
	Plan(context.Context, ...tfexec.PlanOption) (bool, error)
	ShowPlanFile(context.Context, string, ...tfexec.ShowOption) (*tfjson.Plan, error)
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

var terraformApplyPrompt = StringPrompt

type resourceImport struct {
	address string
	id      string
}

func BootstrapCmd() *cobra.Command {
	// Shared variables for all providers, scoped to this functions closure. At least they aren't globals.
	var vars []*tfexec.VarOption
	var logLevel string
	var tfVars []string
	var resourceImports []string

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

			operation := "bootstrap " + cmd.Name()
			if flag := cmd.Flags().Lookup("generate-scopes-file"); flag != nil && flag.Value.String() == "true" {
				operation += " generate legacy scopes file"
			} else if len(resourceImports) > 0 {
				operation += " import"
			} else if cmd.Flag("dry-run").Value.String() == "true" {
				operation += " dry-run"
			}
			operationLock, err := acquireStateOperationLock(cmd.Flag("state").Value.String(), operation)
			if err != nil {
				return err
			}
			setCommandOperationLock(cmd, operationLock)

			// Log the start of bootstrap
			logger.Debug(
				"Starting Ditto Cloud Bootstrap",
				"command", cmd.Name(),
				"state", operationLock.canonicalStatePath,
			)

			_, _ = header.Println("══════════════════════════════════════════════════")
			_, _ = header.Println("               Ditto Cloud Bootstrap              ")
			_, _ = header.Println("══════════════════════════════════════════════════")
			return nil
		},
		PersistentPostRunE: func(cmd *cobra.Command, args []string) (postRunErr error) {
			defer func() {
				if err := releaseCommandOperationLock(cmd); err != nil {
					postRunErr = errors.Join(postRunErr, fmt.Errorf("unable to release Dittocloud operation lock: %w", err))
				}
			}()
			if commandSkipsTerraformLifecycle(cmd) {
				return nil
			}

			logger := log.FromContext(cmd.Context())
			color.NoColor = cmd.Flag("no-color").Value.String() == "true"

			imports, err := parseResourceImports(resourceImports)
			if err != nil {
				return err
			}
			scopedImportConfiguration, scopedImport := commandAWSScopedImportConfiguration(cmd.Context())
			if scopedImport {
				imports = slices.Clone(scopedImportConfiguration.Imports)
			}
			importOnly := len(imports) > 0

			// Copy the packaged terrafrom files into a temporary directory
			tmpDir, err := os.MkdirTemp(os.TempDir(), "dittocloud")
			if err != nil {
				return fmt.Errorf("unable to create temporary directory: %w", err)
			}
			retainTmpDir := false
			if cmd.Flag("remove-tmpdir").Value.String() == "true" {
				defer func() {
					if !retainTmpDir {
						_ = os.RemoveAll(tmpDir)
					}
				}()
			}

			_, _ = progress.Printf("Copying terraform files to temporary directory %q\n", tmpDir)
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
			_, _ = progress.Printf("Using %q provider\n", provider)

			localStateFilePath := commandCanonicalStatePath(cmd)
			tmpStateFilePath := filepath.Join(workingDir, "terraform.tfstate")

			if _, err := os.Stat(localStateFilePath); err == nil {
				_, _ = progress.Printf("Copying local state file %q to temporary directory %q\n", localStateFilePath, workingDir)
				input, err := os.ReadFile(localStateFilePath)
				if err != nil {
					return fmt.Errorf("unable to read local state file: %w", err)
				}
				if err := os.WriteFile(tmpStateFilePath, input, 0600); err != nil {
					return fmt.Errorf("unable to write state file to temporary directory: %w", err)
				}
			} else if errors.Is(err, os.ErrNotExist) {
				_, _ = progress.Printf(
					"No local state file found, new state file will be created at %q\n",
					localStateFilePath,
				)
			} else {
				return fmt.Errorf("unable to inspect local state file %q: %w", localStateFilePath, err)
			}

			var execPath string

			// this will be set to true if a valid terraform executable is not found
			shouldDownload := cmd.Flag("force-terraform-download").Value.String() == "true"

			execPath, err = GetTerraform(cmd.Context(), shouldDownload)
			if err != nil {
				return fmt.Errorf("terraform executable not available: %w", err)
			}
			tf, err := terraformFactory(workingDir, execPath)
			if err != nil {
				return fmt.Errorf("unable to create terraform instance: %w", err)
			}
			_, _ = progress.Println("Initializing terraform...")
			if err := tf.Init(cmd.Context(), tfexec.Upgrade(true)); err != nil {
				return fmt.Errorf("unable to initialize terraform: %w", err)
			}

			var scopedImportBackup terraformMigrationBackup
			if scopedImport {
				importAddresses := make([]string, 0, len(imports))
				for _, resource := range imports {
					importAddresses = append(importAddresses, resource.address)
				}
				scopedImportBackup, err = createTerraformImportBackup(
					localStateFilePath,
					scopedImportConfiguration.OriginalState,
					scopedImportConfiguration.TerraformState,
					importAddresses,
					[]byte(scopedImportConfiguration.EncodedScopes),
				)
				if err != nil {
					return fmt.Errorf("unable to create scope-mode import backup: %w", err)
				}
				_, _ = progress.Printf(
					"Created pre-import state backup %q and manifest %q.\n",
					scopedImportBackup.StatePath,
					scopedImportBackup.ManifestPath,
				)
			}

			// Parse and append any --tf-var flags to the vars slice
			for _, tfVar := range tfVars {
				if !strings.Contains(tfVar, "=") {
					return fmt.Errorf("invalid --tf-var format %q: must be in key=value format", tfVar)
				}
				vars = append(vars, tfexec.Var(tfVar))
			}

			for _, resource := range imports {
				_, _ = progress.Printf("Importing Terraform resource %q...\n", resource.address)
				if err := tf.Import(cmd.Context(), resource.address, resource.id, toImportOptions(vars)...); err != nil {
					if scopedImport {
						return fmt.Errorf(
							"unable to import Terraform resource %q: %w; pre-import state backup remains at %q",
							resource.address,
							err,
							scopedImportBackup.StatePath,
						)
					}
					return fmt.Errorf("unable to import Terraform resource %q: %w", resource.address, err)
				}
				if scopedImport {
					if err := validateAWSScopedImportRegistry(tmpStateFilePath, scopedImportConfiguration.Scopes); err != nil {
						retainTmpDir = true
						return fmt.Errorf(
							"imported Terraform resource %q produced invalid scope registry state: %w; temporary state retained at %q and pre-import backup remains at %q",
							resource.address,
							err,
							tmpStateFilePath,
							scopedImportBackup.StatePath,
						)
					}
				}
				if err := persistTerraformState(tmpStateFilePath, localStateFilePath); err != nil {
					retainTmpDir = true
					if scopedImport {
						return fmt.Errorf(
							"unable to save state after importing Terraform resource %q: %w; pre-import state backup remains at %q",
							resource.address,
							err,
							scopedImportBackup.StatePath,
						)
					}
					return fmt.Errorf("unable to save state after importing Terraform resource %q: %w", resource.address, err)
				}
			}
			if importOnly {
				_, _ = progress.Printf("Imported %d Terraform resource(s) and saved state to %q.\n", len(imports), localStateFilePath)
			}

			_, _ = progress.Println("Running terraform plan...")

			// Import workflows always show the post-import plan. Other workflows show
			// detailed plan output only when debug logging is enabled.
			showDetailedPlan := importOnly || logger.Enabled(cmd.Context(), slog.LevelDebug)

			if showDetailedPlan {
				// For debug mode, configure terraform to show output to user
				if importOnly {
					_, _ = progress.Println("Showing detailed post-import Terraform plan...")
				} else {
					logger.Debug("Debug mode enabled - showing detailed terraform plan output")
				}
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
				if importOnly {
					color.Yellow("\n📋 Changes detected. The import workflow will not apply them.\n")
				} else {
					color.Yellow("\n📋 Changes detected and will be applied.\n")
				}
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

			if importOnly {
				_, _ = progress.Println("Terraform import workflow complete. Review the plan, then rerun without --import-resource to apply any changes.")
				return nil
			}

			if cmd.Flag("dry-run").Value.String() == "true" {
				_, _ = progress.Println("Terraform plan complete. Run command without `--dry-run` to apply the changes.")
				return nil
			}

			// Only accept yes/no as inputs and re-prompt if it wasn't provided
			// to prevent errant ENTER smashes as an approval.
			color.White("%s", color.New(color.Bold).Sprint("Are you sure you want to apply these changes?"))
			for {
				v := terraformApplyPrompt("(y/n)", "")
				if v == "n" || v == "no" {
					_, _ = progress.Println("Aborting...")
					return nil
				}
				if v == "y" || v == "yes" {
					break
				}
				_, _ = progress.Println("Only \"y\" or \"n\" inputs are accepted.")
			}

			_, _ = progress.Println("Running terraform apply...")
			applyErr := tf.Apply(cmd.Context(), toApplyOptions(vars)...)

			var outputErr error
			if applyErr == nil {
				outputErr = showOutputs(cmd.Context(), tf)
			}

			_, _ = progress.Printf("Persisting Terraform state to %q\n", localStateFilePath)
			persistenceErr := persistTerraformState(tmpStateFilePath, localStateFilePath)
			if persistenceErr != nil {
				retainTmpDir = true
			}

			if applyErr != nil {
				wrappedApplyErr := fmt.Errorf(
					"unable to run terraform apply: %w; partial Terraform state was saved to %q",
					applyErr,
					localStateFilePath,
				)
				if persistenceErr != nil {
					wrappedApplyErr = fmt.Errorf("unable to run terraform apply: %w", applyErr)
				}
				return errors.Join(wrappedApplyErr, persistenceErr)
			}
			return errors.Join(outputErr, persistenceErr)
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
	cmd.PersistentFlags().StringArrayVar(
		&resourceImports,
		"import-resource",
		[]string{},
		"Import an existing resource into state, then plan without applying (repeatable; address=id; modifies state)",
	)

	// The subcommands will handle cloud provider specific variables and mutate the list of vars to be passed to terraform plan/apply
	cmd.AddCommand(awsCmd(&vars))
	cmd.AddCommand(gcpCmd(&vars))
	return cmd
}

func parseResourceImports(values []string) ([]resourceImport, error) {
	imports := make([]resourceImport, 0, len(values))
	seenAddresses := make(map[string]struct{}, len(values))

	for _, value := range values {
		address, id, found := strings.Cut(value, "=")
		address = strings.TrimSpace(address)
		id = strings.TrimSpace(id)
		if !found || address == "" || id == "" {
			return nil, fmt.Errorf("invalid --import-resource value %q: expected address=id with non-empty values", value)
		}
		if _, found := seenAddresses[address]; found {
			return nil, fmt.Errorf("duplicate --import-resource address %q", address)
		}

		seenAddresses[address] = struct{}{}
		imports = append(imports, resourceImport{address: address, id: id})
	}

	return imports, nil
}

func toPlanOptions(vars []*tfexec.VarOption) []tfexec.PlanOption {
	planOpts := make([]tfexec.PlanOption, len(vars))
	for i, v := range vars {
		planOpts[i] = v
	}
	return planOpts
}

func toImportOptions(vars []*tfexec.VarOption) []tfexec.ImportOption {
	importOpts := make([]tfexec.ImportOption, len(vars))
	for i, v := range vars {
		importOpts[i] = v
	}
	return importOpts
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
		_, _ = prompt.Printf("%s (default: %s): ", label, color.WhiteString(def))
	} else {
		_, _ = prompt.Printf("%s: ", label)
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
		_, _ = prompt.Printf("%s %s: ", label, color.WhiteString("%v", options))
		_, err := fmt.Scanln(&value)
		if err != nil {
			return ""
		}
		if slices.Contains(options, value) {
			return value
		}
		_, _ = failed.Println("Invalid option, please try again.")
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
