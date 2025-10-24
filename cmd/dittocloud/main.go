package main

import (
	"os"

	"github.com/fatih/color"
	"github.com/spf13/cobra"

	"github.com/getditto/dittocloud/cmd/internal/bootstrap"
)

func RootCommand() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "dittocloud",
		Short: "Dittocloud CLI",
		Long:  "Dittocloud CLI",
	}
	cmd.AddCommand(
		bootstrap.BootstrapCmd(),
	)

	return cmd
}

func main() {
	if err := RootCommand().Execute(); err != nil {
		color.Red("Error: %v", err)
		os.Exit(1)
	}
}
