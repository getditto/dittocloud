package privatenetworking

import (
	"context"
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

// TerraformExecutor is an interface that abstracts terraform operations for testing
type TerraformExecutor interface {
	Init(context.Context, ...tfexec.InitOption) error
	Plan(context.Context, ...tfexec.PlanOption) (bool, error)
	Apply(context.Context, ...tfexec.ApplyOption) error
	Destroy(context.Context, ...tfexec.DestroyOption) error
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

// terraformPathFinder resolves the terraform executable path (can be replaced in tests)
var terraformPathFinder = bootstrap.GetTerraform

func PrivateNetworkingCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "private-networking",
		Short: "Manage private networking for Big Peer deployments (AWS only)",
		Long: `Manage VPC Endpoint Services and VPC Endpoints for private networking access to Big Peer deployments.

NOTE: This command is only supported for AWS deployments. GCP is not supported.`,
		PersistentPreRun: func(cmd *cobra.Command, args []string) {
			noColor, err := cmd.Flags().GetBool("no-color")
			if err == nil {
				color.NoColor = noColor
			}
		},
	}

	cmd.PersistentFlags().Bool("no-color", false, "Disable color output")

	cmd.AddCommand(EndpointServiceCmd())
	cmd.AddCommand(EndpointCmd())

	return cmd
}

// lifecycleConfig holds the per-command parameters for runTerraformLifecycle.
type lifecycleConfig struct {
	tmpDirPrefix    string
	terraformSubDir string
	workingDirLabel string
	destroyWarning  string
	destroySuccess  string
	showOutputsFn   func(context.Context, TerraformExecutor, *color.Color, *color.Color) error
	afterApplyFn    func(*color.Color) // optional; called after showOutputsFn on a successful apply
}

