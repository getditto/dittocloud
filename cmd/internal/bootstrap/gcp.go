package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/fatih/color"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"google.golang.org/api/serviceusage/v1"

	"github.com/getditto/ditto-cloud-bootstrap/cmd/internal/log"
)

// enableRequiredAPIs enables the GCP APIs required for Terraform to run successfully
func enableRequiredAPIs(ctx context.Context, projectID string) error {
	requiredAPIs := []string{
		"compute.googleapis.com",
		"iam.googleapis.com",
	}

	logger := log.FromContext(ctx)
	progress := color.New(color.FgMagenta)

	logger.Debug("Checking and enabling required GCP APIs", "project", projectID)
	progress.Printf("Ensuring required APIs are enabled for project %s...\n", projectID)

	// Create service usage client
	service, err := serviceusage.NewService(ctx)
	if err != nil {
		return fmt.Errorf("failed to create service usage client: %w", err)
	}

	// Check which APIs are already enabled
	enabledAPIs := make(map[string]bool)
	listCall := service.Services.List(fmt.Sprintf("projects/%s", projectID)).Filter("state:ENABLED")

	err = listCall.Pages(ctx, func(page *serviceusage.ListServicesResponse) error {
		for _, svc := range page.Services {
			enabledAPIs[svc.Config.Name] = true
		}
		return nil
	})
	if err != nil {
		return fmt.Errorf("failed to list enabled services: %w", err)
	}

	// Enable any missing APIs
	var apisToEnable []string
	for _, api := range requiredAPIs {
		if !enabledAPIs[api] {
			apisToEnable = append(apisToEnable, api)
		}
	}

	if len(apisToEnable) == 0 {
		logger.Debug("All required APIs are already enabled")
		progress.Println("✅ All required APIs are already enabled")
		return nil
	}

	progress.Printf("Enabling %d APIs: %v\n", len(apisToEnable), apisToEnable)

	// Enable APIs one by one (batch enable isn't always reliable)
	for _, api := range apisToEnable {
		logger.Debug("Enabling API", "api", api)

		req := &serviceusage.EnableServiceRequest{}
		op, err := service.Services.Enable(fmt.Sprintf("projects/%s/services/%s", projectID, api), req).Context(ctx).Do()
		if err != nil {
			return fmt.Errorf("failed to enable API %s: %w", api, err)
		}

		// Wait for the operation to complete
		for !op.Done {
			time.Sleep(2 * time.Second)
			op, err = service.Operations.Get(op.Name).Context(ctx).Do()
			if err != nil {
				return fmt.Errorf("failed to check operation status for API %s: %w", api, err)
			}
		}

		if op.Error != nil {
			return fmt.Errorf("failed to enable API %s: %s", api, op.Error.Message)
		}

		progress.Printf("✅ Enabled %s\n", api)
	}

	// Wait a bit for APIs to fully propagate
	logger.Debug("Waiting for API enablement to propagate")
	progress.Println("Waiting for API enablement to propagate...")
	time.Sleep(10 * time.Second)

	return nil
}

// checkGCPAuth verifies that the user has proper GCP authentication
func checkGCPAuth(ctx context.Context) error {
	logger := log.FromContext(ctx)

	// Try to create a service usage client to verify authentication
	_, err := serviceusage.NewService(ctx)
	if err != nil {
		logger.Error("Failed to authenticate with GCP", "error", err)
		return fmt.Errorf("GCP authentication failed. Please run 'gcloud auth application-default login' or set up service account credentials: %w", err)
	}

	logger.Debug("GCP authentication verified")
	return nil
}

// GCPConfig holds GCP-specific configuration
type GCPConfig struct {
	ProjectID                  string
	Region                     string
	CreateSubnets              bool
	CreateDefaultFirewallRules bool
	SubnetCidr                 string
	PodsCidrRange              string
	ServicesCidrRange          string
}

func (g *GCPConfig) BuildTFVars() []*tfexec.VarOption {

	var vars []*tfexec.VarOption
	vars = append(vars,
		tfexec.Var("project_id="+g.ProjectID),
		tfexec.Var("region="+g.Region),
	)

	// Convert vpc_config to JSON using map
	vpcConfigMap := map[string]string{
		"create_subnets":                fmt.Sprintf("%t", g.CreateSubnets),
		"create_default_firewall_rules": fmt.Sprintf("%t", g.CreateDefaultFirewallRules),
		"subnet_cidr":                   g.SubnetCidr,
		"pods_cidr_range":               g.PodsCidrRange,
		"services_cidr_range":           g.ServicesCidrRange,
	}

	vpcConfigJSON, err := json.Marshal(vpcConfigMap)
	if err != nil {
		// Log error but continue with empty vpc_config
		log.FromContext(context.Background()).Error("Failed to marshal vpc_config", "error", err)
		vars = append(vars, tfexec.Var("vpc_config={}"))
	} else {
		vars = append(vars, tfexec.Var("vpc_config="+string(vpcConfigJSON)))
	}
	return vars
}

func (g *GCPConfig) BucketURL() (string, error) {
	if g.ProjectID == "" {
		return "", fmt.Errorf("project ID is required for GCP state management")
	}
	return fmt.Sprintf("gs://ditto-terraform-state-%s", g.ProjectID), nil
}

