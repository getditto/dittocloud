package bootstrap

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/fatih/color"
	"github.com/getditto/dittocloud/cmd/internal/log"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// gcpCmd handles gcp specific variables and mutates the list of vars to be passed to terraform plan/apply
func gcpCmd(vars *[]*tfexec.VarOption) *cobra.Command {
	flags := pflag.NewFlagSet("gcp", pflag.ContinueOnError)

	cmd := &cobra.Command{
		Use:   "gcp",
		Short: "Bootstrap GCP",
		Long:  "Ready a GCP project to host Ditto",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.FromContext(cmd.Context())
			logger.Debug("Processing GCP bootstrap command")

			gcpVars, err := promptGcpValues(cmd.Context(), flags)
			if err != nil {
				return fmt.Errorf("unable to prompt for values: %w", err)
			}
			*vars = append(*vars, gcpVars...)
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

func promptGcpValues(ctx context.Context, flags *pflag.FlagSet) ([]*tfexec.VarOption, error) {
	vars := []*tfexec.VarOption{}
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

	projectId := fmt.Sprintf("project_id=%s", flags.Lookup("project-id").Value.String())
	log.FromContext(ctx).Debug("terraform variable", "project_id", projectId)
	region := fmt.Sprintf("region=%s", flags.Lookup("region").Value.String())
	log.FromContext(ctx).Debug("terraform variable", "region", region)
	vpcConfigMap := map[string]string{
		"create_subnets":                flags.Lookup("create-subnets").Value.String(),
		"create_default_firewall_rules": flags.Lookup("create-default-firewall-rules").Value.String(),
		"subnet_cidr":                   flags.Lookup("subnet-cidr").Value.String(),
		"pods_cidr_range":               flags.Lookup("pods-cidr-range").Value.String(),
		"services_cidr_range":           flags.Lookup("services-cidr-range").Value.String(),
	}

	vpcConfigJson, err := json.Marshal(vpcConfigMap)
	if err != nil {
		return nil, fmt.Errorf("unable to marshal vpc config: %w", err)
	}
	vpcConfig := fmt.Sprintf("vpc_config=%s", string(vpcConfigJson))
	log.FromContext(ctx).Debug("terraform variable", "vpc_config", vpcConfig)
	return append(vars, tfexec.Var(projectId), tfexec.Var(region), tfexec.Var(vpcConfig)), nil
}
