package bootstrap

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/getditto/dittocloud/cmd/internal/log"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
)

// mockTerraformExecutor records terraform operations for verification
type mockTerraformExecutor struct {
	// Call counts
	initCallCount   int
	importCallCount int
	planCallCount   int
	applyCallCount  int

	workingDir  string
	importCalls []mockImportCall
	importState []byte

	// Parsed variables from Plan() call
	PlanVars map[string]string

	// Return values
	planReturnChanged bool
	planReturnError   error
	importReturnError error
	applyReturnError  error
	outputReturn      map[string]tfexec.OutputMeta
}

type mockImportCall struct {
	address string
	id      string
	vars    map[string]string
}

func (m *mockTerraformExecutor) Init(ctx context.Context, opts ...tfexec.InitOption) error {
	m.initCallCount++
	return nil
}

func (m *mockTerraformExecutor) Import(ctx context.Context, address, id string, opts ...tfexec.ImportOption) error {
	m.importCallCount++

	vars := make(map[string]string)
	for _, opt := range opts {
		if key, value, ok := terraformVarOption(opt); ok {
			vars[key] = value
		}
	}
	m.importCalls = append(m.importCalls, mockImportCall{address: address, id: id, vars: vars})

	if m.importReturnError != nil {
		return m.importReturnError
	}
	if m.importState != nil {
		if err := os.WriteFile(filepath.Join(m.workingDir, "terraform.tfstate"), m.importState, 0600); err != nil {
			return err
		}
	}
	return nil
}

func (m *mockTerraformExecutor) Plan(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
	m.planCallCount++

	// Extract and parse variables from plan options
	m.PlanVars = make(map[string]string)
	for _, opt := range opts {
		if key, value, ok := terraformVarOption(opt); ok {
			m.PlanVars[key] = value
		}
	}

	return m.planReturnChanged, m.planReturnError
}

func (m *mockTerraformExecutor) Apply(ctx context.Context, opts ...tfexec.ApplyOption) error {
	m.applyCallCount++
	return m.applyReturnError
}

func (m *mockTerraformExecutor) Output(ctx context.Context, opts ...tfexec.OutputOption) (map[string]tfexec.OutputMeta, error) {
	return m.outputReturn, nil
}

func (m *mockTerraformExecutor) SetStdout(w io.Writer) {}

func (m *mockTerraformExecutor) SetStderr(w io.Writer) {}

func terraformVarOption(opt any) (string, string, bool) {
	varOpt, ok := opt.(*tfexec.VarOption)
	if !ok {
		return "", "", false
	}

	// Use reflection to access the unexported field containing "key=value".
	val := reflect.ValueOf(varOpt).Elem()
	if val.Kind() != reflect.Struct {
		return "", "", false
	}

	for i := 0; i < val.NumField(); i++ {
		field := val.Field(i)
		if field.Kind() != reflect.String || field.String() == "" {
			continue
		}
		parts := strings.SplitN(field.String(), "=", 2)
		if len(parts) == 2 {
			return parts[0], parts[1], true
		}
	}
	return "", "", false
}

// setupBootstrapTest creates a test environment with a mocked terraform executor
func setupBootstrapTest(t *testing.T, args []string) (*cobra.Command, *mockTerraformExecutor) {
	t.Helper()

	hasStatePath := false
	for _, arg := range args {
		if arg == "--state" || strings.HasPrefix(arg, "--state=") {
			hasStatePath = true
			break
		}
	}
	if !hasStatePath {
		args = append(slices.Clone(args), "--state="+filepath.Join(t.TempDir(), "terraform.tfstate"))
	}

	ctx := log.WithLogger(context.Background(), log.Setup("debug"))

	// Save and restore original terraform factory
	originalFactory := terraformFactory
	t.Cleanup(func() { terraformFactory = originalFactory })

	// Create mock terraform executor
	mock := &mockTerraformExecutor{
		planReturnChanged: true,
		outputReturn:      map[string]tfexec.OutputMeta{},
	}

	// Inject mock
	terraformFactory = func(workingDir, execPath string) (TerraformExecutor, error) {
		mock.workingDir = workingDir
		return mock, nil
	}

	// Create and configure command
	cmd := BootstrapCmd()
	cmd.SetContext(ctx)
	cmd.SetArgs(args)

	return cmd, mock
}

