# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Project Overview

A Go CLI tool that bootstraps cloud infrastructure (AWS and GCP) for Ditto deployment. The tool wraps embedded Terraform modules and provides an interactive CLI experience using Cobra.

## Important rules of conduct you should never violate.

1. NEVER RUN THIS COMMAND LOCALLY.  This command makes modifications to AWS and GCP accounts and is too dangerous to run locally. Instead, you should only run the tests.

## Common Commands

### Build
```bash
go build -o dittocloud ./cmd/dittocloud
```

### Test
```bash
go test ./...
```

## Architecture

### Code Structure

**CLI Layer** (`cmd/dittocloud/` and `cmd/internal/bootstrap/`):
- `main.go`: Root Cobra command setup
- `bootstrap.go`: Core bootstrap command with shared logic for all cloud providers
  - Handles Terraform lifecycle: init → plan → apply
  - Manages state file copying between local and temp directories
  - Implements interactive prompts and confirmation flows
  - Provider-agnostic orchestration
- `aws.go`: AWS-specific variable prompting and flag definitions
- `gcp.go`: GCP-specific variable prompting and flag definitions
- `install.go`: Terraform version management (downloads v1.11.4 if needed, caches in `~/.cache/dittocloud/terraform/`)

**Terraform Layer** (`terraform/`):
- `embed.go`: Embeds all Terraform files into the binary using `//go:embed`
- `aws/`: AWS VPC and cross-account IAM role modules
  - `vpc/`: VPC with subnets across availability zones
  - `cross_account_iam/`: CAPA controller, control plane, and node IAM roles
- `gcp/`: GCP networking, service accounts, and custom IAM roles
  - VPC with optional subnets and firewall rules
  - Tag-based IAM access control (uses resource tags to limit IAM permissions)
  - Service accounts for CAPG control plane and worker nodes
  - Custom roles for CAPG, Crossplane, and Velero

### Key Patterns

**Variable Flow**: User flags/prompts → `[]*tfexec.VarOption` slice → Terraform plan/apply

**State Management**: Local `terraform.tfstate` is copied to temp dir, modified during apply, then copied back

**Terraform Lifecycle**:
1. Copy embedded Terraform files to temp directory
2. Copy existing state file (if exists) to temp directory
3. Run `terraform init` and `terraform plan`
4. Prompt user for confirmation (unless `--dry-run`)
5. Run `terraform apply`
6. Copy state file back to local directory
7. Display outputs as JSON

**Interactive Prompts**: The `FlagOrPrompt()` helper checks if a flag was set; if not, prompts the user interactively

**Terraform Version**: Tool requires Terraform 1.11.4 (checks system, downloads if needed, caches for reuse)

## GCP-Specific Details

**Tag-based IAM Access Control**: The GCP module uses resource tags to restrict IAM permissions. All resources created by Ditto are tagged, and IAM roles include conditions that check for these tags. This prevents service accounts from accessing resources outside Ditto's management scope.

**VPC Configuration**: The `vpc_config` variable in GCP is passed as JSON and controls whether to create subnets, firewall rules, and secondary IP ranges for Kubernetes pods/services.

**Firewall Rules Module**: Firewall rules are created in a separate module (`terraform/gcp/firewall_rules`) to avoid conditional type errors when `create_default_firewall_rules` is false.

## AWS-Specific Details

**Cross-Account IAM**: The AWS module creates IAM roles that can be assumed by Ditto services running in a different AWS account (via trusted role ARNs).

**VPC Module**: Creates a VPC with subnets across multiple availability zones (requires region with at least 3 AZs).

## Testing

Only one test file exists: `cmd/internal/bootstrap/install_test.go` for Terraform installation logic.

### Testing Patterns

**Test Structure**:
- Use `t.Run()` for subtests with descriptive names that explain the scenario
- Follow setup → execute → verify pattern
- Initialize context with logger: `ctx := log.WithLogger(context.Background(), log.Setup("debug"))`

**Environment Isolation**:
- Create helper functions that return structs with cleanup functions
- `setupCleanEnvironment(t)` pattern:
  - Save original environment variables (e.g., PATH)
  - Clear or modify environment to isolate test
  - Use `t.TempDir()` for temporary directories
  - Return struct with `cleanup func()` field
  - Always call `defer cleanup()` immediately after setup

**Helper Functions**:
- Create reusable helpers for common setup tasks (e.g., `installSystemTerraform()`)
- Helpers should return structs with cleanup functions, not just values
- Example struct pattern:
  ```go
  type setupResult struct {
      field1  string
      cleanup func()
  }
  ```

**Verification Pattern**:
1. Check for errors: `if err != nil { t.Fatalf("unexpected error: %v", err) }`
2. Check for empty/nil results: `if result == "" { t.Fatal("expected non-empty result") }`
3. Verify functionality works (e.g., execute binary, call function)
4. Verify location/source using `strings.Contains()` for path checks

**Test Style**:
- When non-destructive, prefer integration tests with real operations over mocks
- Each test should be completely independent and isolated
- Use descriptive variable names and clear test scenario names

**Cleanup Management**:
- Always use `defer cleanup()` immediately after setup
- Cleanup functions should restore original state (environment variables, PATH, etc.)
- Use `defer` for all cleanup, even if test might fail