// runTerraformLifecycle handles tmpdir setup, state management, and the full
// terraform init → plan → apply (or destroy) flow. Command-specific behaviour
// is supplied via cfg.
func runTerraformLifecycle(cmd *cobra.Command, vars []*tfexec.VarOption, cfg lifecycleConfig) error {
	progress := color.New(color.FgMagenta)
	success := color.New(color.FgGreen, color.Bold)
	failure := color.New(color.FgRed, color.Bold)
	logger := log.FromContext(cmd.Context())

	destroyMode := cmd.Flag("destroy").Value.String() == "true"

	tmpDir, err := os.MkdirTemp(os.TempDir(), cfg.tmpDirPrefix)
	if err != nil {
		return fmt.Errorf("unable to create temporary directory: %w", err)
	}
	if cmd.Flag("remove-tmpdir").Value.String() == "true" {
		defer func() {
			if err := os.RemoveAll(tmpDir); err != nil {
				slog.Warn("unable to remove temporary directory", "tmpDir", tmpDir, "error", err)
			}
		}()
	}

	progress.Printf("Copying terraform files to temporary directory %q\n", tmpDir)
	if err := os.CopyFS(tmpDir, terraform.TerraformFiles); err != nil {
		return fmt.Errorf("unable to copy terraform files: %w", err)
	}
	if err := os.Chmod(tmpDir, 0700); err != nil {
		return fmt.Errorf("unable to change permissions on temporary directory: %w", err)
	}

	workingDir := filepath.Join(tmpDir, cfg.terraformSubDir)
	progress.Printf("Using %s in %q\n", cfg.workingDirLabel, workingDir)

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
		progress.Printf("No local state file found, new state file will be created at %q\n", localStateFilePath)
	}

	shouldDownload := cmd.Flag("force-terraform-download").Value.String() == "true"
	execPath, err := terraformPathFinder(cmd.Context(), shouldDownload)
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

	autoApprove := cmd.Flag("yes").Value.String() == "true"

	if destroyMode {
		color.Red("\n⚠️  WARNING: %s\n", cfg.destroyWarning)
		if !autoApprove {
			color.White("%s", color.New(color.Bold).Sprint("Are you sure you want to destroy all resources?"))
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
		}

		defer func() {
			progress.Printf("Copying state file back to %q\n", localStateFilePath)
			stateFileData, err := os.ReadFile(tmpStateFilePath)
			if err != nil {
				failure.Printf("unable to read state file from temporary directory: %v", err)
				return
			}
			if err := os.WriteFile(localStateFilePath, stateFileData, 0600); err != nil {
				failure.Printf("unable to write state file to %q: %v", localStateFilePath, err)
			}
		}()

		destroyOpts := make([]tfexec.DestroyOption, len(vars))
		for i, v := range vars { destroyOpts[i] = v }
		progress.Println("Running terraform destroy...")
		if err := tf.Destroy(cmd.Context(), destroyOpts...); err != nil {
			return fmt.Errorf("unable to run terraform destroy: %w", err)
		}
		success.Println(cfg.destroySuccess)
		return nil
	}

	progress.Println("Running terraform plan...")

	showDetailedPlan := logger.Enabled(cmd.Context(), slog.LevelDebug)
	if showDetailedPlan {
		logger.Debug("Debug mode enabled - showing detailed terraform plan output")
		tf.SetStdout(os.Stdout)
		tf.SetStderr(os.Stderr)
	} else {
		tf.SetStdout(io.Discard)
		tf.SetStderr(io.Discard)
	}

	planOpts := make([]tfexec.PlanOption, len(vars))
	for i, v := range vars { planOpts[i] = v }
	planChanged, err := tf.Plan(cmd.Context(), planOpts...)
	if err != nil {
		return fmt.Errorf("unable to run terraform plan: %w", err)
	}

	if !planChanged {
		color.Green("\n✅ No changes detected. Infrastructure is up to date.\n")
		return cfg.showOutputsFn(cmd.Context(), tf, success, failure)
	}

	if showDetailedPlan {
		color.Yellow("\n📋 Changes detected and will be applied.\n")
	} else {
		color.Yellow("\n📋 Terraform Plan Summary:")
		color.Yellow("Changes have been detected and will be applied.")
		color.Yellow("Use --log-level debug to see detailed plan output.\n")
	}

	if cmd.Flag("dry-run").Value.String() == "true" {
		progress.Println("Terraform plan complete. Run command without `--dry-run` to apply the changes.")
		return nil
	}

	if !autoApprove {
		color.White("%s", color.New(color.Bold).Sprint("Are you sure you want to apply these changes?"))
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
	}

	defer func() {
		progress.Printf("Copying state file back to %q\n", localStateFilePath)
		stateFileData, err := os.ReadFile(tmpStateFilePath)
		if err != nil {
			failure.Printf("unable to read state file from temporary directory: %v", err)
			return
		}
		if err := os.WriteFile(localStateFilePath, stateFileData, 0600); err != nil {
			failure.Printf("unable to write state file to %q: %v", localStateFilePath, err)
		}
	}()

	applyOpts := make([]tfexec.ApplyOption, len(vars))
	for i, v := range vars { applyOpts[i] = v }
	progress.Println("Running terraform apply...")
	if err := tf.Apply(cmd.Context(), applyOpts...); err != nil {
		return fmt.Errorf("unable to run terraform apply: %w", err)
	}

	if err := cfg.showOutputsFn(cmd.Context(), tf, success, failure); err != nil {
		return err
	}
	if cfg.afterApplyFn != nil {
		cfg.afterApplyFn(success)
	}
	return nil
}

