package bootstrap

import (
	"context"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/getditto/dittocloud/cmd/internal/log"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
)

// mockTerraformExecutor records terraform operations for verification
type mockTerraformExecutor struct {
	// Call counts
	initCallCount  int
	planCallCount  int
	applyCallCount int

	// Parsed variables from Plan() call
	PlanVars map[string]string

	// Return values
	planReturnChanged bool
	planReturnError   error
	applyReturnError  error
	outputReturn      map[string]tfexec.OutputMeta
}

func (m *mockTerraformExecutor) Init(ctx context.Context, opts ...tfexec.InitOption) error {
	m.initCallCount++
	return nil
}

func (m *mockTerraformExecutor) Plan(ctx context.Context, opts ...tfexec.PlanOption) (bool, error) {
	m.planCallCount++

	// Extract and parse variables from plan options
	m.PlanVars = make(map[string]string)
	for _, opt := range opts {
		varOpt, ok := opt.(*tfexec.VarOption)
		if !ok {
			continue
		}

		// Use reflection to access the unexported field containing "key=value"
		val := reflect.ValueOf(varOpt).Elem()
		if val.Kind() != reflect.Struct {
			continue
		}

		for i := 0; i < val.NumField(); i++ {
			field := val.Field(i)
			if field.Kind() == reflect.String && field.String() != "" {
				varString := field.String()

				// Parse "key=value" into map
				parts := strings.SplitN(varString, "=", 2)
				if len(parts) == 2 {
					m.PlanVars[parts[0]] = parts[1]
				}
			}
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

// setupBootstrapTest creates a test environment with a mocked terraform executor
func setupBootstrapTest(t *testing.T, args []string) (*cobra.Command, *mockTerraformExecutor) {
	t.Helper()

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

func TestBootstrap(t *testing.T) {
	t.Run("should pass correct variables to terraform for AWS", func(t *testing.T) {
		cmd, mock := setupBootstrapTest(t, []string{
			"aws",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--aws-vpc-name=test-vpc",
			"--aws-vpc-cidr=10.0.0.0/16",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)

		wantVars := map[string]string{
			"profile":  "test-profile",
			"region":   "us-west-2",
			"vpc_name": "test-vpc",
			"vpc_cidr": "10.0.0.0/16",
		}

		for key, want := range wantVars {
			if got := mock.PlanVars[key]; got != want {
				t.Errorf("%s: got %q, want %q", key, got, want)
			}
		}
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
			"--tf-var=unrestricted=true",
			"--state=/tmp/test.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0)

		if got := mock.PlanVars["unrestricted"]; got != "true" {
			t.Errorf("unrestricted: got %q, want %q", got, "true")
		}

		if got := mock.PlanVars["controller_trusted_role_arns"]; got != `["arn:example1", "arn:example2"]` {
			t.Errorf("controller_trusted_role_arns: got %q, want %q", got, `["arn:example1", "arn:example2"]`)
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
