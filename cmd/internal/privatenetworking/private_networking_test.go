package privatenetworking

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
	initCallCount    int
	planCallCount    int
	applyCallCount   int
	destroyCallCount int

	// Parsed variables from Plan() call
	PlanVars map[string]string

	// Parsed variables from Destroy() call
	DestroyVars map[string]string

	// Return values
	planReturnChanged bool
	planReturnError   error
	applyReturnError  error
	destroyReturnError error
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

func (m *mockTerraformExecutor) Destroy(ctx context.Context, opts ...tfexec.DestroyOption) error {
	m.destroyCallCount++

	// Extract and parse variables from destroy options
	m.DestroyVars = make(map[string]string)
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
					m.DestroyVars[parts[0]] = parts[1]
				}
			}
		}
	}

	return m.destroyReturnError
}

func (m *mockTerraformExecutor) Output(ctx context.Context, opts ...tfexec.OutputOption) (map[string]tfexec.OutputMeta, error) {
	return m.outputReturn, nil
}

func (m *mockTerraformExecutor) SetStdout(w io.Writer) {}

func (m *mockTerraformExecutor) SetStderr(w io.Writer) {}

// setupEndpointServiceTest creates a test environment for endpoint-service commands
func setupEndpointServiceTest(t *testing.T, args []string) (*cobra.Command, *mockTerraformExecutor) {
	t.Helper()

	ctx := log.WithLogger(context.Background(), log.Setup("debug"))

	// Save and restore original terraform factory and path finder
	originalFactory := terraformFactory
	originalFinder := terraformPathFinder
	t.Cleanup(func() {
		terraformFactory = originalFactory
		terraformPathFinder = originalFinder
	})

	// Bypass real terraform path resolution
	terraformPathFinder = func(_ context.Context, _ bool) (string, error) {
		return "terraform", nil
	}

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
	cmd := EndpointServiceCmd()
	cmd.SetContext(ctx)
	cmd.SetArgs(args)

	// Disable user input prompts by setting a non-interactive context
	cmd.SetIn(strings.NewReader(""))

	return cmd, mock
}

// setupEndpointTest creates a test environment for endpoint commands
func setupEndpointTest(t *testing.T, args []string) (*cobra.Command, *mockTerraformExecutor) {
	t.Helper()

	ctx := log.WithLogger(context.Background(), log.Setup("debug"))

	// Save and restore original terraform factory and path finder
	originalFactory := terraformFactory
	originalFinder := terraformPathFinder
	t.Cleanup(func() {
		terraformFactory = originalFactory
		terraformPathFinder = originalFinder
	})

	// Bypass real terraform path resolution
	terraformPathFinder = func(_ context.Context, _ bool) (string, error) {
		return "terraform", nil
	}

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
	cmd := EndpointCmd()
	cmd.SetContext(ctx)
	cmd.SetArgs(args)

	// Disable user input prompts by setting a non-interactive context
	cmd.SetIn(strings.NewReader(""))

	return cmd, mock
}

// assertCallCounts verifies that terraform methods were called the expected number of times
func assertCallCounts(t *testing.T, mock *mockTerraformExecutor, init, plan, apply, destroy int) {
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
	if mock.destroyCallCount != destroy {
		t.Errorf("expected %d Destroy call(s), got %d", destroy, mock.destroyCallCount)
	}
}