func EndpointServiceCmd() *cobra.Command {
	var logLevel string
	var tfVars []string

	header := color.New(color.FgCyan, color.Bold)

	cmd := &cobra.Command{
		Use:   "endpoint-service",
		Short: "Manage VPC Endpoint Service in BYOC account",
		Long: `Configure VPC Endpoint Service for private networking access to Big Peer deployments.

This command should be run after:
1. Running 'dittocloud bootstrap aws' to prepare the account
2. Deploying the Big Peer via Valet control plane

It will:
- Find the NLB associated with your Big Peer deployment
- Create a VPC Endpoint Service with auto-accept for the specified principal
- Configure private DNS name for the endpoint service
- Provide domain verification details for setting up TXT records`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.Setup(logLevel)
			cmd.SetContext(log.WithLogger(cmd.Context(), logger))
			logger.Debug("Starting Private Networking Setup", "command", cmd.Name())

			header.Println("══════════════════════════════════════════════════")
			header.Println("          VPC Endpoint Service Management         ")
			header.Println("══════════════════════════════════════════════════")

			destroyMode := cmd.Flag("destroy").Value.String() == "true"
			bigPeerName := bootstrap.FlagOrPrompt(cmd.Flags().Lookup("big-peer-name"), "Enter the Big Peer name", "")
			if bigPeerName == "" {
				return fmt.Errorf("big-peer-name is required")
			}

			awsProfile := cmd.Flags().Lookup("aws-profile").Value.String()
			awsRegion := cmd.Flags().Lookup("aws-region").Value.String()

			var vars []*tfexec.VarOption
			if destroyMode {
				vars = []*tfexec.VarOption{
					tfexec.Var("big_peer_name=" + bigPeerName),
					tfexec.Var("private_dns_name=placeholder.example.com"),
					tfexec.Var(`allowed_principals=["arn:aws:iam::000000000000:root"]`),
					tfexec.Var("profile=" + awsProfile),
				}
				if awsRegion != "" {
					vars = append(vars, tfexec.Var("region="+awsRegion))
				}
			} else {
				privateDNSName := bootstrap.FlagOrPrompt(cmd.Flags().Lookup("private-dns-name"), "Enter the private DNS name (FQDN)", "")
				if privateDNSName == "" {
					return fmt.Errorf("private-dns-name is required")
				}
				allowedPrincipalsStr := bootstrap.FlagOrPrompt(cmd.Flags().Lookup("allowed-principals"), "Enter the allowed principals, comma-separated (AWS account IDs, IAM role ARNs, or principal ARNs)", "")
				if allowedPrincipalsStr == "" {
					return fmt.Errorf("allowed-principals is required")
				}
				vars = []*tfexec.VarOption{
					tfexec.Var("big_peer_name=" + bigPeerName),
					tfexec.Var("private_dns_name=" + privateDNSName),
					tfexec.Var("allowed_principals=[" + formatPrincipals(allowedPrincipalsStr) + "]"),
					tfexec.Var("profile=" + awsProfile),
				}
				if awsRegion != "" {
					vars = append(vars, tfexec.Var("region="+awsRegion))
				}
			}

			for _, tfVar := range tfVars {
				if !strings.Contains(tfVar, "=") {
					return fmt.Errorf("invalid --tf-var format %q: must be in key=value format", tfVar)
				}
				vars = append(vars, tfexec.Var(tfVar))
			}

			return runTerraformLifecycle(cmd, vars, lifecycleConfig{
				tmpDirPrefix:    "dittocloud-endpoint-service",
				terraformSubDir: "aws/private_networking/vpc_endpoint_service",
				workingDirLabel: "AWS private networking module",
				destroyWarning:  "You are about to DESTROY the private networking infrastructure!",
				destroySuccess:  "\n✅ Private networking infrastructure successfully destroyed!",
				showOutputsFn:   showOutputs,
				afterApplyFn: func(success *color.Color) {
					success.Println("\n══════════════════════════════════════════════════")
					success.Println("          Domain Verification Required            ")
					success.Println("══════════════════════════════════════════════════")
					color.White("\nPlease provide the domain verification details shown above to Ditto.")
					color.White("Ditto will set up the required TXT record to verify domain ownership.\n")
				},
			})
		},
	}

	cmd.Flags().String("big-peer-name", "", "Name of the Big Peer deployment")
	cmd.Flags().String("private-dns-name", "", "Fully qualified domain name for the VPC Endpoint Service")
	cmd.Flags().String("allowed-principals", "", "Comma-separated AWS principals allowed to create endpoint connections")
	cmd.Flags().String("aws-profile", "", "AWS profile to use")
	cmd.Flags().String("aws-region", "", "AWS region (optional, will use default region if not specified)")
	cmd.Flags().Bool("dry-run", false, "Run terraform plan instead of terraform apply")
	cmd.Flags().Bool("destroy", false, "Destroy the private networking infrastructure")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompts (useful for automation)")
	cmd.Flags().String("state", "terraform-endpoint-service.tfstate", "Path to the terraform state file")
	cmd.Flags().Bool("remove-tmpdir", true, "Remove the temporary directory after running")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Set the log level")
	cmd.Flags().Bool("force-terraform-download", false, "Force download terraform")
	cmd.Flags().StringArrayVar(&tfVars, "tf-var", []string{}, "Pass arbitrary variables to terraform (can be specified multiple times)")
	_ = cmd.Flags().MarkHidden("tf-var")

	return cmd
}