// assertCallCounts verifies that terraform methods were called the expected number of times
func assertCallCounts(t *testing.T, mock *mockTerraformExecutor, init, plan, apply int) {
	t.Helper()
	if mock.initCallCount != init {
		t.Errorf("expected %d Init call(s), got %d", init, mock.initCallCount)
	}
	if mock.planCallCount != plan {
		t.Errorf("expected %d Plan call(s), got %d", plan, mock.planCallCount)
	}
	if mock.applyCallCount != apply {
		t.Errorf("expected %d Apply call(s), got %d", apply, mock.applyCallCount)
	}
}

func TestParseResourceImports(t *testing.T) {
	t.Run("parses multiple imports and preserves equals signs in IDs", func(t *testing.T) {
		imports, err := parseResourceImports([]string{
			"aws_iam_policy.network=arn:aws:iam::123456789012:policy/network",
			"module.example.resource.item=id=with=equals",
		})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		want := []resourceImport{
			{address: "aws_iam_policy.network", id: "arn:aws:iam::123456789012:policy/network"},
			{address: "module.example.resource.item", id: "id=with=equals"},
		}
		if !reflect.DeepEqual(imports, want) {
			t.Fatalf("imports: got %#v, want %#v", imports, want)
		}
	})

	t.Run("rejects malformed imports", func(t *testing.T) {
		for _, value := range []string{"missing-delimiter", "=missing-address", "missing-id="} {
			_, err := parseResourceImports([]string{value})
			if err == nil || !strings.Contains(err.Error(), "expected address=id") {
				t.Errorf("value %q: expected address=id error, got %v", value, err)
			}
		}
	})

	t.Run("rejects duplicate addresses", func(t *testing.T) {
		_, err := parseResourceImports([]string{"aws_iam_policy.example=one", "aws_iam_policy.example=two"})
		if err == nil || !strings.Contains(err.Error(), "duplicate --import-resource address") {
			t.Fatalf("expected duplicate address error, got %v", err)
		}
	})
}