func TestEndpointServiceCmd(t *testing.T) {
	t.Run("should pass correct variables to terraform", func(t *testing.T) {
		cmd, mock := setupEndpointServiceTest(t, []string{
			"--big-peer-name=test-big-peer",
			"--private-dns-name=test.example.com",
			"--allowed-principals=arn:aws:iam::123456789012:root",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--state=/tmp/test-endpoint-service.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0, 0)

		wantVars := map[string]string{
			"big_peer_name":      "test-big-peer",
			"private_dns_name":   "test.example.com",
			"allowed_principals": `["arn:aws:iam::123456789012:root"]`,
			"profile":            "test-profile",
			"region":             "us-west-2",
		}

		for key, want := range wantVars {
			if got := mock.PlanVars[key]; got != want {
				t.Errorf("%s: got %q, want %q", key, got, want)
			}
		}
	})

	t.Run("should pass --tf-var values to terraform", func(t *testing.T) {
		cmd, mock := setupEndpointServiceTest(t, []string{
			"--big-peer-name=test-big-peer",
			"--private-dns-name=test.example.com",
			"--allowed-principals=arn:aws:iam::123456789012:root",
			"--tf-var=custom_tag=test-value",
			"--state=/tmp/test-endpoint-service.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0, 0)

		if got := mock.PlanVars["custom_tag"]; got != "test-value" {
			t.Errorf("custom_tag: got %q, want %q", got, "test-value")
		}
	})

	t.Run("should error on invalid --tf-var format", func(t *testing.T) {
		cmd, _ := setupEndpointServiceTest(t, []string{
			"--big-peer-name=test-big-peer",
			"--private-dns-name=test.example.com",
			"--allowed-principals=arn:aws:iam::123456789012:root",
			"--tf-var=invalid_format_without_equals",
			"--state=/tmp/test-endpoint-service.tfstate",
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

	t.Run("should call destroy with correct variables when --destroy and --yes are set", func(t *testing.T) {
		cmd, mock := setupEndpointServiceTest(t, []string{
			"--big-peer-name=test-big-peer",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--state=/tmp/test-endpoint-service.tfstate",
			"--destroy",
			"--yes",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 0, 0, 1)

		wantVars := map[string]string{
			"big_peer_name": "test-big-peer",
			"profile":       "test-profile",
			"region":        "us-west-2",
		}
		for key, want := range wantVars {
			if got := mock.DestroyVars[key]; got != want {
				t.Errorf("%s: got %q, want %q", key, got, want)
			}
		}
	})
}

func TestEndpointCmd(t *testing.T) {
	t.Run("should pass correct variables to terraform", func(t *testing.T) {
		cmd, mock := setupEndpointTest(t, []string{
			"--service-name=com.amazonaws.vpce.us-east-2.vpce-svc-123456",
			"--vpc-id=vpc-12345678",
			"--subnet-ids=subnet-111,subnet-222,subnet-333",
			"--private-dns-name=test.example.com",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--state=/tmp/test-endpoint.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0, 0)

		wantVars := map[string]string{
			"service_name":     "com.amazonaws.vpce.us-east-2.vpce-svc-123456",
			"vpc_id":           "vpc-12345678",
			"subnet_ids":       `["subnet-111","subnet-222","subnet-333"]`,
			"private_dns_name": "test.example.com",
			"profile":          "test-profile",
			"region":           "us-west-2",
		}

		for key, want := range wantVars {
			if got := mock.PlanVars[key]; got != want {
				t.Errorf("%s: got %q, want %q", key, got, want)
			}
		}
	})

	t.Run("should format subnet IDs with spaces correctly", func(t *testing.T) {
		cmd, mock := setupEndpointTest(t, []string{
			"--service-name=com.amazonaws.vpce.us-east-2.vpce-svc-123456",
			"--vpc-id=vpc-12345678",
			"--subnet-ids=subnet-111, subnet-222 ,  subnet-333",
			"--private-dns-name=test.example.com",
			"--state=/tmp/test-endpoint.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0, 0)

		// Verify subnet IDs are trimmed and formatted correctly
		want := `["subnet-111","subnet-222","subnet-333"]`
		if got := mock.PlanVars["subnet_ids"]; got != want {
			t.Errorf("subnet_ids: got %q, want %q", got, want)
		}
	})

	t.Run("should call destroy with correct variables when --destroy and --yes are set", func(t *testing.T) {
		cmd, mock := setupEndpointTest(t, []string{
			"--service-name=com.amazonaws.vpce.us-east-2.vpce-svc-123456",
			"--vpc-id=vpc-12345678",
			"--subnet-ids=subnet-111",
			"--private-dns-name=test.example.com",
			"--aws-profile=test-profile",
			"--aws-region=us-west-2",
			"--state=/tmp/test-endpoint.tfstate",
			"--destroy",
			"--yes",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 0, 0, 1)

		wantVars := map[string]string{
			"profile": "test-profile",
			"region":  "us-west-2",
		}
		for key, want := range wantVars {
			if got := mock.DestroyVars[key]; got != want {
				t.Errorf("%s: got %q, want %q", key, got, want)
			}
		}
	})

	t.Run("should pass --tf-var values to terraform", func(t *testing.T) {
		cmd, mock := setupEndpointTest(t, []string{
			"--service-name=com.amazonaws.vpce.us-east-2.vpce-svc-123456",
			"--vpc-id=vpc-12345678",
			"--subnet-ids=subnet-111",
			"--private-dns-name=test.example.com",
			"--tf-var=custom_tag=test-value",
			"--state=/tmp/test-endpoint.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		assertCallCounts(t, mock, 1, 1, 0, 0)

		if got := mock.PlanVars["custom_tag"]; got != "test-value" {
			t.Errorf("custom_tag: got %q, want %q", got, "test-value")
		}
	})

	t.Run("should error on invalid --tf-var format", func(t *testing.T) {
		cmd, _ := setupEndpointTest(t, []string{
			"--service-name=com.amazonaws.vpce.us-east-2.vpce-svc-123456",
			"--vpc-id=vpc-12345678",
			"--subnet-ids=subnet-111",
			"--private-dns-name=test.example.com",
			"--tf-var=invalid_format_without_equals",
			"--state=/tmp/test-endpoint.tfstate",
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

func TestFormatSubnetIDs(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "single subnet",
			input: "subnet-111",
			want:  `"subnet-111"`,
		},
		{
			name:  "multiple subnets",
			input: "subnet-111,subnet-222,subnet-333",
			want:  `"subnet-111","subnet-222","subnet-333"`,
		},
		{
			name:  "subnets with spaces",
			input: "subnet-111, subnet-222 ,  subnet-333",
			want:  `"subnet-111","subnet-222","subnet-333"`,
		},
		{
			name:  "single subnet with leading/trailing spaces",
			input: "  subnet-111  ",
			want:  `"subnet-111"`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := formatSubnetIDs(tt.input)
			if got != tt.want {
				t.Errorf("formatSubnetIDs(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestEndpointServiceMultiplePrincipals(t *testing.T) {
	t.Run("should format multiple allowed principals correctly", func(t *testing.T) {
		cmd, mock := setupEndpointServiceTest(t, []string{
			"--big-peer-name=test-big-peer",
			"--private-dns-name=test.example.com",
			"--allowed-principals=arn:aws:iam::111111111111:root,arn:aws:iam::222222222222:root",
			"--state=/tmp/test-endpoint-service.tfstate",
			"--dry-run",
		})

		if err := cmd.Execute(); err != nil {
			t.Fatalf("unexpected error executing command: %v", err)
		}

		want := `["arn:aws:iam::111111111111:root","arn:aws:iam::222222222222:root"]`
		if got := mock.PlanVars["allowed_principals"]; got != want {
			t.Errorf("allowed_principals: got %q, want %q", got, want)
		}
	})
}