// showOutputs pretty-prints TF outputs for the endpoint-service command.
func showOutputs(ctx context.Context, tf TerraformExecutor, success *color.Color, failure *color.Color) error {
	output, err := tf.Output(ctx)
	if err != nil {
		return fmt.Errorf("unable to get terraform output: %w", err)
	}

	success.Println("\n══════════════════════════════════════════════════")
	success.Println("            Private Networking Setup Complete      ")
	success.Println("══════════════════════════════════════════════════")

	if domainVerif, ok := output["domain_verification"]; ok {
		success.Println("\nDomain Verification Details:")
		success.Println("──────────────────────────────────────────────────")
		raw, _ := domainVerif.Value.MarshalJSON()
		color.Yellow("%s", string(raw))
		success.Println("──────────────────────────────────────────────────")
	}

	color.Green("\nAll Terraform Outputs:")
	for k, v := range output {
		raw, _ := v.Value.MarshalJSON()
		color.Green("%s: %s", color.New(color.Bold).Sprint(k), raw)
	}

	return nil
}

// showEndpointOutputs pretty-prints TF outputs for the endpoint command.
func showEndpointOutputs(ctx context.Context, tf TerraformExecutor, success *color.Color, failure *color.Color) error {
	output, err := tf.Output(ctx)
	if err != nil {
		return fmt.Errorf("unable to get terraform output: %w", err)
	}

	success.Println("\n══════════════════════════════════════════════════")
	success.Println("            VPC Endpoint Setup Complete            ")
	success.Println("══════════════════════════════════════════════════")

	if endpointOutput, ok := output["endpoint"]; ok {
		success.Println("\nVPC Endpoint Details:")
		success.Println("──────────────────────────────────────────────────")
		raw, _ := endpointOutput.Value.MarshalJSON()
		color.Cyan("%s", string(raw))
		success.Println("──────────────────────────────────────────────────")
	}

	color.Green("\nAll Terraform Outputs:")
	for k, v := range output {
		raw, _ := v.Value.MarshalJSON()
		color.Green("%s: %s", color.New(color.Bold).Sprint(k), raw)
	}

	return nil
}

