package bootstrap

import (
	"context"
	"fmt"

	"github.com/fatih/color"
	"github.com/getditto/ditto-cloud-bootstrap/cmd/internal/log"
	"github.com/hashicorp/terraform-exec/tfexec"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

// azureCmd handles Azure specific variables and mutates the list of vars to be passed to terraform plan/apply
func azureCmd(vars *[]*tfexec.VarOption) *cobra.Command {
	flags := pflag.NewFlagSet("azure", pflag.ContinueOnError)

	cmd := &cobra.Command{
		Use:   "azure",
		Short: "Bootstrap Azure",
		Long:  "Ready an Azure subscription to host Ditto with managed identity and OIDC configuration",
		RunE: func(cmd *cobra.Command, args []string) error {
			logger := log.FromContext(cmd.Context())
			logger.Debug("Processing Azure bootstrap command")

			azureVars, err := promptAzureValues(cmd.Context(), flags)
			if err != nil {
				return fmt.Errorf("unable to prompt for values: %w", err)
			}
			*vars = append(*vars, azureVars...)
			return nil
		},
	}

	// Azure specific flags with defaults from the provided variables
	flags.String("subscription-id", "2aeaecef-0f47-4e9c-ba4d-20f8f04e1f9e", "Azure subscription ID to use")
	flags.String("location", "eastus", "Azure region to deploy resources to")
	flags.String("resource-group", "azure-byoc", "Name of the Azure resource group to create")
	flags.String("identity-name", "azure-byoc", "Name of the user-assigned managed identity")
	flags.String("issuer-url", "https://login.microsoftonline.com/2aeaecef-0f47-4e9c-ba4d-20f8f04e1f9e", "OIDC issuer URL for federated credentials (required)")

	// Mark required flags
	cmd.MarkFlagRequired("issuer-url")

	cmd.Flags().AddFlagSet(flags)

	return cmd
}

func promptAzureValues(ctx context.Context, flags *pflag.FlagSet) ([]*tfexec.VarOption, error) {
	vars := []*tfexec.VarOption{}
	required := color.New(color.FgRed, color.Bold)

	// Confirm all flag values
	flags.VisitAll(func(flag *pflag.Flag) {
		err := flag.Value.Set(StringPrompt(flag.Name, flag.Value.String()))
		if err != nil {
			log.FromContext(ctx).Error("unable to set flag value from prompt", "flag", flag.Name, "error", err)
			if err := flag.Value.Set(""); err != nil {
				log.FromContext(ctx).Error("unexpected error resetting flag value to empty string", "flag", flag.Name, "error", err)
				panic(err)
			}
		}
	})

	// Prompt for unset values
	allValuesSet := false
	for !allValuesSet {
		allValuesSet = true
		flags.VisitAll(func(flag *pflag.Flag) {
			// Skip non-required flags that have default values
			if flag.Value.String() != "" && flag.Name != "issuer-url" {
				return
			}

			val := flag.Value.String()
			if val == "" {
				required.Printf("Input required for %s: ", flag.Name)
				allValuesSet = false
				val = StringPrompt(flag.Name, val)
				err := flag.Value.Set(val)
				if err != nil {
					log.FromContext(ctx).Error("unable to set flag value", "flag", flag.Name, "error", err)
				}
			}
		})
	}

	// Prepare Azure variables
	subscriptionID := fmt.Sprintf("subscription_id=%s", flags.Lookup("subscription-id").Value.String())
	location := fmt.Sprintf("location=%s", flags.Lookup("location").Value.String())
	resourceGroup := fmt.Sprintf("resource_group_name=%s", flags.Lookup("resource-group").Value.String())
	identityName := fmt.Sprintf("identity_name=%s", flags.Lookup("identity-name").Value.String())
	issuerURL := fmt.Sprintf("issuer_url=%s", flags.Lookup("issuer-url").Value.String())

	return append(vars,
		tfexec.Var(subscriptionID),
		tfexec.Var(location),
		tfexec.Var(resourceGroup),
		tfexec.Var(identityName),
		tfexec.Var(issuerURL),
	), nil
}
