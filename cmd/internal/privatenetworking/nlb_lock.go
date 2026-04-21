package privatenetworking

import (
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"

	"github.com/fatih/color"
	"github.com/getditto/dittocloud/cmd/internal/bootstrap"
	"github.com/getditto/dittocloud/cmd/internal/log"
	"github.com/getditto/dittocloud/terraform"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
)

func LockNLBCmd() *cobra.Command {
	var logLevel string
	var tfVars []string

	header := color.New(color.FgCyan, color.Bold)
	progress := color.New(color.FgMagenta)
	failure := color.New(color.FgRed, color.Bold)
	success := color.New(color.FgGreen, color.Bold)

	cmd := &cobra.Command{
		Use:   "lock-nlb",
		Short: "Protect NLB from modification by adding IAM deny policies",
		Long: `Add IAM deny policies to CAPA roles to prevent modification or deletion of the NLB.

This command protects the Network Load Balancer associated with a Big Peer deployment
by attaching deny policies to the CAPA controller, control plane, and node IAM roles.

The deny policies prevent:
- Deletion of the load balancer
- Modification of load balancer attributes
- Changing load balancer subnets
- Deletion of load balancer network interfaces

This protection is necessary because the VPC Endpoint Service is permanently bound
to the NLB ARN. If Valet recreates or modifies the NLB, the endpoint service becomes
orphaned and customer connectivity breaks.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Setup logger
			logger := log.Setup(logLevel)
			ctx := log.WithLogger(cmd.Context(), logger)
			cmd.SetContext(ctx)

			logger.Debug("Starting NLB Protection", "command", cmd.Name())

			header.Println("══════════════════════════════════════════════════")
			header.Println("             NLB Protection - Lock                 ")
			header.Println("══════════════════════════════════════════════════")

			// Get Big Peer name
			bigPeerName := bootstrap.FlagOrPrompt(
				cmd.Flags().Lookup("big-peer-name"),
				"Enter the Big Peer name",
				"",
			)
			if bigPeerName == "" {
				return fmt.Errorf("big-peer-name is required")
			}

			// Get bootstrap state file path
			bootstrapStatePath := cmd.Flags().Lookup("bootstrap-state").Value.String()

			// Parse bootstrap state to get IAM role names
			progress.Printf("Reading bootstrap state from %q\n", bootstrapStatePath)
			roleNames, err := parseBootstrapState(bootstrapStatePath)
			if err != nil {
				return fmt.Errorf("failed to parse bootstrap state: %w", err)
			}

			progress.Printf("Found IAM roles:\n")
			progress.Printf("  - Controller: %s\n", roleNames.ControllerRole)
			progress.Printf("  - Control Plane: %s\n", roleNames.ControlPlaneRole)
			progress.Printf("  - Nodes: %s\n", roleNames.NodesRole)

			// Get AWS credentials
			awsProfile := cmd.Flags().Lookup("aws-profile").Value.String()
			awsRegion := cmd.Flags().Lookup("aws-region").Value.String()

			// Build terraform variables
			vars := []*tfexec.VarOption{
				tfexec.Var("big_peer_name=" + bigPeerName),
				tfexec.Var("capa_controller_role_name=" + roleNames.ControllerRole),
				tfexec.Var("capa_controlplane_role_name=" + roleNames.ControlPlaneRole),
				tfexec.Var("capa_nodes_role_name=" + roleNames.NodesRole),
				tfexec.Var("profile=" + awsProfile),
				tfexec.Var("region=" + awsRegion),
			}

			// Parse and append any --tf-var flags
			for _, tfVar := range tfVars {
				if !strings.Contains(tfVar, "=") {
					return fmt.Errorf("invalid --tf-var format %q: must be in key=value format", tfVar)
				}
				vars = append(vars, tfexec.Var(tfVar))
			}

			// Copy the packaged terraform files into a temporary directory
			tmpDir, err := os.MkdirTemp(os.TempDir(), "dittocloud-nlb-protection")
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
			if err := os.Chmod(tmpDir, 0700); err != nil {
				return fmt.Errorf("unable to change permissions on temporary directory: %w", err)
			}

			workingDir := filepath.Join(tmpDir, "aws", "private_networking", "nlb_protection")
			progress.Printf("Using AWS NLB protection module in %q\n", workingDir)

			// State file uses big peer name for tracking
			localStateFilePath := fmt.Sprintf("terraform-nlb-protection-%s.tfstate", bigPeerName)
			if customState := cmd.Flag("state").Value.String(); customState != "" {
				localStateFilePath = customState
			}
			tmpStateFilePath := filepath.Join(workingDir, "terraform.tfstate")

			if _, err := os.Stat(localStateFilePath); err == nil {
				return fmt.Errorf("NLB is already protected (state file exists: %s). Use unlock-nlb first if you want to re-lock", localStateFilePath)
			}

			var execPath string
			shouldDownload := cmd.Flag("force-terraform-download").Value.String() == "true"

			execPath, err = bootstrap.GetTerraform(cmd.Context(), shouldDownload)
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

			progress.Println("Running terraform plan...")

			showDetailedPlan := logger.Enabled(cmd.Context(), slog.LevelDebug)

			if showDetailedPlan {
				logger.Debug("Debug mode enabled - showing detailed terraform plan output")
				tf.SetStdout(os.Stdout)
				tf.SetStderr(os.Stderr)

				planChanged, err := tf.Plan(cmd.Context(), toPlanOptions(vars)...)
				if err != nil {
					return fmt.Errorf("unable to run terraform plan: %w", err)
				}

				if !planChanged {
					color.Yellow("\n⚠️  No changes detected - this is unexpected for lock operation\n")
					return nil
				}
				color.Yellow("\n📋 Changes detected and will be applied.\n")
			} else {
				tf.SetStdout(io.Discard)
				tf.SetStderr(io.Discard)

				planChanged, err := tf.Plan(cmd.Context(), toPlanOptions(vars)...)
				if err != nil {
					return fmt.Errorf("unable to run terraform plan: %w", err)
				}

				if !planChanged {
					color.Yellow("\n⚠️  No changes detected - this is unexpected for lock operation\n")
					return nil
				}
				color.Yellow("\n📋 Terraform Plan Summary:")
				color.Yellow("Deny policies will be attached to CAPA IAM roles.")
				color.Yellow("Use --log-level debug to see detailed plan output.\n")
			}

			if cmd.Flag("dry-run").Value.String() == "true" {
				progress.Println("Terraform plan complete. Run command without `--dry-run` to apply the changes.")
				return nil
			}

			// Confirmation prompt
			color.Yellow("\n⚠️  This will LOCK the NLB and prevent Valet from modifying it.")
			color.White("%s", color.New(color.Bold).Sprint("Are you sure you want to lock the NLB?"))
			for {
				v := bootstrap.StringPrompt("(y/n)", "")
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

			// Show outputs
			output, err := tf.Output(ctx)
			if err != nil {
				return fmt.Errorf("unable to get terraform output: %w", err)
			}

			success.Println("\n══════════════════════════════════════════════════")
			success.Println("             NLB Successfully Locked               ")
			success.Println("══════════════════════════════════════════════════")

			// Display protection summary
			if protectionSummary, ok := output["protection_summary"]; ok {
				success.Println("\nProtection Summary:")
				success.Println("──────────────────────────────────────────────────")
				raw, _ := protectionSummary.Value.MarshalJSON()
				color.Green("%s", string(raw))
				success.Println("──────────────────────────────────────────────────")
			}

			color.Green("\nAll Terraform Outputs:")
			for k, v := range output {
				raw, _ := v.Value.MarshalJSON()
				color.Green("%s: %s", color.New(color.Bold).Sprint(k), raw)
			}

			color.Yellow("\n✅ The NLB is now protected from modification.")
			color.Yellow("To unlock, run: dittocloud private-networking unlock-nlb --big-peer-name %s\n", bigPeerName)

			return nil
		},
	}

	cmd.Flags().String("big-peer-name", "", "Name of the Big Peer deployment")
	cmd.Flags().String("bootstrap-state", "terraform.tfstate", "Path to bootstrap Terraform state file")
	cmd.Flags().String("state", "", "Custom path for NLB protection state file (default: terraform-nlb-protection-<big-peer-name>.tfstate)")
	cmd.Flags().String("aws-profile", "", "AWS profile to use")
	cmd.Flags().String("aws-region", "", "AWS region")
	cmd.Flags().Bool("dry-run", false, "Run terraform plan instead of terraform apply")
	cmd.Flags().Bool("no-color", false, "Disable color output")
	cmd.Flags().Bool("remove-tmpdir", true, "Remove the temporary directory after running")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Set the log level")
	cmd.Flags().Bool("force-terraform-download", false, "Force download terraform")
	cmd.Flags().StringArrayVar(&tfVars, "tf-var", []string{}, "Pass arbitrary variables to terraform (can be specified multiple times)")
	_ = cmd.Flags().MarkHidden("tf-var")

	return cmd
}

func UnlockNLBCmd() *cobra.Command {
	var logLevel string
	var tfVars []string

	header := color.New(color.FgCyan, color.Bold)
	progress := color.New(color.FgMagenta)
	failure := color.New(color.FgRed, color.Bold)
	success := color.New(color.FgGreen, color.Bold)

	cmd := &cobra.Command{
		Use:   "unlock-nlb",
		Short: "Remove NLB protection by removing IAM deny policies",
		Long: `Remove IAM deny policies from CAPA roles to restore normal NLB management.

This command removes the protection added by lock-nlb, allowing Valet to manage
the Network Load Balancer again. Use this when you need to make changes to the
NLB or when decommissioning the private networking setup.`,
		RunE: func(cmd *cobra.Command, args []string) error {
			// Setup logger
			logger := log.Setup(logLevel)
			ctx := log.WithLogger(cmd.Context(), logger)
			cmd.SetContext(ctx)

			logger.Debug("Starting NLB Unlock", "command", cmd.Name())

			header.Println("══════════════════════════════════════════════════")
			header.Println("             NLB Protection - Unlock               ")
			header.Println("══════════════════════════════════════════════════")

			// Get Big Peer name
			bigPeerName := bootstrap.FlagOrPrompt(
				cmd.Flags().Lookup("big-peer-name"),
				"Enter the Big Peer name",
				"",
			)
			if bigPeerName == "" {
				return fmt.Errorf("big-peer-name is required")
			}

			// Get AWS credentials
			awsProfile := cmd.Flags().Lookup("aws-profile").Value.String()
			awsRegion := cmd.Flags().Lookup("aws-region").Value.String()

			// Build minimal terraform variables (for destroy, values don't matter much)
			vars := []*tfexec.VarOption{
				tfexec.Var("big_peer_name=" + bigPeerName),
				tfexec.Var("capa_controller_role_name=placeholder"),
				tfexec.Var("capa_controlplane_role_name=placeholder"),
				tfexec.Var("capa_nodes_role_name=placeholder"),
				tfexec.Var("profile=" + awsProfile),
				tfexec.Var("region=" + awsRegion),
			}

			// Parse and append any --tf-var flags
			for _, tfVar := range tfVars {
				if !strings.Contains(tfVar, "=") {
					return fmt.Errorf("invalid --tf-var format %q: must be in key=value format", tfVar)
				}
				vars = append(vars, tfexec.Var(tfVar))
			}

			// Copy the packaged terraform files into a temporary directory
			tmpDir, err := os.MkdirTemp(os.TempDir(), "dittocloud-nlb-protection")
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
			if err := os.Chmod(tmpDir, 0700); err != nil {
				return fmt.Errorf("unable to change permissions on temporary directory: %w", err)
			}

			workingDir := filepath.Join(tmpDir, "aws", "private_networking", "nlb_protection")
			progress.Printf("Using AWS NLB protection module in %q\n", workingDir)

			// State file uses big peer name for tracking
			localStateFilePath := fmt.Sprintf("terraform-nlb-protection-%s.tfstate", bigPeerName)
			if customState := cmd.Flags().Lookup("state").Value.String(); customState != "" {
				localStateFilePath = customState
			}
			tmpStateFilePath := filepath.Join(workingDir, "terraform.tfstate")

			if _, err := os.Stat(localStateFilePath); err != nil {
				return fmt.Errorf("NLB is not locked (state file not found: %s)", localStateFilePath)
			}

			progress.Printf("Copying local state file %q to temporary directory %q\n", localStateFilePath, workingDir)
			input, err := os.ReadFile(localStateFilePath)
			if err != nil {
				return fmt.Errorf("unable to read local state file: %w", err)
			}
			if err := os.WriteFile(tmpStateFilePath, input, 0600); err != nil {
				return fmt.Errorf("unable to write state file to temporary directory: %w", err)
			}

			var execPath string
			shouldDownload := cmd.Flag("force-terraform-download").Value.String() == "true"

			execPath, err = bootstrap.GetTerraform(cmd.Context(), shouldDownload)
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

			// Confirmation prompt
			color.Red("\n⚠️  WARNING: This will UNLOCK the NLB and allow Valet to modify it again!")
			color.White("%s", color.New(color.Bold).Sprint("Are you sure you want to unlock the NLB?"))
			for {
				v := bootstrap.StringPrompt("(y/n)", "")
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
				progress.Printf("Copying state file back to %q\n", localStateFilePath)
				stateFileData, err := os.ReadFile(tmpStateFilePath)
				if err != nil {
					failure.Printf("unable to read state file from temporary directory: %v", err)
				}
				if err := os.WriteFile(localStateFilePath, stateFileData, 0600); err != nil {
					failure.Printf("unable to write state file to %q: %v", localStateFilePath, err)
				}
			}()

			progress.Println("Running terraform destroy...")
			if err := tf.Destroy(cmd.Context(), toDestroyOptions(vars)...); err != nil {
				return fmt.Errorf("unable to run terraform destroy: %w", err)
			}

			success.Println("\n✅ NLB successfully unlocked! Valet can now manage the NLB again.")

			// Optionally remove the state file after successful destroy
			if err := os.Remove(localStateFilePath); err != nil {
				color.Yellow("\n⚠️  Warning: Could not remove state file %s: %v", localStateFilePath, err)
			} else {
				progress.Printf("Removed state file %s\n", localStateFilePath)
			}

			return nil
		},
	}

	cmd.Flags().String("big-peer-name", "", "Name of the Big Peer deployment")
	cmd.Flags().String("state", "", "Custom path for NLB protection state file (default: terraform-nlb-protection-<big-peer-name>.tfstate)")
	cmd.Flags().String("aws-profile", "", "AWS profile to use")
	cmd.Flags().String("aws-region", "", "AWS region")
	cmd.Flags().Bool("no-color", false, "Disable color output")
	cmd.Flags().Bool("remove-tmpdir", true, "Remove the temporary directory after running")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Set the log level")
	cmd.Flags().Bool("force-terraform-download", false, "Force download terraform")
	cmd.Flags().StringArrayVar(&tfVars, "tf-var", []string{}, "Pass arbitrary variables to terraform (can be specified multiple times)")
	_ = cmd.Flags().MarkHidden("tf-var")

	return cmd
}
