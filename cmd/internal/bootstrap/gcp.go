package bootstrap

import (
	"context"
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
	flags.String("vpc-name", "ditto-vpc", "GCP VPC name to use")
	flags.Bool("create-default-firewall-rules", false, "Create default firewall rules for internal VPC traffic")
	cmd.Flags().AddFlagSet(flags)

	return cmd
}

func promptGcpValues(ctx context.Context, flags *pflag.FlagSet) ([]*tfexec.VarOption, error) {
	vars := []*tfexec.VarOption{}
	required := color.New(color.FgRed, color.Bold)

	// confirm all flag values
	flags.VisitAll(func(flag *pflag.Flag) {
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


	// Build terraform variables
	projectId := fmt.Sprintf("project_id=%s", flags.Lookup("project-id").Value.String())
	log.FromContext(ctx).Debug("terraform variable", "project_id", projectId)
	vars = append(vars, tfexec.Var(projectId))

	region := fmt.Sprintf("region=%s", flags.Lookup("region").Value.String())
	log.FromContext(ctx).Debug("terraform variable", "region", region)
	vars = append(vars, tfexec.Var(region))

	// Add optional vpc-name if provided
	if vpcNameFlag := flags.Lookup("vpc-name"); vpcNameFlag != nil && vpcNameFlag.Value.String() != "" {
		vpcName := fmt.Sprintf("vpc_name=%s", vpcNameFlag.Value.String())
		log.FromContext(ctx).Debug("terraform variable", "vpc_name", vpcName)
		vars = append(vars, tfexec.Var(vpcName))
	}

	// Add optional create-default-firewall-rules if provided
	if firewallFlag := flags.Lookup("create-default-firewall-rules"); firewallFlag != nil && firewallFlag.Value.String() != "" {
		createFirewallRules := fmt.Sprintf("create_default_firewall_rules=%s", firewallFlag.Value.String())
		log.FromContext(ctx).Debug("terraform variable", "create_default_firewall_rules", createFirewallRules)
		vars = append(vars, tfexec.Var(createFirewallRules))
	}

	return vars, nil
}