func EndpointCmd() *cobra.Command {
	var logLevel string
	var tfVars []string

	header := color.New(color.FgCyan, color.Bold)

	cmd := &cobra.Command{
		Use:   "endpoint",
		Short: "Manage VPC Endpoint in customer account",
		Long: `Create VPC Endpoint in customer account to access the VPC Endpoint Service.

This command should be run in the customer's AWS account after:
1. Creating the VPC Endpoint Service using 'dittocloud private-networking endpoint-service'
2. Obtaining the service name from the endpoint service output

It will:
- Create a VPC Endpoint in the specified VPC and subnets
- Create a security group allowing inbound traffic from VPC CIDR
- Enable private DNS for seamless access
- Display endpoint details including connection status and ENI IDs`,
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.Setup(logLevel)
			cmd.SetContext(log.WithLogger(cmd.Context(), logger))
			logger.Debug("Starting VPC Endpoint Setup", "command", cmd.Name())

			header.Println("══════════════════════════════════════════════════")
			header.Println("            VPC Endpoint Management                ")
			header.Println("══════════════════════════════════════════════════")

			destroyMode := cmd.Flag("destroy").Value.String() == "true"
			awsProfile := cmd.Flags().Lookup("aws-profile").Value.String()
			awsRegion := cmd.Flags().Lookup("aws-region").Value.String()

			var vars []*tfexec.VarOption
			if destroyMode {
				vars = []*tfexec.VarOption{
					tfexec.Var("service_name=com.amazonaws.vpce.placeholder"),
					tfexec.Var("vpc_id=vpc-placeholder"),
					tfexec.Var("subnet_ids=[\"subnet-placeholder\"]"),
					tfexec.Var("private_dns_name=placeholder.example.com"),
					tfexec.Var("profile=" + awsProfile),
					tfexec.Var("region=" + awsRegion),
				}
			} else {
				serviceName := bootstrap.FlagOrPrompt(cmd.Flags().Lookup("service-name"), "Enter the VPC Endpoint Service name (e.g., com.amazonaws.vpce.us-east-2.vpce-svc-xxx)", "")
				if serviceName == "" {
					return fmt.Errorf("service-name is required")
				}
				vpcID := bootstrap.FlagOrPrompt(cmd.Flags().Lookup("vpc-id"), "Enter the VPC ID to deploy the endpoint into", "")
				if vpcID == "" {
					return fmt.Errorf("vpc-id is required")
				}
				subnetIDsStr := bootstrap.FlagOrPrompt(cmd.Flags().Lookup("subnet-ids"), "Enter comma-separated subnet IDs (e.g., subnet-xxx,subnet-yyy)", "")
				if subnetIDsStr == "" {
					return fmt.Errorf("subnet-ids is required")
				}
				privateDNSName := bootstrap.FlagOrPrompt(cmd.Flags().Lookup("private-dns-name"), "Enter the private DNS name (must match endpoint service DNS name)", "")
				if privateDNSName == "" {
					return fmt.Errorf("private-dns-name is required")
				}
				vars = []*tfexec.VarOption{
					tfexec.Var("service_name=" + serviceName),
					tfexec.Var("vpc_id=" + vpcID),
					tfexec.Var("subnet_ids=[" + formatSubnetIDs(subnetIDsStr) + "]"),
					tfexec.Var("private_dns_name=" + privateDNSName),
					tfexec.Var("profile=" + awsProfile),
				}
				if awsRegion != "" {
					vars = append(vars, tfexec.Var("region="+awsRegion))
				}
			}

			for _, tfVar := range tfVars {
				if !strings.Contains(tfVar, "=") {
					return fmt.Errorf("invalid --tf-var format %q: must be in key=value format", tfVar)
				}
				vars = append(vars, tfexec.Var(tfVar))
			}

			return runTerraformLifecycle(cmd, vars, lifecycleConfig{
				tmpDirPrefix:    "dittocloud-vpc-endpoint",
				terraformSubDir: "aws/private_networking/vpc_endpoint",
				workingDirLabel: "AWS VPC endpoint module",
				destroyWarning:  "You are about to DESTROY the VPC endpoint!",
				destroySuccess:  "\n✅ VPC endpoint successfully destroyed!",
				showOutputsFn:   showEndpointOutputs,
			})
		},
	}

	cmd.Flags().String("service-name", "", "VPC Endpoint Service name")
	cmd.Flags().String("vpc-id", "", "VPC ID to deploy the endpoint into")
	cmd.Flags().String("subnet-ids", "", "Comma-separated subnet IDs")
	cmd.Flags().String("private-dns-name", "", "Private DNS name (must match endpoint service)")
	cmd.Flags().String("aws-profile", "", "AWS profile to use")
	cmd.Flags().String("aws-region", "", "AWS region (optional, will use default region if not specified)")
	cmd.Flags().Bool("dry-run", false, "Run terraform plan instead of terraform apply")
	cmd.Flags().Bool("destroy", false, "Destroy the VPC endpoint infrastructure")
	cmd.Flags().Bool("yes", false, "Skip confirmation prompts (useful for automation)")
	cmd.Flags().String("state", "terraform-endpoint.tfstate", "Path to the terraform state file")
	cmd.Flags().Bool("remove-tmpdir", true, "Remove the temporary directory after running")
	cmd.Flags().StringVar(&logLevel, "log-level", "info", "Set the log level")
	cmd.Flags().Bool("force-terraform-download", false, "Force download terraform")
	cmd.Flags().StringArrayVar(&tfVars, "tf-var", []string{}, "Pass arbitrary variables to terraform (can be specified multiple times)")
	_ = cmd.Flags().MarkHidden("tf-var")

	return cmd
}

// formatSubnetIDs formats comma-separated subnet IDs into a Terraform list literal.
func formatSubnetIDs(subnetIDsStr string) string {
	return formatCommaSeparated(subnetIDsStr)
}

// formatPrincipals formats comma-separated AWS principals into a Terraform list literal.
func formatPrincipals(principalsStr string) string {
	return formatCommaSeparated(principalsStr)
}

func formatCommaSeparated(s string) string {
	parts := strings.Split(s, ",")
	quoted := make([]string, len(parts))
	for i, part := range parts {
		quoted[i] = "\"" + strings.TrimSpace(part) + "\""
	}
	return strings.Join(quoted, ",")
}