func TestBootstrap(t *testing.T) {
	t.Run("should import resources, persist state, show a plan, and never apply", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "terraform.tfstate")
		if err := os.WriteFile(statePath, []byte(`{"serial":1}`), 0600); err != nil {
			t.Fatalf("unable to create test state: %v", err)
		}

		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--state=" + statePath,
			"--import-resource=module.cross_account_iam[0].aws_iam_policy.capa_controller_network=arn:aws:iam::123456789012:policy/ditto-capa-controller-network-policy",
			"--import-resource=module.cross_account_iam[0].aws_iam_policy.capa_control_plane_tags=arn:aws:iam::123456789012:policy/control-plane-tags.cluster-api-provider-aws.sigs.k8s.io",
		})
		mock.importState = []byte(`{"serial":3}`)

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)
		if mock.importCallCount != 2 {
			t.Fatalf("expected 2 Import calls, got %d", mock.importCallCount)
		}

		wantAddresses := []string{
			"module.cross_account_iam[0].aws_iam_policy.capa_controller_network",
			"module.cross_account_iam[0].aws_iam_policy.capa_control_plane_tags",
		}
		for i, call := range mock.importCalls {
			if call.address != wantAddresses[i] {
				t.Errorf("import %d address: got %q, want %q", i, call.address, wantAddresses[i])
			}
			if call.vars["profile"] != "test-profile" {
				t.Errorf("import %d profile: got %q, want %q", i, call.vars["profile"], "test-profile")
			}
			if call.vars["region"] != "us-west-2" {
				t.Errorf("import %d region: got %q, want %q", i, call.vars["region"], "us-west-2")
			}
		}

		state, err := os.ReadFile(statePath)
		if err != nil {
			t.Fatalf("unable to read imported state: %v", err)
		}
		if got, want := string(state), `{"serial":3}`; got != want {
			t.Fatalf("persisted state: got %q, want %q", got, want)
		}
	})

	t.Run("should stop before plan when an import fails", func(t *testing.T) {
		statePath := filepath.Join(t.TempDir(), "terraform.tfstate")
		originalState := []byte(`{"serial":1}`)
		if err := os.WriteFile(statePath, originalState, 0600); err != nil {
			t.Fatalf("unable to create test state: %v", err)
		}

		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--state=" + statePath,
			"--import-resource=aws_iam_policy.example=arn:aws:iam::123456789012:policy/example",
		})
		mock.importReturnError = errors.New("resource is already managed")

		err := cmd.Execute()
		if err == nil || !strings.Contains(err.Error(), "unable to import Terraform resource") {
			t.Fatalf("expected import error, got %v", err)
		}
		assertCallCounts(t, mock, 1, 0, 0)
		if mock.importCallCount != 1 {
			t.Fatalf("expected 1 Import call, got %d", mock.importCallCount)
		}

		state, readErr := os.ReadFile(statePath)
		if readErr != nil {
			t.Fatalf("unable to read original state: %v", readErr)
		}
		if !reflect.DeepEqual(state, originalState) {
			t.Fatalf("state changed after failed import: got %q, want %q", state, originalState)
		}
	})

	t.Run("should pass correct variables to terraform for AWS", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--aws-vpc-name=test-vpc",
			"--aws-vpc-cidr=10.0.0.0/16",
			"--create-vpc=true",
			"--enable-eks=true",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)

		wantVars := map[string]string{
			"profile":    "test-profile",
			"region":     "us-west-2",
			"vpc_name":   "test-vpc",
			"vpc_cidr":   "10.0.0.0/16",
			"create_vpc": "true",
			"enable_eks": "true",
		}

		for key, want := range wantVars {
			if got := mock.PlanVars[key]; got != want {
				t.Errorf("%s: got %q, want %q", key, got, want)
			}
		}
	})

	t.Run("should expose Cluster API VPC and EKS modes as first-class flags", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--create-vpc=false",
			"--enable-eks=false",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)

		if got := mock.PlanVars["create_vpc"]; got != "false" {
			t.Errorf("create_vpc: got %q, want %q", got, "false")
		}
		if got := mock.PlanVars["enable_eks"]; got != "false" {
			t.Errorf("enable_eks: got %q, want %q", got, "false")
		}
		if got, ok := mock.PlanVars["vpc_name"]; ok {
			t.Errorf("vpc_name should not be set when Cluster API creates the VPC, got %q", got)
		}
		if got, ok := mock.PlanVars["vpc_cidr"]; ok {
			t.Errorf("vpc_cidr should not be set when Cluster API creates the VPC, got %q", got)
		}
	})

	t.Run("should disable VPC creation for a customer-managed VPC", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--customer-managed-vpc",
			"--vpc-id=vpc-09e877f9012f52241",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)

		if got := mock.PlanVars["customer_managed_vpc"]; got != "true" {
			t.Errorf("customer_managed_vpc: got %q, want %q", got, "true")
		}
		if got := mock.PlanVars["create_vpc"]; got != "false" {
			t.Errorf("create_vpc: got %q, want %q", got, "false")
		}
		if got := mock.PlanVars["vpc_id"]; got != "vpc-09e877f9012f52241" {
			t.Errorf("vpc_id: got %q, want %q", got, "vpc-09e877f9012f52241")
		}
		if got, ok := mock.PlanVars["vpc_name"]; ok {
			t.Errorf("vpc_name should not be set for a customer-managed VPC, got %q", got)
		}
		if got, ok := mock.PlanVars["vpc_cidr"]; ok {
			t.Errorf("vpc_cidr should not be set for a customer-managed VPC, got %q", got)
		}
	})

	t.Run("should require a VPC ID for a customer-managed VPC", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--customer-managed-vpc",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected an error when --vpc-id is omitted")
		}
		if !strings.Contains(err.Error(), "--vpc-id is required with --customer-managed-vpc") {
			t.Fatalf("unexpected error: %v", err)
		}

		assertCallCounts(t, mock, 0, 0, 0)
	})

	t.Run("should reject Terraform VPC creation with a customer-managed VPC", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--customer-managed-vpc",
			"--vpc-id=vpc-09e877f9012f52241",
			"--create-vpc=true",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected an error for contradictory VPC ownership flags")
		}
		if !strings.Contains(err.Error(), "--create-vpc=true cannot be used with --customer-managed-vpc") {
			t.Fatalf("unexpected error: %v", err)
		}

		assertCallCounts(t, mock, 0, 0, 0)
	})

	t.Run("should reject VPC configuration when Cluster API creates the VPC", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--create-vpc=false",
			"--aws-vpc-name=unused",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected an error for VPC configuration in Cluster API mode")
		}
		if !strings.Contains(err.Error(), "--aws-vpc-name can only be used when --create-vpc=true") {
			t.Fatalf("unexpected error: %v", err)
		}

		assertCallCounts(t, mock, 0, 0, 0)
	})

	t.Run("should pass correct variables to terraform for GCP", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"gcp",
			"--project-id=test-project",
			"--region=us-central1",
			"--vpc-name=test-vpc",
			"--create-default-firewall-rules=false",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)

		wantVars := map[string]string{
			"project_id":                    "test-project",
			"region":                        "us-central1",
			"vpc_name":                      "test-vpc",
			"create_default_firewall_rules": "false",
		}

		for key, want := range wantVars {
			if got := mock.PlanVars[key]; got != want {
				t.Errorf("%s: got %q, want %q", key, got, want)
			}
		}
	})

	t.Run("should pass --tf-var values to terraform", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			`--tf-var=controller_trusted_role_arns=["arn:example1", "arn:example2"]`,
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)

		if got := mock.PlanVars["controller_trusted_role_arns"]; got != `["arn:example1", "arn:example2"]` {
			t.Errorf("controller_trusted_role_arns: got %q, want %q", got, `["arn:example1", "arn:example2"]`)
		}
	})

	t.Run("should pass controller-trusted-role-arns and iam-trusted-role-arns as JSON lists", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--controller-trusted-role-arns=arn:aws:iam::123456789012:role/controller-role",
			"--controller-trusted-role-arns=arn:aws:iam::123456789012:role/valet-controller-role",
			"--iam-trusted-role-arns=arn:aws:iam::123456789012:role/trust-editor-role",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)

		wantControllerARNs := `["arn:aws:iam::123456789012:role/controller-role","arn:aws:iam::123456789012:role/valet-controller-role"]`
		if got := mock.PlanVars["controller_trusted_role_arns"]; got != wantControllerARNs {
			t.Errorf("controller_trusted_role_arns: got %q, want %q", got, wantControllerARNs)
		}

		wantIAMARNs := `["arn:aws:iam::123456789012:role/trust-editor-role"]`
		if got := mock.PlanVars["iam_trusted_role_arns"]; got != wantIAMARNs {
			t.Errorf("iam_trusted_role_arns: got %q, want %q", got, wantIAMARNs)
		}
	})

	t.Run("should error on invalid --tf-var format", func(t *testing.T) {
		cmd, _ := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--tf-var=invalid_format_without_equals",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		err := cmd.Execute()
		if err == nil {
			t.Fatal("expected error for invalid --tf-var format, got nil")
		}

		if !strings.Contains(err.Error(), "invalid --tf-var format") {
			t.Errorf("expected error message about invalid format, got: %v", err)
		}
	})
}