func (g *GCPConfig) GetBackendConfig() (TerraformBackendConfig, error) {
	if g.ProjectID == "" {
		return nil, fmt.Errorf("project ID is required for GCP state management")
	}

	bucketName := fmt.Sprintf("ditto-terraform-state-%s", g.ProjectID)
	return &GCPBackendConfig{
		BucketName: bucketName,
		Prefix:     "terraform/state",
	}, nil
}

// gcpCmd handles gcp specific variables and populates the config
func gcpCmd(config *GCPConfig) *cobra.Command {
	flags := pflag.NewFlagSet("gcp", pflag.ContinueOnError)

	cmd := &cobra.Command{
		Use:   "gcp",
		Short: "Bootstrap GCP",
		Long:  "Ready a GCP project to host Ditto",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.FromContext(cmd.Context())
			logger.Debug("Processing GCP bootstrap command")

			err := promptGcpValues(cmd.Context(), flags, config)
			if err != nil {
				return fmt.Errorf("unable to prompt for values: %w", err)
			}

			// Check GCP authentication first
			if err := checkGCPAuth(cmd.Context()); err != nil {
				return err
			}

			// Enable required APIs before the bootstrap process continues
			if err := enableRequiredAPIs(cmd.Context(), config.ProjectID); err != nil {
				return fmt.Errorf("failed to enable required GCP APIs: %w", err)
			}

			return nil
		},
	}

	flags.String("project-id", "", "GCP project ID to use")
	flags.String("region", "", "GCP region to use")
	flags.Bool("create-subnets", false, "Create subnets and firewall rules")
	flags.Bool("create-default-firewall-rules", false, "Create default firewall rules")
	flags.String("subnet-cidr", "10.140.0.0/19", "Subnet CIDR to use")
	flags.String("pods-cidr-range", "100.90.0.0/16", "Pods CIDR range to use")
	flags.String("services-cidr-range", "100.91.0.0/16", "Services CIDR range to use")

	cmd.Flags().AddFlagSet(flags)
	// cmd.MarkFlagRequired("project-id")
	// cmd.MarkFlagRequired("region")

	return cmd
}

func promptGcpValues(ctx context.Context, flags *pflag.FlagSet, gcpConfig *GCPConfig) error {
	// optional := color.New(color.FgYellow)
	required := color.New(color.FgRed, color.Bold)
	// failed := color.New(color.FgRed)

	// confirm all flag values
	flags.VisitAll(func(flag *pflag.Flag) {
		// is this correctly handling bool?
		err := flag.Value.Set(StringPrompt(flag.Name, flag.Value.String()))
		if err != nil {
			log.FromContext(ctx).Error("unable to set flag value from prompt", "flag", flag.Name, "error", err)
			// set the flag value to empty, since we overwrote the default value, the intent was to probably
			// provide a different value, and we made a typo here. Resetting the flag value to empty will
			// cause the flag to be prompted for via the allValuesSet check below.
			if err := flag.Value.Set(""); err != nil {
				log.FromContext(ctx).Error("unexpected error resetting flag value to empty string", "flag", flag.Name, "error", err)
				panic(err)
			}
		}
	})

	// prompt for unset values
	allValuesSet := false
	for !allValuesSet {
		// flip allValuesSet back to false if any flag value is empty
		allValuesSet = true
		flags.VisitAll(func(flag *pflag.Flag) {
			var val string
			val = flag.Value.String()
			if val == "" {
				required.Println("Input required for flag: ", flag.Name)
				allValuesSet = false
				val = StringPrompt(flag.Name, val)
			}
			err := flag.Value.Set(val)
			if err != nil {
				log.FromContext(ctx).Error("unable to set flag value", "flag", flag.Name, "error", err)
			}
		})
	}

	gcpConfig.ProjectID = flags.Lookup("project-id").Value.String()
	gcpConfig.Region = flags.Lookup("region").Value.String()
	gcpConfig.CreateSubnets = flags.Lookup("create-subnets").Value.String() == "true"
	gcpConfig.CreateDefaultFirewallRules = flags.Lookup("create-default-firewall-rules").Value.String() == "true"
	gcpConfig.SubnetCidr = flags.Lookup("subnet-cidr").Value.String()
	gcpConfig.PodsCidrRange = flags.Lookup("pods-cidr-range").Value.String()
	gcpConfig.ServicesCidrRange = flags.Lookup("services-cidr-range").Value.String()

	log.FromContext(ctx).Debug("GCP configuration",
		"project_id", gcpConfig.ProjectID,
		"region", gcpConfig.Region,
		"create_subnets", gcpConfig.CreateSubnets,
		"create_default_firewall_rules", gcpConfig.CreateDefaultFirewallRules,
		"subnet_cidr", gcpConfig.SubnetCidr,
		"pods_cidr_range", gcpConfig.PodsCidrRange,
		"services_cidr_range", gcpConfig.ServicesCidrRange,
	)

	return nil
}

// GCPBackendConfig implements TerraformBackendConfig for GCP
type GCPBackendConfig struct {
	BucketName string
	Prefix     string
}

func (c *GCPBackendConfig) BackendConfigFile() (string, error) {
	return `terraform {
  backend "gcs" {}
}
`, nil
}

func (c *GCPBackendConfig) GetBackendConfig() ([]tfexec.InitOption, error) {
	return []tfexec.InitOption{
		tfexec.BackendConfig(fmt.Sprintf("bucket=%s", c.BucketName)),
		tfexec.BackendConfig(fmt.Sprintf("prefix=%s", c.Prefix)),
	}, nil
}

func (c *GCPBackendConfig) GetBackendType() string {
	return "gcs"
}
